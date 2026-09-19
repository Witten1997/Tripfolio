package finance

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
)

// LedgerService 实现支出与退款用例（接口设计 2.5、3.5；数据库设计表 9、10、26）：
// 币种由旅行锁定与解锁、退款不超过原支出、原支出分类联动、删除原支出时解除关联退款；
// 付款人与参与人须为本旅行有效成员，分摊份额由服务端计算并随账目保存。
type LedgerService struct {
	uow     write.UnitOfWork[LedgerRepo]
	reader  LedgerReader
	cursors paging.Codec
	clock   clock.Clock
}

// NewLedgerService 创建服务。
func NewLedgerService(uow write.UnitOfWork[LedgerRepo], reader LedgerReader, cursors paging.Codec, clk clock.Clock) *LedgerService {
	return &LedgerService{uow: uow, reader: reader, cursors: cursors, clock: clk}
}

// Get 返回有效账目；不存在或非本人 404，已删除 410。
func (s *LedgerService) Get(ctx context.Context, a actor.Actor, tripID, id uuid.UUID) (LedgerResource, error) {
	if _, err := loadLedgerTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return LedgerResource{}, err
	}
	r, found, err := s.reader.Get(ctx, a.AccountID, tripID, id)
	if err != nil {
		return LedgerResource{}, apperr.Internal(err)
	}
	if !found {
		return LedgerResource{}, apperr.NotFound()
	}
	if r.DeletedAt != nil {
		return LedgerResource{}, ledgerGone()
	}
	return r, nil
}

func ledgerListScope(tripID uuid.UUID, q LedgerListQuery) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ledger|trip=%s", tripID)
	if q.DateFrom != nil {
		fmt.Fprintf(&b, "|from=%s", *q.DateFrom)
	}
	if q.DateTo != nil {
		fmt.Fprintf(&b, "|to=%s", *q.DateTo)
	}
	if q.CategoryID != nil {
		fmt.Fprintf(&b, "|category=%s", *q.CategoryID)
	}
	if q.Kind != nil {
		fmt.Fprintf(&b, "|kind=%s", *q.Kind)
	}
	if q.RefundedEntryID != nil {
		fmt.Fprintf(&b, "|refunded=%s", *q.RefundedEntryID)
	}
	return b.String()
}

// List 返回一页有效账目（接口设计 3.9 LedgerFilters）：按 occurred_on、id 均降序。
func (s *LedgerService) List(ctx context.Context, a actor.Actor, tripID uuid.UUID, f LedgerFilters) (paging.Page[LedgerResource], error) {
	if _, err := loadLedgerTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return paging.Page[LedgerResource]{}, err
	}
	var fields []apperr.FieldError
	q := LedgerListQuery{CategoryID: f.CategoryID, RefundedEntryID: f.RefundedEntryID}
	var ferr *apperr.FieldError
	if f.DateFrom != "" {
		q.DateFrom, ferr = parseLedgerDate("date_from", &f.DateFrom)
		addField(&fields, ferr)
	}
	if f.DateTo != "" {
		q.DateTo, ferr = parseLedgerDate("date_to", &f.DateTo)
		addField(&fields, ferr)
	}
	addField(&fields, validateDateRange(q.DateFrom, q.DateTo))
	if f.Kind != "" {
		kind, ferr := validateKind(f.Kind)
		addField(&fields, ferr)
		q.Kind = &kind
	}
	limit, err := paging.Limit(f.Limit)
	if err != nil {
		e, ok := apperr.As(err)
		if !ok {
			return paging.Page[LedgerResource]{}, err
		}
		fields = append(fields, e.Fields...)
	}
	if len(fields) > 0 {
		return paging.Page[LedgerResource]{}, apperr.Validation(fields...)
	}
	scope := ledgerListScope(tripID, q)
	if f.Cursor != "" {
		var p LedgerPosition
		if err := s.cursors.Decode(a.AccountID, scope, f.Cursor, &p); err != nil {
			return paging.Page[LedgerResource]{}, paging.InvalidCursor()
		}
		q.After = &p
	}
	q.Limit = limit + 1
	rows, err := s.reader.List(ctx, a.AccountID, tripID, q)
	if err != nil {
		return paging.Page[LedgerResource]{}, apperr.Internal(err)
	}
	page := paging.Page[LedgerResource]{Items: []LedgerResource{}}
	if len(rows) > limit {
		last := rows[limit-1]
		token, err := s.cursors.Encode(a.AccountID, scope, LedgerPosition{OccurredOn: last.OccurredOn, ID: last.ID})
		if err != nil {
			return paging.Page[LedgerResource]{}, apperr.Internal(err)
		}
		page.NextCursor = &token
		rows = rows[:limit]
	}
	page.Items = append(page.Items, rows...)
	return page, nil
}

// CreateLedgerCommand 是创建命令（接口设计 3.5 LedgerCreate）；nil 表示缺省。
// PayerMemberID 缺省为「我」，SplitMode 缺省为 even，ParticipantMemberIDs 缺省为全部有效成员；
// 退款关联原支出且三者均缺省时继承原支出。
type CreateLedgerCommand struct {
	ID                   uuid.UUID
	Kind                 string
	Amount               string
	CurrencyCode         *string
	CategoryID           uuid.UUID
	OccurredOn           *string
	Notes                *string
	RefundedEntryID      *uuid.UUID
	AttachmentAssetIDs   []uuid.UUID
	PayerMemberID        *uuid.UUID
	SplitMode            *string
	ParticipantMemberIDs []uuid.UUID
}

// createFingerprint 是创建命令的规范化结构：金额已化简，缺省字段以 null 表示。
type createFingerprint struct {
	Kind                 LedgerKind  `json:"kind"`
	Amount               string      `json:"amount"`
	CurrencyCode         *string     `json:"currency_code"`
	CategoryID           uuid.UUID   `json:"category_id"`
	OccurredOn           *types.Date `json:"occurred_on"`
	Notes                string      `json:"notes"`
	RefundedEntryID      *uuid.UUID  `json:"refunded_entry_id"`
	AttachmentAssetIDs   []uuid.UUID `json:"attachment_asset_ids"`
	PayerMemberID        *uuid.UUID  `json:"payer_member_id"`
	SplitMode            *SplitMode  `json:"split_mode"`
	ParticipantMemberIDs []uuid.UUID `json:"participant_member_ids"`
}

// Create 记录支出或退款；第一条有效账目时锁定旅行币种并把旅行写入 affected。
func (s *LedgerService) Create(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd CreateLedgerCommand) (write.Result, error) {
	var fields []apperr.FieldError
	if cmd.ID == uuid.Nil {
		fields = append(fields, apperr.Field("id", "INVALID", "id 必填"))
	}
	kind, ferr := validateKind(cmd.Kind)
	addField(&fields, ferr)
	compact, ferr := compactMoney(cmd.Amount)
	addField(&fields, ferr)
	addField(&fields, validateLedgerCurrency(cmd.CurrencyCode))
	if cmd.CategoryID == uuid.Nil {
		fields = append(fields, apperr.Field("category_id", "INVALID", "category_id 必填"))
	}
	occurredOn, ferr := parseLedgerDate("occurred_on", cmd.OccurredOn)
	addField(&fields, ferr)
	notes := ""
	if cmd.Notes != nil {
		notes = *cmd.Notes
		addField(&fields, validateLedgerNotes(notes))
	}
	if cmd.RefundedEntryID != nil {
		switch {
		case kind == KindExpense:
			fields = append(fields, apperr.Field("refunded_entry_id", "NOT_ALLOWED", "只有退款可以关联原支出"))
		case *cmd.RefundedEntryID == uuid.Nil:
			fields = append(fields, apperr.Field("refunded_entry_id", "INVALID", "必须是 UUID"))
		case *cmd.RefundedEntryID == cmd.ID:
			fields = append(fields, apperr.Field("refunded_entry_id", "INVALID", "退款不能关联自身"))
		}
	}
	attachments, ferr := validateAttachments(cmd.AttachmentAssetIDs)
	addField(&fields, ferr)
	if cmd.PayerMemberID != nil && *cmd.PayerMemberID == uuid.Nil {
		fields = append(fields, apperr.Field("payer_member_id", "INVALID", "必须是 UUID"))
	}
	mode, ferr := validateSplitMode(cmd.SplitMode)
	addField(&fields, ferr)
	addField(&fields, validateParticipants(cmd.ParticipantMemberIDs))
	if len(fields) > 0 {
		return write.Result{}, apperr.Validation(fields...)
	}
	fp := createFingerprint{
		Kind: kind, Amount: compact, CurrencyCode: cmd.CurrencyCode, CategoryID: cmd.CategoryID,
		OccurredOn: occurredOn, Notes: notes, RefundedEntryID: cmd.RefundedEntryID, AttachmentAssetIDs: attachments,
		PayerMemberID: cmd.PayerMemberID, SplitMode: mode, ParticipantMemberIDs: cmd.ParticipantMemberIDs,
	}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "ledger_entry.create",
		Fingerprint: write.Fingerprint("ledger_entry.create", cmd.ID.String(), nil, fp),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo LedgerRepo) error {
		info, err := loadLedgerTrip(ctx, repo, a.AccountID, tripID)
		if err != nil {
			return err
		}
		exists, err := repo.IDExists(ctx, cmd.ID)
		if err != nil {
			return err
		}
		if exists {
			return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
		}
		amount, err := canonicalLedgerAmount(compact, cmd.CurrencyCode, info.CurrencyCode)
		if err != nil {
			return err
		}
		units, err := ledgerMinorUnits(info.CurrencyCode)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		on := todayIn(now, info.Timezone)
		if occurredOn != nil {
			on = *occurredOn
		}
		if err := s.checkReferences(ctx, repo, a.AccountID, tripID, cmd.CategoryID, attachments); err != nil {
			return err
		}
		payer, participants := cmd.PayerMemberID, cmd.ParticipantMemberIDs
		if cmd.RefundedEntryID != nil {
			original, err := loadRefundOriginal(ctx, repo, a.AccountID, tripID, *cmd.RefundedEntryID)
			if err != nil {
				return err
			}
			if err := s.checkRefundLink(ctx, repo, a.AccountID, tripID, original, cmd.CategoryID, amount, cmd.ID); err != nil {
				return err
			}
			if payer == nil && mode == nil && participants == nil {
				p, m := original.PayerMemberID, original.SplitMode
				payer, mode, participants = &p, &m, splitMemberIDs(original.Splits)
			}
		}
		members, err := repo.ActiveMembers(ctx, a.AccountID, tripID)
		if err != nil {
			return err
		}
		plan, err := resolveSplitPlan(members, payer, mode, participants)
		if err != nil {
			return err
		}
		splits, personal, err := computeSplits(amount, units, plan)
		if err != nil {
			return err
		}
		created, err := repo.Insert(ctx, a.AccountID, LedgerResource{
			ID: cmd.ID, TripID: tripID, Kind: kind, Amount: amount, SplitCount: int32(len(splits)),
			PersonalAmount: personal, PayerMemberID: plan.Payer, SplitMode: plan.Mode, Splits: splits, CurrencyCode: info.CurrencyCode,
			CategoryID: cmd.CategoryID, OccurredOn: on, Notes: notes, RefundedEntryID: cmd.RefundedEntryID,
			AttachmentAssetIDs: attachments, Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		recordLedger(scope, created, write.ChangeUpsert, LedgerFields)
		scope.SetPrimary(ledgerRef(created))
		if info.CurrencyLockedAt == nil {
			locked, err := repo.SetTripCurrencyLock(ctx, a.AccountID, tripID, &now, now)
			if err != nil {
				return err
			}
			recordTripCurrencyLock(scope, locked)
		}
		return nil
	}, s.reload(a, tripID, cmd.ID))
}

// checkReferences 校验分类为本账号有效分类、票据为同旅行有效图片资产；不满足返回 422 INVALID_REFERENCE。
func (s *LedgerService) checkReferences(ctx context.Context, repo LedgerRepo, accountID, tripID, categoryID uuid.UUID, attachments []uuid.UUID) error {
	active, err := repo.CategoryActive(ctx, accountID, categoryID)
	if err != nil {
		return err
	}
	if !active {
		return invalidReference("账单分类不存在或已删除")
	}
	if len(attachments) > 0 {
		missing, err := repo.MissingAssets(ctx, accountID, tripID, attachments)
		if err != nil {
			return err
		}
		if len(missing) > 0 {
			return invalidReference(fmt.Sprintf("票据资产 %s 不存在、已删除或不属于本旅行", missing[0]))
		}
	}
	return nil
}

// loadRefundOriginal 读取退款关联的原支出：须为同旅行有效支出，否则 422 INVALID_REFERENCE。
func loadRefundOriginal(ctx context.Context, repo LedgerRepo, accountID, tripID, originalID uuid.UUID) (LedgerResource, error) {
	original, found, err := repo.Get(ctx, accountID, tripID, originalID)
	if err != nil {
		return LedgerResource{}, err
	}
	if !found || original.DeletedAt != nil || original.Kind != KindExpense {
		return LedgerResource{}, invalidReference("原支出不存在、已删除或不是支出")
	}
	return original, nil
}

// checkRefundLink 校验退款与原支出的关系：分类一致、有效关联退款合计不超过原支出。
// excludeID 是正在写入的退款自身，汇总时跳过。
func (s *LedgerService) checkRefundLink(ctx context.Context, repo LedgerRepo, accountID, tripID uuid.UUID, original LedgerResource, categoryID uuid.UUID, amount string, excludeID uuid.UUID) error {
	if original.CategoryID != categoryID {
		return apperr.Validation(apperr.Field("category_id", "REFUND_CATEGORY_MISMATCH", "退款分类须与原支出一致"))
	}
	linked, err := repo.LinkedRefundsForUpdate(ctx, accountID, tripID, original.ID)
	if err != nil {
		return err
	}
	return checkRefundTotal(original, linked, amount, excludeID)
}

// checkRefundTotal 校验 linked（跳过 excludeID）加上 amount 不超过原支出金额。
func checkRefundTotal(original LedgerResource, linked []LedgerResource, amount string, excludeID uuid.UUID) error {
	total, err := sumAmounts(linked, excludeID)
	if err != nil {
		return err
	}
	add, err := parseAmount(amount)
	if err != nil {
		return fmt.Errorf("金额 %q 无法解析: %w", amount, err)
	}
	limit, err := parseAmount(original.Amount)
	if err != nil {
		return fmt.Errorf("原支出 %s 金额 %q 无法解析: %w", original.ID, original.Amount, err)
	}
	if total.Add(add).Cmp(limit) > 0 {
		return apperr.Unprocessable(codeRefundAmountExceeded, "关联退款合计不能超过原支出金额")
	}
	return nil
}

// LedgerPatch 是局部更新（接口设计 3.5 LedgerPatch）；nil 表示缺省。
// RefundedSet 区分“解除关联”（显式 null）与缺省；AttachmentAssetIDs、ParticipantMemberIDs 出现时整体替换。
type LedgerPatch struct {
	Amount               *string      `json:"amount"`
	CurrencyCode         *string      `json:"currency_code"`
	CategoryID           *uuid.UUID   `json:"category_id"`
	OccurredOn           *string      `json:"occurred_on"`
	Notes                *string      `json:"notes"`
	RefundedSet          bool         `json:"refunded_set"`
	RefundedEntryID      *uuid.UUID   `json:"refunded_entry_id"`
	AttachmentAssetIDs   *[]uuid.UUID `json:"attachment_asset_ids"`
	PayerMemberID        *uuid.UUID   `json:"payer_member_id"`
	SplitMode            *string      `json:"split_mode"`
	ParticipantMemberIDs *[]uuid.UUID `json:"participant_member_ids"`
}

func (p LedgerPatch) submittedFields() []string {
	var f []string
	if p.Amount != nil {
		f = append(f, "amount")
	}
	if p.PayerMemberID != nil {
		f = append(f, "payer_member_id")
	}
	if p.SplitMode != nil {
		f = append(f, "split_mode")
	}
	if p.ParticipantMemberIDs != nil {
		f = append(f, "splits")
	}
	if p.CategoryID != nil {
		f = append(f, "category_id")
	}
	if p.OccurredOn != nil {
		f = append(f, "occurred_on")
	}
	if p.Notes != nil {
		f = append(f, "notes")
	}
	if p.RefundedSet {
		f = append(f, "refunded_entry_id")
	}
	if p.AttachmentAssetIDs != nil {
		f = append(f, "attachment_asset_ids")
	}
	return f
}

// Update 局部更新账目，按字段级合并规则处理基线版本；kind 不可改。
// 修改原支出的金额须仍覆盖其关联退款；修改原支出的分类同事务更新关联退款并使其进入 affected。
// 金额、分摊模式或参与人变化时重算分摊份额；changed_fields 在金额变化时额外含 splits。
func (s *LedgerService) Update(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, baseVersion int64, patch LedgerPatch) (write.Result, error) {
	var fields []apperr.FieldError
	if patch.Amount != nil {
		compact, ferr := compactMoney(*patch.Amount)
		addField(&fields, ferr)
		patch.Amount = &compact
	}
	addField(&fields, validateLedgerCurrency(patch.CurrencyCode))
	if patch.CategoryID != nil && *patch.CategoryID == uuid.Nil {
		fields = append(fields, apperr.Field("category_id", "INVALID", "必须是 UUID"))
	}
	occurredOn, ferr := parseLedgerDate("occurred_on", patch.OccurredOn)
	addField(&fields, ferr)
	if patch.Notes != nil {
		addField(&fields, validateLedgerNotes(*patch.Notes))
	}
	if patch.RefundedSet && patch.RefundedEntryID != nil {
		switch {
		case *patch.RefundedEntryID == uuid.Nil:
			fields = append(fields, apperr.Field("refunded_entry_id", "INVALID", "必须是 UUID"))
		case *patch.RefundedEntryID == id:
			fields = append(fields, apperr.Field("refunded_entry_id", "INVALID", "退款不能关联自身"))
		}
	}
	var attachments []uuid.UUID
	if patch.AttachmentAssetIDs != nil {
		attachments, ferr = validateAttachments(*patch.AttachmentAssetIDs)
		addField(&fields, ferr)
		patch.AttachmentAssetIDs = &attachments
	}
	if patch.PayerMemberID != nil && *patch.PayerMemberID == uuid.Nil {
		fields = append(fields, apperr.Field("payer_member_id", "INVALID", "必须是 UUID"))
	}
	mode, ferr := validateSplitMode(patch.SplitMode)
	addField(&fields, ferr)
	if patch.ParticipantMemberIDs != nil {
		addField(&fields, validateParticipants(*patch.ParticipantMemberIDs))
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
		AccountID: a.AccountID, OperationID: operationID, OperationType: "ledger_entry.update",
		Fingerprint: write.Fingerprint("ledger_entry.update", id.String(), &base, patch),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo LedgerRepo) error {
		info, err := loadLedgerTrip(ctx, repo, a.AccountID, tripID)
		if err != nil {
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
			return ledgerGone()
		}
		decision, err := write.ResolvePatch(ctx, repo.MergeSource(), a.AccountID, EntityTypeLedger, id, baseVersion, int64(current.Version), submitted)
		if err != nil {
			return err
		}
		if !decision.Merge {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityTypeLedger, EntityID: id, ExpectedVersion: baseVersion,
				CurrentVersion: int64(current.Version), ConflictingFields: decision.Conflicting, Current: current,
			})
		}
		if decision.Merged {
			scope.Warn(write.WarnMergedWithNewerVersion)
		}
		if patch.CurrencyCode != nil && *patch.CurrencyCode != info.CurrencyCode {
			return apperr.Unprocessable(codeCurrencyMismatch, "账目币种必须与旅行币种一致")
		}
		v := current.Values()
		if patch.Amount != nil {
			amount, err := canonicalLedgerAmount(*patch.Amount, patch.CurrencyCode, info.CurrencyCode)
			if err != nil {
				return err
			}
			v.Amount = amount
		}
		changed := submitted
		if patch.Amount != nil || patch.PayerMemberID != nil || patch.SplitMode != nil || patch.ParticipantMemberIDs != nil {
			payer, splitMode, participants := current.PayerMemberID, current.SplitMode, splitMemberIDs(current.Splits)
			if patch.PayerMemberID != nil {
				payer = *patch.PayerMemberID
			}
			if mode != nil {
				splitMode = *mode
			}
			if patch.ParticipantMemberIDs != nil {
				participants = *patch.ParticipantMemberIDs
			}
			members, err := repo.ActiveMembers(ctx, a.AccountID, tripID)
			if err != nil {
				return err
			}
			plan, err := resolveSplitPlan(members, &payer, &splitMode, participants)
			if err != nil {
				return err
			}
			units, err := ledgerMinorUnits(info.CurrencyCode)
			if err != nil {
				return err
			}
			splits, personal, err := computeSplits(v.Amount, units, plan)
			if err != nil {
				return err
			}
			v.PayerMemberID, v.SplitMode, v.Splits, v.PersonalAmount, v.SplitCount = plan.Payer, plan.Mode, splits, personal, int32(len(splits))
			if patch.ParticipantMemberIDs == nil && (patch.Amount != nil || patch.SplitMode != nil) {
				changed = append(append([]string(nil), submitted...), "splits")
			}
		}
		if patch.CategoryID != nil {
			v.CategoryID = *patch.CategoryID
		}
		if occurredOn != nil {
			v.OccurredOn = *occurredOn
		}
		if patch.Notes != nil {
			v.Notes = *patch.Notes
		}
		if patch.RefundedSet {
			if current.Kind == KindExpense && patch.RefundedEntryID != nil {
				return apperr.Validation(apperr.Field("refunded_entry_id", "NOT_ALLOWED", "只有退款可以关联原支出"))
			}
			v.RefundedEntryID = patch.RefundedEntryID
		}
		if patch.AttachmentAssetIDs != nil {
			v.AttachmentAssetIDs = attachments
		}
		if patch.CategoryID != nil || patch.AttachmentAssetIDs != nil {
			checkAttachments := attachments
			if patch.AttachmentAssetIDs == nil {
				checkAttachments = nil
			}
			if err := s.checkReferences(ctx, repo, a.AccountID, tripID, v.CategoryID, checkAttachments); err != nil {
				return err
			}
		}
		now := s.clock.Now()
		switch current.Kind {
		case KindExpense:
			linked, err := repo.LinkedRefundsForUpdate(ctx, a.AccountID, tripID, id)
			if err != nil {
				return err
			}
			if v.Amount != current.Amount {
				if err := checkRefundTotal(LedgerResource{ID: id, Amount: v.Amount}, linked, "0", uuid.Nil); err != nil {
					return err
				}
			}
			if v.CategoryID != current.CategoryID {
				for _, refund := range linked {
					rv := refund.Values()
					rv.CategoryID = v.CategoryID
					updated, err := repo.Update(ctx, a.AccountID, tripID, refund.ID, rv, now)
					if err != nil {
						return err
					}
					recordLedger(scope, updated, write.ChangeUpsert, []string{"category_id"})
				}
			}
		case KindRefund:
			if v.RefundedEntryID != nil && (!sameUUID(v.RefundedEntryID, current.RefundedEntryID) || v.Amount != current.Amount || v.CategoryID != current.CategoryID) {
				original, err := loadRefundOriginal(ctx, repo, a.AccountID, tripID, *v.RefundedEntryID)
				if err != nil {
					return err
				}
				if err := s.checkRefundLink(ctx, repo, a.AccountID, tripID, original, v.CategoryID, v.Amount, id); err != nil {
					return err
				}
			}
		}
		updated, err := repo.Update(ctx, a.AccountID, tripID, id, v, now)
		if err != nil {
			return err
		}
		recordLedger(scope, updated, write.ChangeUpsert, changed)
		scope.SetPrimary(ledgerRef(updated))
		return nil
	}, s.reload(a, tripID, id))
}

// Delete 软删除账目；要求版本相等。删除原支出时同事务把其有效关联退款的 refunded_entry_id 置空，
// 这些退款保留为独立退款、版本递增并进入 affected，warnings 含 REFUNDS_UNLINKED；最后一条账目删除后解锁旅行币种。
func (s *LedgerService) Delete(ctx context.Context, a actor.Actor, operationID, tripID, id uuid.UUID, version int64) (write.Result, error) {
	base := version
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "ledger_entry.delete",
		Fingerprint: write.Fingerprint("ledger_entry.delete", id.String(), &base, struct{}{}),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo LedgerRepo) error {
		if _, err := loadLedgerTrip(ctx, repo, a.AccountID, tripID); err != nil {
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
			return ledgerGone()
		}
		if int64(current.Version) != version {
			return apperr.VersionConflict(&apperr.Conflict{
				EntityType: EntityTypeLedger, EntityID: id, ExpectedVersion: version, CurrentVersion: int64(current.Version), Current: current,
			})
		}
		now := s.clock.Now()
		if current.Kind == KindExpense {
			linked, err := repo.LinkedRefundsForUpdate(ctx, a.AccountID, tripID, id)
			if err != nil {
				return err
			}
			for _, refund := range linked {
				rv := refund.Values()
				rv.RefundedEntryID = nil
				updated, err := repo.Update(ctx, a.AccountID, tripID, refund.ID, rv, now)
				if err != nil {
					return err
				}
				recordLedger(scope, updated, write.ChangeUpsert, []string{"refunded_entry_id"})
				scope.Warn(write.WarnRefundsUnlinked)
			}
		}
		deleted, err := repo.SoftDelete(ctx, a.AccountID, tripID, id, now)
		if err != nil {
			return err
		}
		recordLedger(scope, deleted, write.ChangeDelete, nil)
		scope.SetPrimary(ledgerRef(deleted))
		remaining, err := repo.CountActive(ctx, a.AccountID, tripID)
		if err != nil {
			return err
		}
		if remaining == 0 {
			unlocked, err := repo.SetTripCurrencyLock(ctx, a.AccountID, tripID, nil, now)
			if err != nil {
				return err
			}
			recordTripCurrencyLock(scope, unlocked)
		}
		return nil
	}, s.reload(a, tripID, id))
}

// sameUUID 比较两个可空 ID。
func sameUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s *LedgerService) reload(a actor.Actor, tripID, id uuid.UUID) func(ctx context.Context, repo LedgerRepo) (any, error) {
	return func(ctx context.Context, repo LedgerRepo) (any, error) {
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
