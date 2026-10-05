package sync

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/collectionguard"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/itinerary"
)

func executeItinerary(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, epoch uuid.UUID, op syncmodule.PreparedOperation, base int64, deps map[uuid.UUID]*write.SyncFacts) error {
	tripID := *op.TripID
	repo := travelpg.NewItineraryRepository(scope)
	info, found, err := repo.Trip(ctx, a.AccountID, tripID)
	if err != nil {
		return err
	}
	if !found {
		return apperr.NotFound()
	}
	if info.DeletedAt != nil {
		return apperr.Gone("TRIP_DELETED", "旅行已在回收站中")
	}
	svc := itinerary.NewService(boundItemUOW[itinerary.Repo]{scope, repo}, nil, nil, transactionClock{scope.Now})
	var dates []types.Date
	var create itinerary.CreateCommand
	var current itinerary.Resource
	var reorder struct {
		Days []struct {
			Date types.Date  `json:"date"`
			IDs  []uuid.UUID `json:"ordered_ids"`
		} `json:"days"`
	}
	switch op.Type {
	case "itinerary_item.create":
		if err = json.Unmarshal(op.Payload, &create); err != nil {
			return err
		}
		create.ID = *op.EntityID
		date, e := types.ParseDate(create.ScheduledOn)
		if e != nil {
			return apperr.Validation(apperr.Field("scheduled_on", "INVALID", "日期无效"))
		}
		dates = []types.Date{date}
	case "itinerary_item.update", "itinerary_item.delete":
		current, found, err = repo.GetForUpdate(ctx, a.AccountID, tripID, *op.EntityID)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return apperr.Gone("RESOURCE_GONE", "行程已删除")
		}
		dates = []types.Date{current.ScheduledOn}
	case "itinerary_item.reorder":
		if err = json.Unmarshal(op.Payload, &reorder); err != nil {
			return err
		}
		for _, d := range reorder.Days {
			dates = append(dates, d.Date)
		}
	default:
		return apperr.Unprocessable("OFFLINE_OPERATION_NOT_ALLOWED", "不支持的行程操作")
	}
	if op.Type == "itinerary_item.update" {
		if len(op.Guards) != 0 {
			return apperr.Unprocessable("INVALID_REFERENCE", "行程编辑不接受无关集合条件")
		}
	} else if err := checkItineraryGuards(ctx, scope, epoch, tripID, dates, op.Guards, deps); err != nil {
		return err
	}
	switch op.Type {
	case "itinerary_item.create":
		_, err = svc.Create(ctx, a, op.OperationID, tripID, create)
	case "itinerary_item.delete":
		_, err = svc.Delete(ctx, a, op.OperationID, tripID, *op.EntityID, base)
	case "itinerary_item.update":
		var p itinerary.Patch
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(op.Payload, &p); err != nil {
			return err
		}
		if err = json.Unmarshal(op.Payload, &fields); err != nil {
			return err
		}
		_, p.PlannedStartSet = fields["planned_start_local"]
		_, p.PlannedEndSet = fields["planned_end_local"]
		_, p.PlannedDurationSet = fields["planned_duration_minutes"]
		_, p.LatitudeSet = fields["latitude"]
		_, p.LongitudeSet = fields["longitude"]
		_, p.EstimatedAmountSet = fields["estimated_amount"]
		_, p.ActualStartSet = fields["actual_start_local"]
		_, p.ActualEndSet = fields["actual_end_local"]
		_, err = svc.Update(ctx, a, op.OperationID, tripID, *op.EntityID, base, p)
	case "itinerary_item.reorder":
		rows, e := repo.ListDaysForUpdate(ctx, a.AccountID, tripID, dates)
		if e != nil {
			return e
		}
		versions := make(map[uuid.UUID]int64, len(rows))
		for _, r := range rows {
			versions[r.ID] = int64(r.Version)
		}
		cmd := itinerary.ReorderCommand{Days: make([]itinerary.ReorderDay, 0, len(dates))}
		for _, d := range reorder.Days {
			day := itinerary.ReorderDay{Date: string(d.Date), Items: make([]itinerary.ReorderItem, 0, len(d.IDs))}
			for _, id := range d.IDs {
				v, exists := versions[id]
				if !exists {
					return apperr.Unprocessable("INVALID_REFERENCE", "项目不属于受保护日期集合")
				}
				day.Items = append(day.Items, itinerary.ReorderItem{ID: id, BaseVersion: v})
			}
			cmd.Days = append(cmd.Days, day)
		}
		_, err = svc.Reorder(ctx, a, op.OperationID, tripID, cmd)
	}
	if e, ok := apperr.As(err); ok && e.Code == "VERSION_CONFLICT" && e.Conflict != nil && e.Conflict.ExpectedVersion < e.Conflict.CurrentVersion && e.Conflict.ConflictingFields == nil && op.Type == "itinerary_item.update" {
		copy := *e
		copy.Code = "MERGE_HISTORY_UNAVAILABLE"
		return &copy
	}
	if err != nil {
		return err
	}
	for _, date := range dates {
		if err := scope.RecordItineraryDayRevision(ctx, epoch, tripID, date); err != nil {
			return err
		}
	}
	return nil
}

func checkItineraryGuards(ctx context.Context, scope *pgcore.TxScope, epoch, tripID uuid.UUID, dates []types.Date, guards []syncmodule.GuardReference, deps map[uuid.UUID]*write.SyncFacts) error {
	required := make([]collectionguard.Scope, 0, len(dates))
	wanted := map[string]bool{}
	for _, date := range dates {
		key := tripID.String() + "/" + string(date)
		if wanted[key] {
			return apperr.Unprocessable("INVALID_REFERENCE", "日期重复")
		}
		wanted[key] = true
		required = append(required, collectionguard.Scope{Kind: "itinerary_day", ScopeID: key})
	}
	seen := map[string]bool{}
	for _, g := range guards {
		if g.Kind != "itinerary_day" || !wanted[g.ScopeID] || seen[g.ScopeID] {
			return apperr.Unprocessable("INVALID_REFERENCE", "无关或重复日期集合")
		}
		seen[g.ScopeID] = true
		if g.Revision == nil && g.OperationID != nil {
			if facts := deps[*g.OperationID]; facts == nil || facts.Epoch != epoch {
				return apperr.Unprocessable("INVALID_REFERENCE", "依赖缺少本代次原提交事实")
			}
		}
	}
	resolved, err := resolveCollectionGuards(guards, deps)
	if err != nil {
		return err
	}
	// Verification and proof belong to this exact account transaction. The bound
	// service can require the same dates without accepting a caller-supplied flag.
	return scope.CheckCollections(ctx, resolved, required)
}

func reloadItinerary(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation) (any, error) {
	trip, found, err := travelpg.NewTripRepository(scope).Get(ctx, a.AccountID, *op.TripID)
	if err != nil {
		return nil, err
	}
	if !found || trip.PurgeRequestedAt != nil || op.EntityID == nil {
		return nil, nil
	}
	r, found, err := travelpg.NewItineraryRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
	if e, ok := apperr.As(err); ok && e.Code == "RESOURCE_GONE" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return r, nil
}
