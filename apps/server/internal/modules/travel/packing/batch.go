package packing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
)

// BatchItem 是批量创建中的一件物品；状态固定为 pending，不覆盖既有条目。
type BatchItem struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Category Category  `json:"category"`
	Quantity *int32    `json:"quantity"`
	Notes    *string   `json:"notes"`
}

// BatchCommand 是批量创建命令（接口设计 3.4 PackingBatchCreate）。
type BatchCommand struct {
	Items []BatchItem `json:"items"`
}

// CreateBatch 在一个事务与同一变更批次中创建最多 100 件物品。
// 与本旅行同分类同名的有效物品、以及请求内重复的同分类同名项被跳过而不报错；
// 全部跳过时仍返回成功与空 affected。
func (s *Service) CreateBatch(ctx context.Context, a actor.Actor, operationID, tripID uuid.UUID, cmd BatchCommand) (BatchResult, error) {
	if len(cmd.Items) == 0 {
		return BatchResult{}, apperr.Validation(apperr.Field("items", "REQUIRED", "至少提交一件物品"))
	}
	if len(cmd.Items) > maxBatch {
		return BatchResult{}, apperr.Validation(apperr.Field("items", "TOO_MANY", fmt.Sprintf("一次最多 %d 件物品", maxBatch)))
	}
	var fields []apperr.FieldError
	seenIDs := make(map[uuid.UUID]struct{}, len(cmd.Items))
	normalized := make([]BatchItem, len(cmd.Items))
	for i, item := range cmd.Items {
		path := fmt.Sprintf("items[%d]", i)
		if item.ID == uuid.Nil {
			fields = append(fields, apperr.Field(path+".id", "INVALID", "id 必填"))
		} else if _, dup := seenIDs[item.ID]; dup {
			fields = append(fields, apperr.Field(path+".id", "DUPLICATE", "同一 id 只能出现一次"))
		} else {
			seenIDs[item.ID] = struct{}{}
		}
		name, ferr := validateName(item.Name)
		if ferr != nil {
			fields = append(fields, apperr.Field(path+".name", ferr.Code, ferr.Message))
		}
		item.Name = name
		if !item.Category.Valid() {
			fields = append(fields, apperr.Field(path+".category", "INVALID", "分类不合法"))
		}
		if item.Quantity != nil {
			if ferr := validateQuantity(path+".quantity", *item.Quantity); ferr != nil {
				fields = append(fields, *ferr)
			}
		}
		if item.Notes != nil {
			if ferr := validateNotes(path+".notes", *item.Notes); ferr != nil {
				fields = append(fields, *ferr)
			}
		}
		normalized[i] = item
	}
	if len(fields) > 0 {
		return BatchResult{}, apperr.Validation(fields...)
	}
	cmd.Items = normalized

	req := write.Request{
		AccountID: a.AccountID, OperationID: operationID, OperationType: "packing.batch_create",
		Fingerprint: write.Fingerprint("packing.batch_create", tripID.String(), nil, cmd),
	}
	res, err := s.uow.Run(ctx, req, func(ctx context.Context, scope write.Scope, repo Repo) error {
		now := s.clock.Now()
		started := time.Now()
		trip, found, probes, err := repo.ProbeBatch(ctx, a.AccountID, tripID, cmd.Items)
		write.RecordTiming(ctx, "batch_probe", time.Since(started))
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if trip.DeletedAt != nil {
			return tripDeleted()
		}
		taken := map[string]struct{}{}
		toInsert := make([]Resource, 0, len(cmd.Items))
		for _, item := range cmd.Items {
			key := string(item.Category) + "\x00" + normalizeName(item.Name)
			if _, dup := taken[key]; dup {
				continue
			}
			probe := probes[item.ID]
			if probe.NameTaken {
				taken[key] = struct{}{}
				continue
			}
			if probe.IDUsed {
				return apperr.Conflicted(codeIDAlreadyUsed, "该 ID 已被使用")
			}
			quantity := int32(1)
			if item.Quantity != nil {
				quantity = *item.Quantity
			}
			notes := ""
			if item.Notes != nil {
				notes = *item.Notes
			}
			toInsert = append(toInsert, Resource{
				ID: item.ID, TripID: tripID, Name: item.Name, Category: item.Category, Quantity: quantity,
				Notes: notes, Status: StatusPending, Version: 1, CreatedAt: now, UpdatedAt: now,
			})
			taken[key] = struct{}{}
		}
		started = time.Now()
		created, err := repo.InsertBatch(ctx, a.AccountID, toInsert)
		write.RecordTiming(ctx, "batch_insert", time.Since(started))
		if err != nil {
			return err
		}
		for _, item := range created {
			record(scope, item, write.ChangeUpsert, Fields)
		}
		return nil
	}, nil)
	if err != nil {
		return BatchResult{}, err
	}
	return buildBatchResult(res, cmd.Items), nil
}

// buildBatchResult 从写结果的 affected 推导创建与跳过明细；重放时同样成立，
// 因为未创建的物品只可能因同分类同名被跳过。
func buildBatchResult(res write.Result, items []BatchItem) BatchResult {
	created := make(map[uuid.UUID]struct{}, len(res.Affected))
	for _, ref := range res.Affected {
		if ref.Type == EntityType {
			created[ref.ID] = struct{}{}
		}
	}
	out := BatchResult{Result: res, CreatedIDs: []uuid.UUID{}, Skipped: []SkippedItem{}}
	out.Primary = nil
	out.Data = nil
	for _, item := range items {
		if _, ok := created[item.ID]; ok {
			out.CreatedIDs = append(out.CreatedIDs, item.ID)
			continue
		}
		out.Skipped = append(out.Skipped, SkippedItem{ID: item.ID, Name: item.Name, Category: item.Category, Reason: ReasonDuplicate})
	}
	return out
}
