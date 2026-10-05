package sync

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	assetspg "tripfolio/server/internal/adapters/postgres/assets"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	financepg "tripfolio/server/internal/adapters/postgres/finance"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/money"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/modules/metadata"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/packing"
	"tripfolio/server/internal/modules/travel/todo"
)

type snapshotStage struct {
	tx    pgx.Tx
	rows  [][]any
	count int64
}

func (s *snapshotStage) add(ctx context.Context, kind string, id uuid.UUID, tripID *uuid.UUID, version int64, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	s.count++
	s.rows = append(s.rows, []any{s.count, kind, id, tripID, version, data})
	if len(s.rows) >= 256 {
		return s.flush(ctx)
	}
	return nil
}
func (s *snapshotStage) flush(ctx context.Context) error {
	if len(s.rows) == 0 {
		return nil
	}
	_, err := s.tx.CopyFrom(ctx, pgx.Identifier{"pg_temp", "tripfolio_snapshot_stage"}, []string{"ordinal", "entity_type", "entity_id", "trip_id", "entity_version", "payload"}, pgx.CopyFromRows(s.rows))
	s.rows = nil
	return err
}

func materializeSnapshot(ctx context.Context, tx pgx.Tx, owner uuid.UUID, meta *syncmodule.Snapshot) (int64, error) {
	q := dbgen.New(tx)
	trips, err := q.SnapshotTrips(ctx, owner)
	if err != nil {
		return 0, err
	}
	requested := map[uuid.UUID]bool{}
	for _, id := range meta.SelectedTripIDs {
		requested[id] = true
	}
	selected := make([]uuid.UUID, 0, len(requested))
	stage := &snapshotStage{tx: tx}
	neededCategories := map[uuid.UUID]bool{}
	for _, row := range trips {
		if meta.Purpose == "baseline" || requested[row.ID] {
			value, err := travelpg.ToTripResource(row)
			if err != nil {
				return 0, err
			}
			if err = stage.add(ctx, "trip", row.ID, &row.ID, row.Version, value); err != nil {
				return 0, err
			}
		}
		if !requested[row.ID] || row.DeletedAt != nil {
			continue
		}
		selected = append(selected, row.ID)
		if err := materializeTrip(ctx, q, stage, owner, row, neededCategories); err != nil {
			return 0, err
		}
	}
	// Creation validates ownership. Revalidate removals in this exact captured view.
	for id := range requested {
		found := false
		for _, row := range trips {
			if row.ID == id {
				found = true
				break
			}
		}
		if !found {
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entity_tombstones WHERE account_id=$1 AND entity_type='trip' AND entity_id=$2) OR EXISTS(SELECT 1 FROM trips WHERE account_id=$1 AND id=$2 AND purge_requested_at IS NOT NULL)`, owner, id).Scan(&owned); err != nil {
				return 0, err
			}
			if !owned {
				return 0, errors.New("SNAPSHOT_SCOPE_CHANGED")
			}
		}
	}
	if meta.Purpose == "trip_reload" && len(selected) != len(requested) {
		return 0, errors.New("SNAPSHOT_SCOPE_CHANGED")
	}
	meta.SelectedTripIDs = selected
	categories, err := q.SnapshotCategories(ctx, owner)
	if err != nil {
		return 0, err
	}
	for _, row := range categories {
		if meta.Purpose == "baseline" || neededCategories[row.ID] {
			if err := stage.add(ctx, "expense_category", row.ID, nil, row.Version, financepg.SyncCategory(row)); err != nil {
				return 0, err
			}
		}
	}
	if err := stage.flush(ctx); err != nil {
		return 0, err
	}
	meta.ItemCount = strconv.FormatInt(stage.count, 10)
	return stage.count, nil
}

func materializeTrip(ctx context.Context, q *dbgen.Queries, stage *snapshotStage, owner uuid.UUID, trip dbgen.Trip, neededCategories map[uuid.UUID]bool) error {
	{
		rows, err := q.SnapshotMembers(ctx, dbgen.SnapshotMembersParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := travelpg.ToMemberResource(row)
			if err != nil {
				return err
			}
			if err := stage.add(ctx, "trip_member", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotItinerary(ctx, dbgen.SnapshotItineraryParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := travelpg.ToItineraryResource(row, trip.CurrencyCode)
			if err != nil {
				return err
			}
			if err := stage.add(ctx, "itinerary_item", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotPacking(ctx, dbgen.SnapshotPackingParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value := struct {
				packing.Resource
				SortOrder int32 `json:"sort_order"`
			}{travelpg.ToPackingResource(row), row.SortOrder}
			if err := stage.add(ctx, "packing_item", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotTodos(ctx, dbgen.SnapshotTodosParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value := struct {
				todo.Resource
				SortOrder int32 `json:"sort_order"`
			}{travelpg.ToTodoResource(row), row.SortOrder}
			if err := stage.add(ctx, "todo", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotReservations(ctx, dbgen.SnapshotReservationsParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := travelpg.SyncReservation(row)
			if err != nil {
				return err
			}
			if err := stage.add(ctx, "reservation", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotDocuments(ctx, dbgen.SnapshotDocumentsParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := travelpg.SyncDocument(row)
			if err != nil {
				return err
			}
			if err := stage.add(ctx, "document", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotPhotos(ctx, dbgen.SnapshotPhotosParams{AccountID: owner, TripID: trip.ID})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := travelpg.SyncPhoto(row)
			if err != nil {
				return err
			}
			if err := stage.add(ctx, "photo", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	{
		rows, err := q.SnapshotAssets(ctx, dbgen.SnapshotAssetsParams{AccountID: owner, TripID: uuid.NullUUID{UUID: trip.ID, Valid: true}})
		if err != nil {
			return err
		}
		for _, row := range rows {
			value, err := assetspg.SyncResource(row)
			if err != nil {
				return err
			}
			if err := stage.add(ctx, "asset", row.ID, &trip.ID, row.Version, value); err != nil {
				return err
			}
		}
	}
	entries, err := q.SnapshotLedger(ctx, dbgen.SnapshotLedgerParams{AccountID: owner, TripID: trip.ID})
	if err != nil {
		return err
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, row := range entries {
		ids = append(ids, row.ID)
	}
	attachments, err := q.ListLedgerAttachments(ctx, dbgen.ListLedgerAttachmentsParams{AccountID: owner, TripID: trip.ID, EntryIds: ids})
	if err != nil {
		return err
	}
	splits, err := q.ListLedgerSplits(ctx, dbgen.ListLedgerSplitsParams{AccountID: owner, TripID: trip.ID, EntryIds: ids})
	if err != nil {
		return err
	}
	byAttachment := map[uuid.UUID][]uuid.UUID{}
	for _, row := range attachments {
		byAttachment[row.LedgerEntryID] = append(byAttachment[row.LedgerEntryID], row.AssetID)
	}
	units, ok := metadata.MinorUnits(trip.CurrencyCode)
	if !ok {
		return errors.New("invalid snapshot currency")
	}
	bySplit := map[uuid.UUID][]finance.LedgerSplit{}
	for _, row := range splits {
		amount, err := money.FromStorage(row.Amount, units)
		if err != nil {
			return err
		}
		bySplit[row.LedgerEntryID] = append(bySplit[row.LedgerEntryID], finance.LedgerSplit{MemberID: row.MemberID, Amount: amount})
	}
	for _, row := range entries {
		neededCategories[row.CategoryID] = true
		at := append([]uuid.UUID{}, byAttachment[row.ID]...)
		sp := append([]finance.LedgerSplit{}, bySplit[row.ID]...)
		value, err := financepg.SyncLedger(row, trip.CurrencyCode, at, sp)
		if err != nil {
			return err
		}
		if err := stage.add(ctx, "ledger_entry", row.ID, &trip.ID, row.Version, value); err != nil {
			return err
		}
	}
	return nil
}
