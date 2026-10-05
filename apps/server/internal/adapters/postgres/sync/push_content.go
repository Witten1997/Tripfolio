package sync

import (
	"context"
	"encoding/json"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/document"
	"tripfolio/server/internal/modules/travel/reservation"
)

func executeContent(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation, base int64) error {
	id, tripID := *op.EntityID, *op.TripID
	var err error
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(op.Payload, &fields); err != nil {
		return err
	}
	if op.EntityType == "reservation" {
		svc := reservation.NewService(boundItemUOW[reservation.Repo]{scope, travelpg.NewReservationRepository(scope)}, nil, nil, transactionClock{scope.Now})
		var p reservation.Patch
		if err = json.Unmarshal(op.Payload, &p); err != nil {
			return err
		}
		_, p.TransportNumberSet = fields["transport_number"]
		_, p.ProviderNameSet = fields["provider_name"]
		_, p.StartLocalSet = fields["start_local"]
		_, p.EndLocalSet = fields["end_local"]
		_, p.OriginSet = fields["origin"]
		_, p.DestinationSet = fields["destination"]
		_, p.ContactNameSet = fields["contact_name"]
		_, p.ContactPhoneSet = fields["contact_phone"]
		switch op.Type {
		case "reservation.create":
			_, err = svc.Create(ctx, a, op.OperationID, tripID, reservation.CreateCommand{ID: id, Patch: p})
		case "reservation.update":
			_, err = svc.Update(ctx, a, op.OperationID, tripID, id, base, p)
		case "reservation.delete":
			_, err = svc.Delete(ctx, a, op.OperationID, tripID, id, base)
		}
	} else {
		svc := document.NewService(boundItemUOW[document.Repo]{scope, travelpg.NewDocumentRepository(scope)}, nil, nil, transactionClock{scope.Now})
		var p document.Patch
		if err = json.Unmarshal(op.Payload, &p); err != nil {
			return err
		}
		_, p.ReservationIDSet = fields["reservation_id"]
		switch op.Type {
		case "document.create":
			_, err = svc.Create(ctx, a, op.OperationID, tripID, document.CreateCommand{ID: id, Patch: p})
		case "document.update":
			_, err = svc.Update(ctx, a, op.OperationID, tripID, id, base, p)
		case "document.delete":
			_, err = svc.Delete(ctx, a, op.OperationID, tripID, id, base)
		}
	}
	if e, ok := apperr.As(err); ok && e.Code == "VERSION_CONFLICT" && e.Conflict != nil && e.Conflict.ExpectedVersion < e.Conflict.CurrentVersion && e.Conflict.ConflictingFields == nil && (op.Type == "reservation.update" || op.Type == "document.update") {
		copy := *e
		copy.Code = "MERGE_HISTORY_UNAVAILABLE"
		return &copy
	}
	return err
}
func reloadContent(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation) (any, error) {
	trip, found, err := travelpg.NewTripRepository(scope).Get(ctx, a.AccountID, *op.TripID)
	if err != nil {
		return nil, err
	}
	if !found || trip.PurgeRequestedAt != nil {
		return nil, nil
	}
	var value any
	if op.EntityType == "reservation" {
		value, found, err = travelpg.NewReservationRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
	} else {
		value, found, err = travelpg.NewDocumentRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
	}
	if e, ok := apperr.As(err); ok && e.Code == "RESOURCE_GONE" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return value, nil
}
