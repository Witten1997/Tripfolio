package member

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/foundation/write"
)

// Service 实现成员用例：列表与整体保存。
type Service struct {
	uow    write.UnitOfWork[Repo]
	reader Reader
	clock  clock.Clock
}

// NewService 创建服务。
func NewService(uow write.UnitOfWork[Repo], reader Reader, clk clock.Clock) *Service {
	return &Service{uow: uow, reader: reader, clock: clk}
}

// 错误代码（接口设计 1.4）。
const (
	codeTripDeleted     = "TRIP_DELETED"
	codeIDAlreadyUsed   = "ID_ALREADY_USED"
	codeMemberInUse     = "MEMBER_IN_USE"
	codeSharePercentSum = "SHARE_PERCENT_SUM"
)

func tripDeleted() *apperr.Error { return apperr.Gone(codeTripDeleted, "旅行已在回收站中") }

type tripReader interface {
	Trip(ctx context.Context, accountID, tripID uuid.UUID) (TripInfo, bool, error)
}

func loadTrip(ctx context.Context, r tripReader, accountID, tripID uuid.UUID) error {
	info, found, err := r.Trip(ctx, accountID, tripID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found {
		return apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return tripDeleted()
	}
	return nil
}

func record(scope write.Scope, r Resource, kind write.ChangeKind, fields []string) {
	tripID := r.TripID
	change := write.Change{EntityType: EntityType, EntityID: r.ID, TripID: &tripID, Version: int64(r.Version), Kind: kind}
	if kind == write.ChangeUpsert {
		change.Snapshot = r
		change.ChangedFields = fields
	}
	scope.Record(change)
}

// List 返回本旅行全部有效成员。
func (s *Service) List(ctx context.Context, a actor.Actor, tripID uuid.UUID) ([]Resource, error) {
	if err := loadTrip(ctx, s.reader, a.AccountID, tripID); err != nil {
		return nil, err
	}
	rows, err := s.reader.List(ctx, a.AccountID, tripID)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if rows == nil {
		rows = []Resource{}
	}
	return rows, nil
}

// Input 是整体保存中的一位成员（接口设计 3.5 TripMembersSave）。
type Input struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	SharePercent string    `json:"share_percent"`
}

// SaveCommand 是整体保存命令：数组顺序即 sort_order。
type SaveCommand struct {
	Members []Input `json:"members"`
}

type validated struct {
	id      uuid.UUID
	name    string
	percent string
}

func validate(cmd SaveCommand) ([]validated, error) {
	var fields []apperr.FieldError
	if n := len(cmd.Members); n < 1 || n > MaxMembers {
		fields = append(fields, apperr.Field("members", "INVALID", fmt.Sprintf("成员须为 1–%d 人", MaxMembers)))
		return nil, apperr.Validation(fields...)
	}
	out := make([]validated, 0, len(cmd.Members))
	ids := map[uuid.UUID]struct{}{}
	names := map[string]struct{}{}
	var percents []money.Decimal
	for i, in := range cmd.Members {
		path := fmt.Sprintf("members[%d]", i)
		if in.ID == uuid.Nil {
			fields = append(fields, apperr.Field(path+".id", "INVALID", "id 必填"))
		} else if _, dup := ids[in.ID]; dup {
			fields = append(fields, apperr.Field(path+".id", "DUPLICATE", "成员 id 重复"))
		}
		ids[in.ID] = struct{}{}
		name := strings.TrimSpace(in.Name)
		if n := utf8.RuneCountInString(name); n < 1 || n > MaxNameChars || !utf8.ValidString(name) {
			fields = append(fields, apperr.Field(path+".name", "INVALID", fmt.Sprintf("名称须为 1–%d 个字符", MaxNameChars)))
		} else if _, dup := names[strings.ToLower(name)]; dup {
			fields = append(fields, apperr.Field(path+".name", "DUPLICATE", "成员名称重复"))
		}
		names[strings.ToLower(name)] = struct{}{}
		percent, d, ok := ParsePercent(strings.TrimSpace(in.SharePercent))
		if !ok {
			fields = append(fields, apperr.Field(path+".share_percent", "INVALID", "百分比须为 0–100，最多 2 位小数"))
		}
		percents = append(percents, d)
		out = append(out, validated{id: in.ID, name: name, percent: percent})
	}
	if len(fields) > 0 {
		return nil, apperr.Validation(fields...)
	}
	if total, ok := PercentSum(percents); !ok {
		return nil, apperr.Unprocessable(codeSharePercentSum, fmt.Sprintf("成员百分比之和须为 100，当前为 %s", total.Format(0)))
	}
	return out, nil
}

// Save 整体保存成员：现有 id 更新、新 id 创建、未出现的有效成员删除；「我」不可删除，被引用的成员不可删除。
func (s *Service) Save(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd SaveCommand) (write.Result, error) {
	inputs, err := validate(cmd)
	if err != nil {
		return write.Result{}, err
	}
	fp := make([]Input, 0, len(inputs))
	for _, in := range inputs {
		fp = append(fp, Input{ID: in.id, Name: in.name, SharePercent: in.percent})
	}
	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "trip_member.save",
		Fingerprint: write.Fingerprint("trip_member.save", tripID.String(), nil, fp),
	}
	return s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		if err := loadTrip(ctx, repo, a.AccountID, tripID); err != nil {
			return err
		}
		existing, err := repo.ListForUpdate(ctx, a.AccountID, tripID)
		if err != nil {
			return err
		}
		current := make(map[uuid.UUID]Resource, len(existing))
		for _, r := range existing {
			current[r.ID] = r
		}
		now := s.clock.Now()
		kept := map[uuid.UUID]struct{}{}
		for i, in := range inputs {
			kept[in.id] = struct{}{}
			values := Values{Name: in.name, SharePercent: in.percent, SortOrder: int32(i)}
			if r, ok := current[in.id]; ok {
				var changed []string
				if r.Name != values.Name {
					changed = append(changed, "name")
				}
				if r.SharePercent != values.SharePercent {
					changed = append(changed, "share_percent")
				}
				if r.SortOrder != values.SortOrder {
					changed = append(changed, "sort_order")
				}
				if len(changed) == 0 {
					continue
				}
				updated, err := repo.Update(ctx, a.AccountID, tripID, in.id, values, now)
				if err != nil {
					return err
				}
				record(scope, updated, write.ChangeUpsert, changed)
				continue
			}
			exists, err := repo.IDExists(ctx, in.id)
			if err != nil {
				return err
			}
			if exists {
				return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
			}
			created, err := repo.Insert(ctx, a.AccountID, Resource{
				ID: in.id, TripID: tripID, Name: values.Name, SharePercent: values.SharePercent, SortOrder: values.SortOrder,
				Version: 1, CreatedAt: now, UpdatedAt: now,
			})
			if err != nil {
				return err
			}
			record(scope, created, write.ChangeUpsert, Fields)
		}
		for _, r := range existing {
			if _, ok := kept[r.ID]; ok {
				continue
			}
			if r.IsSelf {
				return apperr.Validation(apperr.Field("members", "SELF_REQUIRED", "「我」不能删除"))
			}
			refs, err := repo.ReferenceCount(ctx, a.AccountID, tripID, r.ID)
			if err != nil {
				return err
			}
			if refs > 0 {
				return apperr.Conflicted(codeMemberInUse, fmt.Sprintf("成员「%s」仍被 %d 条账目引用，不能删除", r.Name, refs))
			}
			deleted, err := repo.SoftDelete(ctx, a.AccountID, tripID, r.ID, now)
			if err != nil {
				return err
			}
			record(scope, deleted, write.ChangeDelete, nil)
		}
		return nil
	}, nil)
}
