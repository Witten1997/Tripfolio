package sync

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/todo"
)

type boundItemUOW[R any] struct {
	scope *pgcore.TxScope
	repo  R
}

func (b boundItemUOW[R]) Run(ctx context.Context, _ write.Request, fn func(context.Context, write.Scope, R) error, _ func(context.Context, R) (any, error)) (write.Result, error) {
	return write.Result{}, fn(ctx, b.scope, b.repo)
}
func executeItem(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation, base int64) error {
	tripID := *op.TripID
	var id uuid.UUID
	if op.EntityID != nil {
		id = *op.EntityID
	}
	var err error
	if op.EntityType == "packing_item" {
		svc := packing.NewService(boundItemUOW[packing.Repo]{scope, travelpg.NewPackingRepository(scope)}, nil, nil, transactionClock{scope.Now})
		switch op.Type {
		case "packing_item.create":
			var c packing.CreateCommand
			if err = json.Unmarshal(op.Payload, &c); err != nil {
				return err
			}
			c.ID = id
			_, err = svc.Create(ctx, a, op.OperationID, tripID, c)
		case "packing_item.update":
			var p packing.Patch
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, err = svc.Update(ctx, a, op.OperationID, tripID, id, base, p)
		case "packing_item.set_status":
			var p struct {
				Status packing.Status `json:"status"`
			}
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, err = svc.SetStatus(ctx, a, op.OperationID, tripID, id, base, p.Status)
		case "packing_item.delete":
			_, err = svc.Delete(ctx, a, op.OperationID, tripID, id, base)
		case "packing_item.reorder":
			var p struct {
				IDs []uuid.UUID `json:"ordered_ids"`
			}
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, err = svc.Reorder(ctx, a, op.OperationID, tripID, p.IDs)
		}
	} else {
		svc := todo.NewService(boundItemUOW[todo.Repo]{scope, travelpg.NewTodoRepository(scope)}, nil, nil, transactionClock{scope.Now})
		switch op.Type {
		case "todo.create":
			var c todo.CreateCommand
			if err = json.Unmarshal(op.Payload, &c); err != nil {
				return err
			}
			c.ID = id
			_, err = svc.Create(ctx, a, op.OperationID, tripID, c)
		case "todo.update":
			var p todo.Patch
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_ = json.Unmarshal(op.Payload, &fields)
			_, p.DueOnSet = fields["due_on"]
			_, err = svc.Update(ctx, a, op.OperationID, tripID, id, base, p)
		case "todo.set_completed":
			var p struct {
				Completed bool `json:"completed"`
			}
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, err = svc.SetCompleted(ctx, a, op.OperationID, tripID, id, base, p.Completed)
		case "todo.delete":
			_, err = svc.Delete(ctx, a, op.OperationID, tripID, id, base)
		case "todo.reorder":
			var p struct {
				IDs []uuid.UUID `json:"ordered_ids"`
			}
			if err = json.Unmarshal(op.Payload, &p); err != nil {
				return err
			}
			_, err = svc.Reorder(ctx, a, op.OperationID, tripID, p.IDs)
		}
	}
	if e, ok := apperr.As(err); ok && e.Code == "VERSION_CONFLICT" && e.Conflict != nil && e.Conflict.ExpectedVersion < e.Conflict.CurrentVersion && e.Conflict.ConflictingFields == nil && (op.Type == "packing_item.update" || op.Type == "todo.update") {
		copy := *e
		copy.Code = "MERGE_HISTORY_UNAVAILABLE"
		return &copy
	}
	return err
}
func reloadItem(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation) (any, error) {
	if op.EntityID == nil {
		return nil, nil
	}
	trip, found, err := travelpg.NewTripRepository(scope).Get(ctx, a.AccountID, *op.TripID)
	if err != nil {
		return nil, err
	}
	if !found || trip.PurgeRequestedAt != nil {
		return nil, nil
	}
	if op.EntityType == "packing_item" {
		r, found, err := travelpg.NewPackingRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		return r, nil
	}
	r, found, err := travelpg.NewTodoRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return r, nil
}
