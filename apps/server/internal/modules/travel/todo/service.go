package todo

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// Service 实现待办用例（接口设计 2.4、3.4）。
type Service struct {
	uow     write.UnitOfWork[Repo]
	reader  Reader
	cursors paging.Codec
	clock   clock.Clock
}

// NewService 创建服务。
func NewService(uow write.UnitOfWork[Repo], reader Reader, cursors paging.Codec, clk clock.Clock) *Service {
	return &Service{uow: uow, reader: reader, cursors: cursors, clock: clk}
}

const (
	maxTitleChars = 200
	maxNotesChars = 4000
)

// 错误代码（接口设计 1.4）。
const (
	codeTripDeleted   = "TRIP_DELETED"
	codeResourceGone  = "RESOURCE_GONE"
	codeIDAlreadyUsed = "ID_ALREADY_USED"
)

func tripDeleted() *apperr.Error  { return apperr.Gone(codeTripDeleted, "旅行已在回收站中") }
func resourceGone() *apperr.Error { return apperr.Gone(codeResourceGone, "待办已删除") }

type tripReader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
}

// loadTrip 读取并校验所属旅行：不存在或非本人 404，回收站中 410 TRIP_DELETED。
func loadTrip(ctx context.Context, r tripReader, accountID, tripID uuid.UUID) (TripInfo, error) {
	info, found, err := r.Trip(ctx, accountID, tripID)
	if err != nil {
		return TripInfo{}, apperr.Internal(err)
	}
	if !found {
		return TripInfo{}, apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return TripInfo{}, tripDeleted()
	}
	return info, nil
}

// todayIn 取旅行时区的今天；时区无法加载时退回 UTC，避免历史数据让列表不可用。
func todayIn(now time.Time, tz string) types.Date {
	today, err := types.TodayIn(now, tz)
	if err != nil {
		return types.DateOf(now.UTC())
	}
	return today
}

func addField(fields *[]apperr.FieldError, e *apperr.FieldError) {
	if e != nil {
		*fields = append(*fields, *e)
	}
}

func validateTitle(title string) (string, *apperr.FieldError) {
	title = strings.TrimSpace(title)
	if n := utf8.RuneCountInString(title); n < 1 || n > maxTitleChars {
		e := apperr.Field("title", "INVALID", fmt.Sprintf("标题须为 1–%d 个字符", maxTitleChars))
		return "", &e
	}
	return title, nil
}

func validateNotes(value string) *apperr.FieldError {
	if utf8.RuneCountInString(value) > maxNotesChars {
		e := apperr.Field("notes", "TOO_LONG", fmt.Sprintf("最多 %d 个字符", maxNotesChars))
		return &e
	}
	if !utf8.ValidString(value) {
		e := apperr.Field("notes", "INVALID", "必须是有效的 UTF-8 文本")
		return &e
	}
	return nil
}

// parseDueOn 解析可空截止日期；raw 为 nil 表示缺省或清空。
func parseDueOn(raw *string, fields *[]apperr.FieldError) *types.Date {
	if raw == nil {
		return nil
	}
	d, err := types.ParseDate(*raw)
	if err != nil {
		*fields = append(*fields, apperr.Field("due_on", "INVALID", "日期格式必须是 YYYY-MM-DD"))
		return nil
	}
	return &d
}

func ref(r Resource) write.EntityRef { return write.Ref(EntityType, r.ID, int64(r.Version)) }

func record(scope write.Scope, r Resource, kind write.ChangeKind, fields []string) {
	tripID := r.TripID
	change := write.Change{EntityType: EntityType, EntityID: r.ID, TripID: &tripID, Version: int64(r.Version), Kind: kind}
	if kind == write.ChangeUpsert {
		change.Snapshot = r
		change.ChangedFields = fields
	}
	scope.Record(change)
}

// Get 返回有效待办；不存在或非本人 404，已删除 410。
func (s *Service) Get(ctx context.Context, a actor.Actor, tripID, id uuid.UUID) (Resource, error) {
	if _, err := loadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return Resource{}, err
	}
	r, found, err := s.reader.Get(ctx, a.AccountID, tripID, id)
	if err != nil {
		return Resource{}, apperr.Internal(err)
	}
	if !found {
		return Resource{}, apperr.NotFound()
	}
	if r.DeletedAt != nil {
		return Resource{}, resourceGone()
	}
	return r, nil
}

func listScope(tripID uuid.UUID, state State) string {
	return fmt.Sprintf("todos|trip=%s|state=%s", tripID, state)
}

// List 返回一页有效待办及其逾期标识（接口设计 3.9 TodoFilters）。
func (s *Service) List(ctx context.Context, a actor.Actor, tripID uuid.UUID, f Filters) (paging.Page[ListItem], error) {
	info, err := loadTrip(ctx, s.reader, a.AccountID, tripID)
	if err != nil {
		return paging.Page[ListItem]{}, err
	}
	var fields []apperr.FieldError
	state := StateAll
	if f.State != "" {
		state = State(f.State)
		if !state.Valid() {
			fields = append(fields, apperr.Field("state", "INVALID", "状态须为 all、pending、completed 或 overdue"))
		}
	}
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		e, ok := apperr.As(err)
		if !ok {
			return paging.Page[ListItem]{}, err
		}
		fields = append(fields, e.Fields...)
	}
	if len(fields) > 0 {
		return paging.Page[ListItem]{}, apperr.Validation(fields...)
	}
	scope := listScope(tripID, state)
	var after *Position
	if f.Cursor != "" {
		var p Position
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &p); err != nil {
			return paging.Page[ListItem]{}, paging.InvalidCursor()
		}
		after = &p
	}
	today := todayIn(s.clock.Now(), info.Timezone)
	rows, err := s.reader.List(ctx, a.AccountID, tripID, ListQuery{State: state, Today: today, Limit: limit + 1, After: after})
	if err != nil {
		return paging.Page[ListItem]{}, apperr.Internal(err)
	}
	page := paging.Page[ListItem]{Items: []ListItem{}}
	if len(rows) > limit {
		last := rows[limit-1]
		token, err := s.cursors.Encode(a.AccountID, scope, Position{DueKey: DueKey(last), ID: last.ID})
		if err != nil {
			return paging.Page[ListItem]{}, apperr.Internal(err)
		}
		page.NextCursor = &token
		rows = rows[:limit]
	}
	for _, r := range rows {
		page.Items = append(page.Items, ListItem{Resource: r, IsOverdue: Overdue(r, today)})
	}
	return page, nil
}

// CreateCommand 是创建命令（接口设计 3.4 TodoCreate）；nil 表示缺省。
type CreateCommand struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	DueOn     *string   `json:"due_on"`
	Notes     *string   `json:"notes"`
	Completed *bool     `json:"completed"`
}

// Create 新建待办；completed=true 时服务端同时写入 completed_at。
func (s *Service) Create(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd CreateCommand) (write.Result, error) {
	var fields []apperr.FieldError
	if cmd.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "INVALID", "id 必填"))
	}
	title, ferr := validateTitle(cmd.Title)
	addField(&fields, ferr)
	cmd.Title = title
	dueOn := parseDueOn(cmd.DueOn, &fields)
	notes := ""
	if cmd.Notes != nil {
		notes = *cmd.Notes
		addField(&fields, validateNotes(notes))
	}
	completed := cmd.Completed != nil && *cmd.Completed
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	fp := struct {
		Title     string      `json:"title"`
		DueOn     *types.Date `json:"due_on"`
		Notes     string      `json:"notes"`
		Completed bool        `json:"completed"`
	}{cmd.Title, dueOn, notes, completed}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "todo.create",
		Fingerprint: write.Fingerprint("todo.create", cmd.ID.String(), nil, fp),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		exists, err := repo.IDExists(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
		}
		now := s.clock.Now()
		var completedAt *time.Time
		if completed {
			t := now
			completedAt = &t
		}
		created, err := repo.Insert(ctx, a.AccountID, Resource{
			ID: cmd.ID, TripID: tripID, Title: cmd.Title, DueOn: dueOn, Notes: notes,
			Completed: completed, CompletedAt: completedAt, Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		record(scope, created, write.ChangeUpsert, Fields)
		scope.SetPrimary(ref(created))
		return nil
	}, s.reload(a, tripID, cmd.ID))
}

// Patch 是局部更新（接口设计 3.4 TodoPatch）；DueOnSet 区分“清空截止日期”与缺省。
type Patch struct {
	Title     *string `json:"title"`
	DueOnSet  bool    `json:"due_on_set"`
	DueOn     *string `json:"due_on"`
	Notes     *string `json:"notes"`
	Completed *bool   `json:"completed"`
}

func (p Patch) submittedFields() []string {
	var f []string
	if p.Title != nil {
		f = append(f, "title")
	}
	if p.DueOnSet {
		f = append(f, "due_on")
	}
	if p.Notes != nil {
		f = append(f, "notes")
	}
	if p.Completed != nil {
		f = append(f, "completed")
	}
	return f
}

// Update 局部更新待办，按字段级合并规则处理基线版本（接口设计 1.2、3.4）。
func (s *Service) Update(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, baseVersion int64, patch Patch) (write.Result, error) {
	var fields []apperr.FieldError
	if patch.Title != nil {
		title, ferr := validateTitle(*patch.Title)
		addField(&fields, ferr)
		patch.Title = &title
	}
	var dueOn *types.Date
	if patch.DueOnSet {
		dueOn = parseDueOn(patch.DueOn, &fields)
	}
	if patch.Notes != nil {
		addField(&fields, validateNotes(*patch.Notes))
	}
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	submitted := patch.submittedFields()
	if len(submitted) == 0 {
		return write.Result{}, apperr.Validation(apperr.Field("", "EMPTY_PATCH", "没有可更新的字段"))
	}
	base := baseVersion
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "todo.update",
		Fingerprint: write.Fingerprint("todo.update", id.String(), &base, patch),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, tripID, id)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return resourceGone()
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
		now := s.clock.Now()
		v := current.Values()
		if patch.Title != nil {
			v.Title = *patch.Title
		}
		if patch.DueOnSet {
			v.DueOn = dueOn
		}
		if patch.Notes != nil {
			v.Notes = *patch.Notes
		}
		if patch.Completed != nil && *patch.Completed != v.Completed {
			v.Completed = *patch.Completed
			if v.Completed {
				t := now
				v.CompletedAt = &t
			} else {
				v.CompletedAt = nil
			}
		}
		updated, err := repo.Update(ctx, a.AccountID, tripID, id, v, now)
		if err != nil {
			return err
		}
		record(scope, updated, write.ChangeUpsert, submitted)
		scope.SetPrimary(ref(updated))
		return nil
	}, s.reload(a, tripID, id))
}

// Delete 软删除待办；要求版本相等。
func (s *Service) Delete(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "todo.delete",
		Fingerprint: write.Fingerprint("todo.delete", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if _, err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		current, found, err := repo.GetForUpdate(ctx, a.AccountID, tripID, id)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return resourceGone()
		}
		if int64(current.Version) != version {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityType, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
			})
		}
		deleted, err := repo.SoftDelete(ctx, a.AccountID, tripID, id, s.clock.Now())
		if err != nil {
			return err
		}
		record(scope, deleted, write.ChangeDelete, nil)
		scope.SetPrimary(ref(deleted))
		return nil
	}, s.reload(a, tripID, id))
}

func (s *Service) reload(a actor.Actor, tripID, id uuid.UUID) func(ctx context.Context, repo Repo) (any, error) {
	return func(ctx context.Context, repo Repo) (any, error) {
		r, found, err := repo.Get(ctx, a.AccountID, tripID, id)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, apperr.NotFound()
		}
		return r, nil
	}
}
