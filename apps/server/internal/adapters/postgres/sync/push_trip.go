package sync

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/clock"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/trip"
)

// boundTripUOW never opens a transaction and deliberately has no AtomicPatcher.
type boundTripUOW struct{ scope *pgcore.TxScope }

func (b boundTripUOW) Run(ctx context.Context, _ write.Request, fn func(context.Context, write.Scope, trip.Repo) error, _ func(context.Context, trip.Repo) (any, error)) (write.Result, error) {
	return write.Result{}, fn(ctx, b.scope, travelpg.NewTripRepository(b.scope))
}

type transactionClock struct{ at time.Time }

func (c transactionClock) Now() time.Time { return c.at }

var _ clock.Clock = transactionClock{}

func executeTrip(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation, base int64) error {
	svc := trip.NewService(boundTripUOW{scope}, nil, nil, transactionClock{scope.Now}, 0)
	id := *op.EntityID
	var err error
	switch op.Type {
	case "trip.create":
		var cmd struct {
			trip.CreateCommand
			SelfMemberID uuid.UUID `json:"self_member_id"`
		}
		if err = json.Unmarshal(op.Payload, &cmd); err != nil {
			return err
		}
		cmd.ID = id
		_, err = svc.CreateWithSelf(ctx, a, op.OperationID, cmd.CreateCommand, cmd.SelfMemberID)
	case "trip.update":
		var patch trip.Patch
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(op.Payload, &patch); err != nil {
			return err
		}
		_ = json.Unmarshal(op.Payload, &fields)
		_, patch.BudgetSet = fields["budget_amount"]
		_, err = svc.Update(ctx, a, op.OperationID, id, base, patch)
	case "trip.set_archived":
		var p struct {
			Archived bool `json:"archived"`
		}
		if err = json.Unmarshal(op.Payload, &p); err != nil {
			return err
		}
		_, err = svc.SetArchived(ctx, a, op.OperationID, id, base, p.Archived)
	case "trip.delete":
		_, err = svc.Trash(ctx, a, op.OperationID, id, base)
	case "trip.restore":
		_, err = svc.Restore(ctx, a, op.OperationID, id, base)
	}
	if e, ok := apperr.As(err); ok && e.Code == "VERSION_CONFLICT" && e.Conflict != nil && e.Conflict.ExpectedVersion < e.Conflict.CurrentVersion && e.Conflict.ConflictingFields == nil && op.Type == "trip.update" {
		copy := *e
		copy.Code = "MERGE_HISTORY_UNAVAILABLE"
		return &copy
	}
	return err
}
