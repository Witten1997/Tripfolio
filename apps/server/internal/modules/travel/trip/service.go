package trip

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/metadata"
	"tripfolio/server/internal/modules/travel/routeplan"
)

// Service 实现旅行与回收站用例（接口设计 2.2、3.2、§5）。
type Service struct {
	uow          write.UnitOfWork[Repo]
	reader       Reader
	cursors      paging.Codec
	clock        clock.Clock
	reauthWindow time.Duration
}

// NewService 创建服务；reauthWindow 是永久清理要求的密码复验窗口（接口设计 1.4：5 分钟）。
func NewService(uow write.UnitOfWork[Repo], reader Reader, cursors paging.Codec, clk clock.Clock, reauthWindow time.Duration) *Service {
	return &Service{uow: uow, reader: reader, cursors: cursors, clock: clk, reauthWindow: reauthWindow}
}

const (
	maxNameChars                    = 120
	maxDestinationChars             = 300
	maxNotesChars                   = 10000
	maxTimezoneChars                = 64
	defaultRouteShortDistanceMeters = int32(1500)
)

// 错误代码（接口设计 1.4）。
const (
	codeTripDeleted          = "TRIP_DELETED"
	codeCurrencyLocked       = "CURRENCY_LOCKED"
	codeCurrencyAmountsExist = "CURRENCY_AMOUNTS_EXIST"
	codeRestoreUnavailable   = "RESTORE_UNAVAILABLE"
	codeReauthRequired       = "REAUTH_REQUIRED"
	codeIDAlreadyUsed        = "ID_ALREADY_USED"
)

func tripDeleted() *apperr.Error {
	return apperr.Gone(codeTripDeleted, "旅行已在回收站中")
}

func validateName(name string) (string, *apperr.FieldError) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameChars {
		e := apperr.Field("name", "INVALID", fmt.Sprintf("旅行名称须为 1–%d 个字符", maxNameChars))
		return "", &e
	}
	return name, nil
}

func validateText(field, value string, max int) *apperr.FieldError {
	if utf8.RuneCountInString(value) > max {
		e := apperr.Field(field, "TOO_LONG", fmt.Sprintf("最多 %d 个字符", max))
		return &e
	}
	if !utf8.ValidString(value) {
		e := apperr.Field(field, "INVALID", "必须是有效的 UTF-8 文本")
		return &e
	}
	return nil
}

func validateTimezone(tz string) *apperr.FieldError {
	if tz == "" || tz == "Local" || len(tz) > maxTimezoneChars {
		e := apperr.Field("timezone", "INVALID", "不是有效的 IANA 时区")
		return &e
	}
	if _, err := time.LoadLocation(tz); err != nil {
		e := apperr.Field("timezone", "INVALID", "不是有效的 IANA 时区")
		return &e
	}
	return nil
}

func validateCurrency(code string) *apperr.FieldError {
	if _, ok := metadata.MinorUnits(code); !ok {
		e := apperr.Field("currency_code", "INVALID", "不支持的币种")
		return &e
	}
	return nil
}

func parseDate(field, raw string) (types.Date, *apperr.FieldError) {
	d, err := types.ParseDate(raw)
	if err != nil {
		e := apperr.Field(field, "INVALID", "日期格式必须是 YYYY-MM-DD")
		return "", &e
	}
	return d, nil
}

// compactBudget 做币种无关的格式校验并返回最简形式，用于指纹；小数位按币种的校验在事务内完成。
func compactBudget(raw string) (string, *apperr.FieldError) {
	out, err := money.Compact(raw)
	if err != nil {
		e := apperr.Field("budget_amount", "INVALID", budgetMessage(err))
		return "", &e
	}
	return out, nil
}

func budgetMessage(err error) string {
	switch {
	case errors.Is(err, money.ErrNegative):
		return "预算不能为负数"
	case errors.Is(err, money.ErrScaleExceeded):
		return "预算小数位超过币种允许的位数"
	case errors.Is(err, money.ErrTooLarge):
		return "预算超出可记录范围"
	default:
		return "预算格式不正确"
	}
}

// canonicalBudget 按币种规范化预算；nil 原样返回。
func canonicalBudget(budget *string, currency string) (*string, error) {
	if budget == nil {
		return nil, nil
	}
	units, ok := metadata.MinorUnits(currency)
	if !ok {
		return nil, apperr.Validation(apperr.Field("currency_code", "INVALID", "不支持的币种"))
	}
	out, err := money.Canonicalize(*budget, units)
	if err != nil {
		return nil, apperr.Validation(apperr.Field("budget_amount", "INVALID", budgetMessage(err)))
	}
	return &out, nil
}

func (s *Service) reload(a actor.Actor, id uuid.UUID) func(ctx context.Context, repo Repo) (any, error) {
	return func(ctx context.Context, repo Repo) (any, error) {
		r, found, err := repo.Get(ctx, a.AccountID, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, apperr.NotFound()
		}
		return r, nil
	}
}

func ref(r Resource) write.EntityRef { return write.Ref(EntityType, r.ID, int64(r.Version)) }

func record(scope write.Scope, r Resource, kind write.ChangeKind, fields []string, requiresSnapshot bool) {
	id := r.ID
	change := write.Change{EntityType: EntityType, EntityID: r.ID, TripID: &id, Version: int64(r.Version), Kind: kind, RequiresSnapshot: requiresSnapshot}
	if kind == write.ChangeUpsert {
		change.Snapshot = r
		change.ChangedFields = fields
	}
	scope.Record(change)
}

// Get 返回有效旅行；不存在或非本人 404，回收站中 410 TRIP_DELETED。
func (s *Service) Get(ctx context.Context, a actor.Actor, id uuid.UUID) (Resource, error) {
	r, found, err := s.reader.Get(ctx, a.AccountID, id)
	if err != nil {
		return Resource{}, apperr.Internal(err)
	}
	if !found {
		return Resource{}, apperr.NotFound()
	}
	if r.DeletedAt != nil {
		return Resource{}, tripDeleted()
	}
	return r, nil
}

// GetTrashed 返回回收站中的旅行；不在回收站或非本人 404。
func (s *Service) GetTrashed(ctx context.Context, a actor.Actor, id uuid.UUID) (Resource, error) {
	r, found, err := s.reader.Get(ctx, a.AccountID, id)
	if err != nil {
		return Resource{}, apperr.Internal(err)
	}
	if !found || r.DeletedAt == nil {
		return Resource{}, apperr.NotFound()
	}
	return r, nil
}

func listScope(f Filters) string {
	phase := ""
	if f.Phase != nil {
		phase = string(*f.Phase)
	}
	return fmt.Sprintf("trips|sort=%s|archived=%s|phase=%s|q=%s", f.Sort, f.Archived, phase, f.Query)
}

const trashedScope = "recycle-bin/trips"

// List 返回一页有效旅行（接口设计 3.9 TripFilters）。
func (s *Service) List(ctx context.Context, a actor.Actor, f Filters) (paging.Page[ListItem], error) {
	var fields []apperr.FieldError
	if utf8.RuneCountInString(f.Query) > maxQueryChars {
		fields = append(fields, apperr.Field("q", "TOO_LONG", fmt.Sprintf("最多 %d 个字符", maxQueryChars)))
	}
	if f.Phase != nil && !f.Phase.Valid() {
		fields = append(fields, apperr.Field("phase", "INVALID", "阶段须为 planned、ongoing 或 ended"))
	}
	if f.Archived == "" {
		f.Archived = ArchivedAll
	} else if !f.Archived.Valid() {
		fields = append(fields, apperr.Field("archived", "INVALID", "须为 true、false 或 all"))
	}
	if f.Sort == "" {
		f.Sort = SortStartDateDesc
	} else if !f.Sort.Valid() {
		fields = append(fields, apperr.Field("sort", "INVALID", "须为 start_date_desc、start_date_asc 或 updated_at_desc"))
	}
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		if e, ok := apperr.As(err); ok {
			fields = append(fields, e.Fields...)
		} else {
			return paging.Page[ListItem]{}, err
		}
	}
	if len(fields) > 0 {
		return paging.Page[ListItem]{}, apperr.Validation(fields...)
	}
	scope := listScope(f)
	var after *Position
	if f.Cursor != "" {
		var p Position
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &p); err != nil {
			return paging.Page[ListItem]{}, paging.InvalidCursor()
		}
		after = &p
	}
	items, err := s.reader.List(ctx, a.AccountID, ListQuery{Filters: f, Now: s.clock.Now(), Limit: limit + 1, After: after})
	if err != nil {
		return paging.Page[ListItem]{}, apperr.Internal(err)
	}
	page := paging.Page[ListItem]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1].Resource
		pos := Position{ID: last.ID}
		if f.Sort == SortUpdatedAtDesc {
			t := last.UpdatedAt
			pos.UpdatedAt = &t
		} else {
			pos.StartDate = last.StartDate
		}
		token, err := s.cursors.Encode(a.AccountID, scope, pos)
		if err != nil {
			return paging.Page[ListItem]{}, apperr.Internal(err)
		}
		page.NextCursor = &token
	}
	if page.Items == nil {
		page.Items = []ListItem{}
	}
	return page, nil
}

// ListTrashed 返回一页回收站旅行，按删除时间倒序。
func (s *Service) ListTrashed(ctx context.Context, a actor.Actor, requestedLimit int, cursor string) (paging.Page[Resource], error) {
	limit, err := paging.Limit(requestedLimit)
	if err != nil {
		return paging.Page[Resource]{}, err
	}
	var after *Position
	if cursor != "" {
		var p Position
		if err := s.cursors.Decode(a.AccountID, trashedScope, cursor, &p); err != nil {
			return paging.Page[Resource]{}, paging.InvalidCursor()
		}
		after = &p
	}
	items, err := s.reader.ListTrashed(ctx, a.AccountID, TrashedQuery{Limit: limit + 1, After: after})
	if err != nil {
		return paging.Page[Resource]{}, apperr.Internal(err)
	}
	page := paging.Page[Resource]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		token, err := s.cursors.Encode(a.AccountID, trashedScope, Position{DeletedAt: last.DeletedAt, ID: last.ID})
		if err != nil {
			return paging.Page[Resource]{}, apperr.Internal(err)
		}
		page.NextCursor = &token
	}
	if page.Items == nil {
		page.Items = []Resource{}
	}
	return page, nil
}

// CreateCommand 是创建命令（接口设计 3.2 TripCreate）；nil 表示缺省，由服务端填默认值。
type CreateCommand struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	StartDate    string    `json:"start_date"`
	EndDate      string    `json:"end_date"`
	Destination  *string   `json:"destination"`
	Notes        *string   `json:"notes"`
	Timezone     *string   `json:"timezone"`
	CurrencyCode *string   `json:"currency_code"`
	BudgetAmount *string   `json:"budget_amount"`
}

// Create 新建旅行。
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID uuid.UUID, cmd CreateCommand) (write.Result, error) {
	var fields []apperr.FieldError
	addField := func(e *apperr.FieldError) {
		if e != nil {
			fields = append(fields, *e)
		}
	}
	if cmd.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "INVALID", "id 必填"))
	}
	name, ferr := validateName(cmd.Name)
	addField(ferr)
	cmd.Name = name
	start, ferr := parseDate("start_date", cmd.StartDate)
	addField(ferr)
	end, ferr := parseDate("end_date", cmd.EndDate)
	addField(ferr)
	if start != "" && end != "" && end.Before(start) {
		fields = append(fields, apperr.Field("end_date", "DATE_ORDER", "结束日期不能早于开始日期"))
	}
	if cmd.Destination != nil {
		d := strings.TrimSpace(*cmd.Destination)
		cmd.Destination = &d
		addField(validateText("destination", d, maxDestinationChars))
	}
	if cmd.Notes != nil {
		addField(validateText("notes", *cmd.Notes, maxNotesChars))
	}
	if cmd.Timezone != nil {
		addField(validateTimezone(*cmd.Timezone))
	}
	if cmd.CurrencyCode != nil {
		addField(validateCurrency(*cmd.CurrencyCode))
	}
	if cmd.BudgetAmount != nil {
		compact, ferr := compactBudget(*cmd.BudgetAmount)
		addField(ferr)
		cmd.BudgetAmount = &compact
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip.create",
		Fingerprint: write.Fingerprint("trip.create", cmd.ID.String(), nil, cmd),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		exists, err := repo.IDExists(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
		}
		tz := ""
		if cmd.Timezone != nil {
			tz = *cmd.Timezone
		} else {
			tz, err = repo.AccountDefaultTimezone(ctx, a.AccountID)
			if err != nil {
				return err
			}
		}
		currency := metadata.DefaultCurrencyCode
		if cmd.CurrencyCode != nil {
			currency = *cmd.CurrencyCode
		}
		budget, err := canonicalBudget(cmd.BudgetAmount, currency)
		if err != nil {
			return err
		}
		destination, notes := "", ""
		if cmd.Destination != nil {
			destination = *cmd.Destination
		}
		if cmd.Notes != nil {
			notes = *cmd.Notes
		}
		now := s.clock.Now()
		created, err := repo.Insert(ctx, a.AccountID, Resource{
			ID: cmd.ID, Name: cmd.Name, StartDate: start, EndDate: end, Destination: destination, Notes: notes,
			Timezone: tz, CurrencyCode: currency, BudgetAmount: budget, RouteShortMode: string(routeplan.ModeWalking),
			RouteShortDistanceMeters: defaultRouteShortDistanceMeters, Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		record(scope, created, write.ChangeUpsert, CreateFields, false)
		scope.SetPrimary(ref(created))
		return nil
	}, s.reload(a, cmd.ID))
}

// Patch 是局部更新（接口设计 3.2 TripPatch）；nil 表示缺省。BudgetSet 区分“清空预算”与缺省。
type Patch struct {
	Name                     *string `json:"name"`
	StartDate                *string `json:"start_date"`
	EndDate                  *string `json:"end_date"`
	Destination              *string `json:"destination"`
	Notes                    *string `json:"notes"`
	Timezone                 *string `json:"timezone"`
	CurrencyCode             *string `json:"currency_code"`
	BudgetSet                bool    `json:"budget_set"`
	BudgetAmount             *string `json:"budget_amount"`
	RouteShortMode           *string `json:"route_short_mode"`
	RouteShortDistanceMeters *int32  `json:"route_short_distance_meters"`
}

func (p Patch) fields() []string {
	var f []string
	if p.Name != nil {
		f = append(f, "name")
	}
	if p.StartDate != nil {
		f = append(f, "start_date")
	}
	if p.EndDate != nil {
		f = append(f, "end_date")
	}
	if p.Destination != nil {
		f = append(f, "destination")
	}
	if p.Notes != nil {
		f = append(f, "notes")
	}
	if p.Timezone != nil {
		f = append(f, "timezone")
	}
	if p.CurrencyCode != nil {
		f = append(f, "currency_code")
	}
	if p.BudgetSet {
		f = append(f, "budget_amount")
	}
	if p.RouteShortMode != nil {
		f = append(f, "route_short_mode")
	}
	if p.RouteShortDistanceMeters != nil {
		f = append(f, "route_short_distance_meters")
	}
	return f
}

// Update 局部更新旅行，按字段级合并规则处理基线版本（接口设计 1.2、3.2）。
func (s *Service) Update(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, baseVersion int64, patch Patch) (write.Result, error) {
	var fields []apperr.FieldError
	addField := func(e *apperr.FieldError) {
		if e != nil {
			fields = append(fields, *e)
		}
	}
	var start, end types.Date
	if patch.Name != nil {
		name, ferr := validateName(*patch.Name)
		addField(ferr)
		patch.Name = &name
	}
	if patch.StartDate != nil {
		d, ferr := parseDate("start_date", *patch.StartDate)
		addField(ferr)
		start = d
	}
	if patch.EndDate != nil {
		d, ferr := parseDate("end_date", *patch.EndDate)
		addField(ferr)
		end = d
	}
	if patch.Destination != nil {
		d := strings.TrimSpace(*patch.Destination)
		patch.Destination = &d
		addField(validateText("destination", d, maxDestinationChars))
	}
	if patch.Notes != nil {
		addField(validateText("notes", *patch.Notes, maxNotesChars))
	}
	if patch.Timezone != nil {
		addField(validateTimezone(*patch.Timezone))
	}
	if patch.CurrencyCode != nil {
		addField(validateCurrency(*patch.CurrencyCode))
	}
	if patch.BudgetSet && patch.BudgetAmount != nil {
		compact, ferr := compactBudget(*patch.BudgetAmount)
		addField(ferr)
		patch.BudgetAmount = &compact
	}
	if patch.RouteShortMode != nil && *patch.RouteShortMode != string(routeplan.ModeWalking) && *patch.RouteShortMode != string(routeplan.ModeCycling) {
		fields = append(fields, apperr.Field("route_short_mode", "INVALID", "短途方式须为步行或骑行"))
	}
	if patch.RouteShortDistanceMeters != nil && (*patch.RouteShortDistanceMeters < 0 || *patch.RouteShortDistanceMeters > 50000) {
		fields = append(fields, apperr.Field("route_short_distance_meters", "OUT_OF_RANGE", "短途距离须在 0-50000 米之间"))
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	submitted := patch.fields()
	if len(submitted) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("", "EMPTY_PATCH", "没有可更新的字段"))
	}
	base := baseVersion
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip.update",
		Fingerprint: write.Fingerprint("trip.update", id.String(), &base, patch),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, id)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return tripDeleted()
		}
		decision, err := write.ResolvePatch(ctx, repo.MergeSource(), a.AccountID, EntityType, id, baseVersion, int64(current.Version), submitted)
		if err != nil {
			return err
		}
		if !decision.Merge {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityType, EntityID: id, ExpectedVersion: baseVersion,
				CurrentVersion: int64(current.Version), ConflictingFields: decision.Conflicting, Current: current,
			})
		}
		if decision.Merged {
			scope.Warn(write.WarnMergedWithNewerVersion)
		}
		v := current.Values()
		if patch.Name != nil {
			v.Name = *patch.Name
		}
		if patch.StartDate != nil {
			v.StartDate = start
		}
		if patch.EndDate != nil {
			v.EndDate = end
		}
		if patch.Destination != nil {
			v.Destination = *patch.Destination
		}
		if patch.Notes != nil {
			v.Notes = *patch.Notes
		}
		if patch.Timezone != nil {
			v.Timezone = *patch.Timezone
		}
		if patch.CurrencyCode != nil {
			v.CurrencyCode = *patch.CurrencyCode
		}
		if patch.RouteShortMode != nil {
			v.RouteShortMode = *patch.RouteShortMode
		}
		if patch.RouteShortDistanceMeters != nil {
			v.RouteShortDistanceMeters = *patch.RouteShortDistanceMeters
		}
		if v.EndDate.Before(v.StartDate) {
			field := "end_date"
			if patch.EndDate == nil {
				field = "start_date"
			}
			return apperr.Validation(apperr.Field(field, "DATE_ORDER", "结束日期不能早于开始日期"))
		}
		if v.CurrencyCode != current.CurrencyCode {
			if current.CurrencyLockedAt != nil {
				return apperr.Conflicted(codeCurrencyLocked, "旅行已有账目，币种不可更改；删除全部账目后可再次修改")
			}
			if !patch.BudgetSet && current.BudgetAmount != nil {
				return apperr.Conflicted(codeCurrencyAmountsExist, "请先清空总预算再更改币种")
			}
			has, err := repo.HasEstimatedAmounts(ctx, a.AccountID, id)
			if err != nil {
				return err
			}
			if has {
				return apperr.Conflicted(codeCurrencyAmountsExist, "请先清空行程项目的预计费用再更改币种")
			}
		}
		if patch.BudgetSet {
			budget, err := canonicalBudget(patch.BudgetAmount, v.CurrencyCode)
			if err != nil {
				return err
			}
			v.BudgetAmount = budget
		}
		if v.StartDate != current.StartDate || v.EndDate != current.EndDate {
			outside, err := repo.HasItineraryOutside(ctx, a.AccountID, id, v.StartDate, v.EndDate)
			if err != nil {
				return err
			}
			if outside {
				scope.Warn(write.WarnItineraryOutsideTripDates)
			}
		}
		if v.Timezone != current.Timezone {
			has, err := repo.HasLocalTimes(ctx, a.AccountID, id)
			if err != nil {
				return err
			}
			if has {
				scope.Warn(write.WarnTimezoneInterpretationChange)
			}
		}
		updated, err := repo.Update(ctx, a.AccountID, id, v, s.clock.Now())
		if err != nil {
			return err
		}
		record(scope, updated, write.ChangeUpsert, submitted, false)
		scope.SetPrimary(ref(updated))
		if v.RouteShortMode != current.RouteShortMode || v.RouteShortDistanceMeters != current.RouteShortDistanceMeters {
			revision, err := repo.InvalidateRouteSummary(ctx, a.AccountID, id, s.clock.Now())
			if err != nil {
				return err
			}
			if err := scope.Enqueue(routeplan.RecalculateJobArgs{AccountID: a.AccountID, TripID: id, Revision: revision}); err != nil {
				return err
			}
		}
		return nil
	}, s.reload(a, id))
}

// lockActive 读取并锁定有效旅行，统一处理 404、410 与版本相等校验。
func lockActive(ctx context.Context, repo Repo, accountID, id uuid.UUID, version int64) (Resource, error) {
	current, found, err := repo.GetForUpdate(ctx, accountID, id)
	if err != nil {
		return Resource{}, err
	}
	if !found {
		return Resource{}, apperr.NotFound()
	}
	if current.DeletedAt != nil {
		return Resource{}, tripDeleted()
	}
	if int64(current.Version) != version {
		return Resource{}, apperr.VersionConflict(&apperr.Conflict{
			EntityType: EntityType, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
		})
	}
	return current, nil
}

// lockTrashed 读取并锁定回收站中的旅行；不在回收站视为 404。
func lockTrashed(ctx context.Context, repo Repo, accountID, id uuid.UUID, version int64) (Resource, error) {
	current, found, err := repo.GetForUpdate(ctx, accountID, id)
	if err != nil {
		return Resource{}, err
	}
	if !found || current.DeletedAt == nil {
		return Resource{}, apperr.NotFound()
	}
	if int64(current.Version) != version {
		return Resource{}, apperr.VersionConflict(&apperr.Conflict{
			EntityType: EntityType, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
		})
	}
	return current, nil
}

// Trash 把整趟旅行放入回收站；要求版本相等。
func (s *Service) Trash(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip.trash",
		Fingerprint: write.Fingerprint("trip.trash", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := lockActive(ctx, repo, a.AccountID, id, version); err != nil {
			return err
		}
		now := s.clock.Now()
		trashed, err := repo.Trash(ctx, a.AccountID, id, now, now.Add(RecycleBinRetention))
		if err != nil {
			return err
		}
		record(scope, trashed, write.ChangeDelete, nil, false)
		scope.SetPrimary(ref(trashed))
		return nil
	}, s.reload(a, id))
}

// SetArchived 归档或取消归档；已处于目标状态时不改变版本。要求版本相等。
func (s *Service) SetArchived(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, version int64, archived bool) (write.Result, error) {
	base := version
	cmd := struct {
		Archived bool `json:"archived"`
	}{archived}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip.archive",
		Fingerprint: write.Fingerprint("trip.archive", id.String(), &base, cmd),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, err := lockActive(ctx, repo, a.AccountID, id, version)
		if err != nil {
			return err
		}
		if (current.ArchivedAt != nil) == archived {
			scope.SetPrimary(ref(current))
			return nil
		}
		now := s.clock.Now()
		var at *time.Time
		if archived {
			at = &now
		}
		updated, err := repo.SetArchived(ctx, a.AccountID, id, at, now)
		if err != nil {
			return err
		}
		record(scope, updated, write.ChangeUpsert, []string{"archived_at"}, false)
		scope.SetPrimary(ref(updated))
		return nil
	}, s.reload(a, id))
}

// Restore 从回收站恢复整趟旅行；须在 purge_after_at 前且未请求永久清理。
func (s *Service) Restore(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip.restore",
		Fingerprint: write.Fingerprint("trip.restore", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		current, err := lockTrashed(ctx, repo, a.AccountID, id, version)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if current.PurgeRequestedAt != nil || current.PurgeAfterAt == nil || !now.Before(*current.PurgeAfterAt) {
			return apperr.Conflicted(codeRestoreUnavailable, "旅行已进入永久清理，不能恢复")
		}
		restored, err := repo.Restore(ctx, a.AccountID, id, now)
		if err != nil {
			return err
		}
		record(scope, restored, write.ChangeUpsert, []string{"deleted_at", "purge_after_at"}, true)
		scope.SetPrimary(ref(restored))
		return nil
	}, s.reload(a, id))
}

// Purge 请求永久清理回收站中的旅行：需要本会话近期密码复验与 confirm=true；
// 标记 purge_requested_at、创建清理任务并与事务一起入队。重复请求返回同一活动任务。
func (s *Service) Purge(ctx context.Context, a actor.Actor, operationID uuid.UUID, id uuid.UUID, version int64, confirm bool) (write.Result, error) {
	if !confirm {
		return write.Result{}, apperr.Validation(apperr.Field("confirm", "INVALID", "必须明确确认永久删除"))
	}
	base := version
	cmd := struct {
		Confirm bool `json:"confirm"`
	}{confirm}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip.purge",
		Fingerprint: write.Fingerprint("trip.purge", id.String(), &base, cmd),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		now := s.clock.Now()
		if !a.RecentlyAuthenticated(now, s.reauthWindow) {
			return apperr.Forbidden(codeReauthRequired, "请先验证密码再永久删除")
		}
		current, err := lockTrashed(ctx, repo, a.AccountID, id, version)
		if err != nil {
			return err
		}
		if current.PurgeRequestedAt != nil {
			job, found, err := repo.ActiveDeletionJob(ctx, a.AccountID, id)
			if err != nil {
				return err
			}
			if found {
				scope.SetPrimary(ref(current))
				scope.AddAffected(write.EntityRef{Type: EntityTypeDeletionJob, ID: job.ID})
				return nil
			}
		}
		target := current
		if current.PurgeRequestedAt == nil {
			target, err = repo.RequestPurge(ctx, a.AccountID, id, now)
			if err != nil {
				return err
			}
			record(scope, target, write.ChangePurge, nil, false)
		}
		job, err := repo.InsertDeletionJob(ctx, DeletionJob{
			ID: uuid.New(), OwnerAccountID: a.AccountID, TargetTripID: id, Status: "queued", Stage: "revoke_access", CreatedAt: now,
		})
		if err != nil {
			return err
		}
		scope.SetPrimary(ref(target))
		scope.AddAffected(write.EntityRef{Type: EntityTypeDeletionJob, ID: job.ID})
		return scope.Enqueue(PurgeJobArgs{JobID: job.ID, AccountID: a.AccountID, TripID: id})
	}, s.reload(a, id))
}
