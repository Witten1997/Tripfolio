package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
)

type PushStore struct{ writer *pgcore.Writer }

func NewPushStore(writer *pgcore.Writer) *PushStore { return &PushStore{writer} }

func (s *PushStore) Execute(ctx context.Context, a actor.Actor, epoch uuid.UUID, op syncmodule.PreparedOperation) (write.Result, error) {
	req := write.Request{AccountID: a.AccountID, OperationID: op.OperationID, OperationType: "sync." + op.Type, Fingerprint: op.Fingerprint}
	return s.writer.RunSync(ctx, req, pgcore.SyncWriteOptions{Epoch: epoch}, func(ctx context.Context, scope *pgcore.TxScope) error {
		deps := map[uuid.UUID]*write.SyncFacts{}
		for _, id := range op.DependsOn {
			var raw []byte
			err := scope.Tx.QueryRow(ctx, `SELECT result FROM mutation_receipts WHERE account_id=$1 AND operation_id=$2`, a.AccountID, id).Scan(&raw)
			if errors.Is(err, pgx.ErrNoRows) {
				code := "DEPENDENCY_NOT_APPLIED"
				if op.BlockedDependencies[id] {
					code = "DEPENDENCY_REJECTED"
				}
				return apperr.New(424, code, "依赖操作尚未成功")
			}
			if err != nil {
				return err
			}
			var result write.Result
			if err = json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if result.Sync == nil || result.Sync.Epoch != epoch {
				return apperr.Unprocessable("INVALID_REFERENCE", "依赖缺少本代次原提交事实")
			}
			deps[id] = result.Sync
		}
		var base int64
		if op.Type == "trip.create" {
			var p struct {
				SelfMemberID uuid.UUID `json:"self_member_id"`
			}
			if err := json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			var used bool
			if err := scope.Tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM trip_members WHERE id=$1) OR EXISTS(SELECT 1 FROM entity_tombstones WHERE account_id=$2 AND entity_type='trip_member' AND entity_id=$1)`, p.SelfMemberID, a.AccountID).Scan(&used); err != nil {
				return err
			}
			if used {
				return apperr.Conflicted("ID_ALREADY_USED", "成员ID已被使用")
			}
		}
		if op.Base != nil {
			if op.Base.Version != nil {
				base, _ = strconv.ParseInt(*op.Base.Version, 10, 64)
			} else {
				facts := deps[*op.Base.OperationID]
				if facts != nil {
					for _, r := range facts.References {
						if r.Type == op.EntityType && r.ID == *op.EntityID && r.Version != nil {
							base = int64(*r.Version)
							break
						}
					}
				}
				if base < 1 {
					return apperr.Unprocessable("INVALID_REFERENCE", "依赖没有记录目标实体版本")
				}
			}
		}
		if op.EntityType == "reservation" || op.EntityType == "document" {
			if len(op.Guards) > 0 {
				return apperr.Unprocessable("INVALID_REFERENCE", "该操作不接受无关集合条件")
			}
			return executeContent(ctx, scope, a, op.Operation, base)
		}
		kind := "members"
		switch op.EntityType {
		case "packing_item":
			kind = "packing_order"
		case "todo":
			kind = "todo_order"
		}
		if (op.Type == "packing_item.reorder" || op.Type == "todo.reorder") && len(op.Guards) == 0 {
			return apperr.New(428, "COLLECTION_BASE_REQUIRED", "排序需要集合版本")
		}
		for _, g := range op.Guards {
			if g.Kind != kind || g.ScopeID != op.TripID.String() {
				return apperr.Unprocessable("INVALID_REFERENCE", "该集合不属于本次旅行操作")
			}
			var expected string
			if g.Revision != nil {
				expected = *g.Revision
			} else {
				if facts := deps[*g.OperationID]; facts != nil {
					for _, r := range facts.ScopeRevisions {
						if r.Kind == g.Kind && r.ScopeID == g.ScopeID {
							expected = r.Revision
							break
						}
					}
				}
			}
			if expected == "" {
				return apperr.Unprocessable("INVALID_REFERENCE", "依赖没有记录目标集合版本")
			}
			revision, err := scope.CollectionRevision(ctx, epoch, kind, *op.TripID)
			if err != nil {
				return err
			}
			if revision.Revision != expected {
				return apperr.New(412, "COLLECTION_CONFLICT", "集合已变化")
			}
		}
		var err error
		if op.EntityType == "trip" {
			err = executeTrip(ctx, scope, a, op.Operation, base)
		} else {
			err = executeItem(ctx, scope, a, op.Operation, base)
		}
		if err != nil {
			return err
		}
		if op.EntityType != "trip" {
			return scope.RecordCollectionRevision(ctx, epoch, kind, *op.TripID)
		}
		if op.Type == "trip.create" || len(op.Guards) > 0 {
			return scope.RecordMembersRevision(ctx, epoch, *op.TripID)
		}
		return nil
	}, func(ctx context.Context, scope *pgcore.TxScope) (any, error) {
		if op.EntityType == "reservation" || op.EntityType == "document" {
			return reloadContent(ctx, scope, a, op.Operation)
		}
		if op.EntityType != "trip" {
			return reloadItem(ctx, scope, a, op.Operation)
		}
		r, found, err := travelpg.NewTripRepository(scope).Get(ctx, a.AccountID, *op.EntityID)
		if err != nil {
			return nil, err
		}
		if !found || r.PurgeRequestedAt != nil {
			return nil, nil
		}
		return r, nil
	})
}
