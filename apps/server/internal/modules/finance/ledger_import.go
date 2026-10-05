package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/member"
)

const MaxLedgerImportRows = 1000
const MaxLedgerImportBytes = 5 * 1024 * 1024

type LedgerImportError struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Message string `json:"message"`
}

type LedgerImportShare struct {
	Member string `json:"member"`
	Amount string `json:"amount"`
}

type LedgerImportRow struct {
	Row          int                 `json:"row"`
	Amount       string              `json:"amount"`
	Category     string              `json:"category"`
	SplitMode    string              `json:"split_mode"`
	Participants []string            `json:"participants"`
	OccurredOn   string              `json:"occurred_on"`
	Notes        string              `json:"notes"`
	Payer        string              `json:"payer"`
	Splits       []LedgerImportShare `json:"splits"`
}

type LedgerImportPreview struct {
	ScopeRevisions []write.ScopeRevision `json:"scope_revisions,omitempty"`
	Rows           []LedgerImportRow     `json:"rows"`
	Errors         []LedgerImportError   `json:"errors"`
	Warnings       []string              `json:"warnings"`
	Count          int                   `json:"count"`
	ValidCount     int                   `json:"valid_count"`
	TotalAmount    string                `json:"total_amount"`
	CurrencyCode   string                `json:"currency_code"`
	Digest         string                `json:"digest"`
}

type importContextReader interface {
	Trip(context.Context, uuid.UUID, uuid.UUID) (LedgerTripInfo, bool, error)
	ImportCategories(context.Context, uuid.UUID) ([]CategoryResource, error)
	ActiveMembers(context.Context, uuid.UUID, uuid.UUID) ([]member.Resource, error)
}

type importContext struct {
	Trip       LedgerTripInfo
	Categories []CategoryResource
	Members    []member.Resource
}

func loadImportContext(ctx context.Context, reader importContextReader, accountID, tripID uuid.UUID) (importContext, error) {
	info, err := loadLedgerTrip(ctx, reader, accountID, tripID)
	if err != nil {
		return importContext{}, err
	}
	categories, err := reader.ImportCategories(ctx, accountID)
	if err != nil {
		return importContext{}, apperr.Internal(err)
	}
	members, err := reader.ActiveMembers(ctx, accountID, tripID)
	if err != nil {
		return importContext{}, apperr.Internal(err)
	}
	return importContext{Trip: info, Categories: categories, Members: members}, nil
}

func importContextDigest(c importContext) string {
	// 币种锁、预算和旅行版本不影响预览；成员顺序影响分摊余数，必须纳入。
	var values []string
	values = append(values, c.Trip.CurrencyCode)
	for _, category := range c.Categories {
		values = append(values, category.ID.String(), category.Name)
	}
	for _, m := range c.Members {
		values = append(values, m.ID.String(), m.Name, m.SharePercent, strconv.FormatBool(m.IsSelf))
	}
	h := write.Fingerprint("ledger_import.context", "", nil, values)
	return hex.EncodeToString(h[:])
}

func (s *LedgerService) ImportTemplate(ctx context.Context, a actor.Actor, tripID uuid.UUID) ([]byte, error) {
	c, err := loadImportContext(ctx, s.reader, a.AccountID, tripID)
	if err != nil {
		return nil, err
	}
	if len(c.Categories) == 0 {
		return nil, apperr.Unprocessable("IMPORT_NO_CATEGORIES", "请先创建账单分类")
	}
	return buildLedgerTemplate(a.AccountID, tripID, c)
}

func (s *LedgerService) PreviewImport(ctx context.Context, a actor.Actor, tripID uuid.UUID, file []byte) (LedgerImportPreview, error) {
	reader, ok := s.reader.(ImportSnapshotReader)
	if !ok {
		return LedgerImportPreview{}, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "导入基线读取暂不可用")
	}
	snapshot, err := reader.ReadImportSnapshot(ctx, a.AccountID, tripID)
	if err != nil {
		return LedgerImportPreview{}, err
	}
	c := importContext{Trip: snapshot.Trip, Categories: snapshot.Categories, Members: snapshot.Members}
	doc, err := parseLedgerWorkbook(file, a.AccountID, tripID)
	if err != nil {
		return LedgerImportPreview{}, err
	}
	preview, _, err := validateImport(doc, c)
	preview.ScopeRevisions = snapshot.ScopeRevisions
	return preview, err
}

func (s *LedgerService) Import(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, file []byte, digest string) (write.Result, error) {
	if operationID == uuid.Nil || len(digest) != 64 {
		return write.Result{}, apperr.BadRequest("IMPORT_PREVIEW_REQUIRED", "请先预览账单，再确认导入")
	}
	doc, err := parseLedgerWorkbook(file, a.AccountID, tripID)
	if err != nil {
		return write.Result{}, err
	}
	req := write.Request{AccountID: a.AccountID, OperationID: operationID, OperationType: "ledger_entry.import",
		Fingerprint: write.Fingerprint("ledger_entry.import", tripID.String(), nil, []string{doc.FileHash, digest})}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo LedgerRepo) error {
		c, err := loadImportContext(ctx, repo, a.AccountID, tripID)
		if err != nil {
			return err
		}
		if err := write.RequireCollections(ctx, scope, []collectionguard.Scope{{Kind: "members", ScopeID: tripID.String()}}); err != nil {
			return err
		}
		preview, entries, err := validateImport(doc, c)
		if err != nil {
			return err
		}
		if preview.Digest != digest {
			return apperr.Conflicted("IMPORT_PREVIEW_CHANGED", "分类、成员、分摊比例或文件已变化，请重新预览并确认")
		}
		if len(preview.Errors) > 0 {
			fields := make([]apperr.FieldError, 0, len(preview.Errors))
			for _, e := range preview.Errors {
				fields = append(fields, apperr.Field(fmt.Sprintf("第%d行.%s", e.Row, e.Column), "INVALID", e.Message))
			}
			return apperr.Validation(fields...)
		}
		now := s.clock.Now()
		for i := range entries {
			entries[i].ID = uuid.NewSHA1(operationID, []byte(fmt.Sprintf("%s:%d", tripID, preview.Rows[i].Row)))
			entries[i].TripID = tripID
			entries[i].Version = 1
			entries[i].CreatedAt, entries[i].UpdatedAt = now, now
		}
		if err := repo.InsertImported(ctx, a.AccountID, tripID, entries); err != nil {
			return err
		}
		for _, entry := range entries {
			recordLedger(scope, entry, write.ChangeUpsert, LedgerFields)
		}
		scope.SetPrimary(ledgerRef(entries[0]))
		if c.Trip.CurrencyLockedAt == nil {
			locked, err := repo.SetTripCurrencyLock(ctx, a.AccountID, tripID, &now, now)
			if err != nil {
				return err
			}
			recordTripCurrencyLock(scope, locked)
		}
		return nil
	}, nil)
}

func validateImport(doc ledgerWorkbook, c importContext) (LedgerImportPreview, []LedgerResource, error) {
	p := LedgerImportPreview{Rows: []LedgerImportRow{}, Errors: append([]LedgerImportError{}, doc.Errors...), Warnings: []string{}, CurrencyCode: c.Trip.CurrencyCode}
	contextHash := importContextDigest(c)
	h := sha256.Sum256([]byte(doc.FileHash + ":" + contextHash))
	p.Digest = hex.EncodeToString(h[:])
	if doc.Currency != c.Trip.CurrencyCode {
		return p, nil, apperr.Unprocessable("CURRENCY_MISMATCH", "模板币种与行程当前币种不同，请重新下载模板")
	}
	if doc.ContextHash != contextHash {
		p.Warnings = append(p.Warnings, "分类或成员设置已变化；本次预览已使用当前名称及分摊比例，请核对后确认。")
	}
	units, err := ledgerMinorUnits(c.Trip.CurrencyCode)
	if err != nil {
		return p, nil, err
	}
	categories := map[uuid.UUID]CategoryResource{}
	members := map[uuid.UUID]member.Resource{}
	var self *member.Resource
	for _, cat := range c.Categories {
		categories[cat.ID] = cat
	}
	for i, m := range c.Members {
		members[m.ID] = m
		if m.IsSelf {
			self = &c.Members[i]
		}
	}
	total := money.Zero()
	entries := []LedgerResource{}
	seen := map[[32]byte]int{}
	for _, raw := range doc.Rows {
		row := LedgerImportRow{Row: raw.Number, Amount: raw.Cells[0], Category: raw.Cells[1], SplitMode: raw.Cells[2], OccurredOn: raw.Cells[3], Notes: raw.Cells[4], Payer: raw.Cells[6], Participants: []string{}, Splits: []LedgerImportShare{}}
		startErrors := len(p.Errors)
		hasParseError := false
		add := func(column, message string) {
			p.Errors = append(p.Errors, LedgerImportError{Row: raw.Number, Column: column, Message: message})
		}
		for _, e := range doc.Errors {
			if e.Row == raw.Number {
				hasParseError = true
				break
			}
		}
		compact, ferr := compactMoney(row.Amount)
		if ferr != nil {
			add("金额", ferr.Message)
		} else {
			amount, e := canonicalLedgerAmount(compact, nil, c.Trip.CurrencyCode)
			if e != nil {
				add("金额", "金额小数位或范围不符合行程币种要求")
			} else {
				row.Amount = amount
			}
		}
		categoryID, ok := doc.Categories[strings.ToLower(row.Category)]
		category, active := categories[categoryID]
		if !ok || !active {
			add("账单分类", "请选择模板中的有效分类；分类不存在或已删除时请重新下载模板")
		} else {
			row.Category = category.Name
		}
		mode := SplitPersonal
		switch row.SplitMode {
		case "", "个人":
			row.SplitMode = string(mode)
		case "均摊":
			mode = SplitEven
			row.SplitMode = string(mode)
		case "按比例":
			mode = SplitRatio
			row.SplitMode = string(mode)
		default:
			add("分摊模式", "只能填写个人、均摊或按比例")
		}
		on, e := types.ParseDate(row.OccurredOn)
		if e != nil {
			add("实际日期", "请填写有效日期，格式为 YYYY-MM-DD")
		}
		if ferr := validateLedgerNotes(row.Notes); ferr != nil {
			add("备注", ferr.Message)
		}
		var payer *uuid.UUID
		resolveMember := func(name string) (uuid.UUID, bool) {
			id, ok := doc.Members[strings.ToLower(strings.TrimSpace(name))]
			_, active := members[id]
			return id, ok && active
		}
		if row.Payer != "" {
			if id, ok := resolveMember(row.Payer); ok {
				payer = &id
			} else {
				add("付款人", "付款人不存在或已删除，请使用模板中的成员名称或编号")
			}
		}
		if self == nil {
			add("付款人", "行程缺少成员「我」")
		}
		participantIDs := []uuid.UUID{}
		if mode == SplitPersonal {
			if raw.Cells[5] != "" {
				add("分摊成员", "个人账单请留空分摊成员")
			}
			if payer != nil && self != nil && *payer != self.ID {
				add("付款人", "个人账单的付款人必须为「我」")
			}
		} else if raw.Cells[5] == "全部成员" {
			// 固定为模板中的全员，避免下载后新增成员被无意计入。
			for _, id := range doc.MemberOrder {
				if _, ok := members[id]; !ok {
					add("分摊成员", "模板中有成员已删除，请重新下载模板")
					break
				}
				participantIDs = append(participantIDs, id)
			}
		} else {
			parts := strings.Split(strings.ReplaceAll(raw.Cells[5], "；", ";"), ";")
			// 含分号的单个姓名仍可匹配，多人时可使用参考页中的成员编号。
			if _, ok := resolveMember(raw.Cells[5]); ok {
				parts = []string{raw.Cells[5]}
			}
			for _, name := range parts {
				id, ok := resolveMember(name)
				if !ok {
					add("分摊成员", "请填写有效成员，以分号分隔，或填写「全部成员」")
					break
				}
				participantIDs = append(participantIDs, id)
			}
		}
		if mode != SplitPersonal {
			if ferr := validateParticipants(participantIDs); ferr != nil {
				add("分摊成员", ferr.Message)
			}
		}
		if !hasParseError && len(p.Errors) == startErrors {
			plan, e := resolveSplitPlan(c.Members, payer, &mode, participantIDs)
			if e != nil {
				add("分摊成员", "所选成员的比例合计必须大于 0，且成员均须有效")
			} else {
				splits, personal, e := computeSplits(row.Amount, units, plan)
				if e != nil {
					return p, nil, apperr.Internal(e)
				}
				row.Payer = members[plan.Payer].Name
				for _, split := range splits {
					row.Participants = append(row.Participants, members[split.MemberID].Name)
					row.Splits = append(row.Splits, LedgerImportShare{Member: members[split.MemberID].Name, Amount: split.Amount})
				}
				entry := LedgerResource{Kind: KindExpense, Amount: row.Amount, CategoryID: categoryID, OccurredOn: on, Notes: row.Notes,
					CurrencyCode: c.Trip.CurrencyCode, PayerMemberID: plan.Payer, SplitMode: mode, Splits: splits, SplitCount: int32(len(splits)), PersonalAmount: personal, AttachmentAssetIDs: []uuid.UUID{}}
				key := write.Fingerprint("ledger_import.row", "", nil, entry)
				if previous, ok := seen[key]; ok {
					p.Warnings = append(p.Warnings, fmt.Sprintf("第 %d 行与第 %d 行内容相同，确认导入后两笔都会保留。", row.Row, previous))
				} else {
					seen[key] = row.Row
				}
				entries = append(entries, entry)
				amount, _ := money.ParseDecimal(row.Amount)
				total = total.Add(amount)
			}
		}
		p.Rows = append(p.Rows, row)
	}
	p.Count, p.ValidCount, p.TotalAmount = len(p.Rows), len(entries), total.Format(units)
	if p.Count == 0 {
		p.Errors = append(p.Errors, LedgerImportError{Row: 5, Column: "金额", Message: "没有可导入的账单，请从第 5 行开始填写"})
	}
	return p, entries, nil
}
