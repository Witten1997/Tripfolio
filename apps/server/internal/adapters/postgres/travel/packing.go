package travelpg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/packing"
)

func toPackingResource(row dbgen.PackingItem) packing.Resource {
	return packing.Resource{
		ID: row.ID, TripID: row.TripID, Name: row.Name, Category: packing.Category(row.Category), Quantity: row.Quantity, Notes: row.Notes,
		Status: packing.Status(row.Status), Version: types.Version(row.Version),
		CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}
}

func packingTripInfo(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (packing.TripInfo, bool, error) {
	info, err := q.GetTripContentInfo(ctx, dbgen.GetTripContentInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return packing.TripInfo{}, false, nil
	}
	if err != nil {
		return packing.TripInfo{}, false, err
	}
	return packing.TripInfo{DeletedAt: pgcore.UTCPtr(info.DeletedAt)}, true, nil
}

func getPackingItem(ctx context.Context, q *dbgen.Queries, accountID, tripID, id uuid.UUID, forUpdate bool) (packing.Resource, bool, error) {
	var row dbgen.PackingItem
	var err error
	if forUpdate {
		row, err = q.GetPackingItemForUpdate(ctx, dbgen.GetPackingItemForUpdateParams{AccountID: accountID, TripID: tripID, ID: id})
	} else {
		row, err = q.GetPackingItem(ctx, dbgen.GetPackingItemParams{AccountID: accountID, TripID: tripID, ID: id})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return packing.Resource{}, false, nil
	}
	if err != nil {
		return packing.Resource{}, false, err
	}
	return toPackingResource(row), true, nil
}

// packingRepo 绑定到一次写事务。
type packingRepo struct {
	scope *pgcore.TxScope
}

var _ packing.Repo = (*packingRepo)(nil)

// NewPackingUnitOfWork 创建行李清单写事务入口。
func NewPackingUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[packing.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) packing.Repo {
		return &packingRepo{scope: scope}
	})
}

func (r *packingRepo) MergeSource() write.MergeSource { return r.scope }

func (r *packingRepo) Trip(ctx context.Context, accountID, tripID uuid.UUID) (packing.TripInfo, bool, error) {
	return packingTripInfo(ctx, r.scope.Queries, accountID, tripID)
}

func (r *packingRepo) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (packing.Resource, bool, error) {
	return getPackingItem(ctx, r.scope.Queries, accountID, tripID, id, false)
}

func (r *packingRepo) GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (packing.Resource, bool, error) {
	return getPackingItem(ctx, r.scope.Queries, accountID, tripID, id, true)
}

func (r *packingRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.PackingItemIDExists(ctx, dbgen.PackingItemIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return boolOf(exists), err
}

func (r *packingRepo) NameTaken(ctx context.Context, accountID, tripID uuid.UUID, category packing.Category, name string, exclude uuid.UUID) (bool, error) {
	return r.scope.Queries.PackingNameTaken(ctx, dbgen.PackingNameTakenParams{AccountID: accountID, TripID: tripID, Category: string(category), Name: name, ExcludeID: exclude})
}

func (r *packingRepo) ProbeBatch(ctx context.Context, accountID, tripID uuid.UUID, items []packing.BatchItem) (packing.TripInfo, bool, map[uuid.UUID]packing.BatchProbe, error) {
	input, err := json.Marshal(items)
	if err != nil {
		return packing.TripInfo{}, false, nil, err
	}
	const query = `
SELECT input.id, trip.id IS NOT NULL, trip.deleted_at, EXISTS (
    SELECT 1 FROM packing_items p
    WHERE p.account_id = $1 AND p.trip_id = $2 AND p.deleted_at IS NULL
      AND p.category = input.category AND lower(btrim(p.name)) = lower(btrim(input.name))
), EXISTS (SELECT 1 FROM packing_items p WHERE p.id = input.id)
   OR EXISTS (SELECT 1 FROM entity_tombstones t
              WHERE t.account_id = $1 AND t.entity_type = 'packing_item' AND t.entity_id = input.id)
FROM jsonb_to_recordset($3::jsonb) AS input(id uuid, category text, name text)
LEFT JOIN trips trip ON trip.account_id = $1 AND trip.id = $2`
	rows, err := r.scope.Tx.Query(ctx, query, accountID, tripID, input)
	if err != nil {
		return packing.TripInfo{}, false, nil, err
	}
	defer rows.Close()
	probes := make(map[uuid.UUID]packing.BatchProbe, len(items))
	var trip packing.TripInfo
	var found bool
	for rows.Next() {
		var id uuid.UUID
		var probe packing.BatchProbe
		if err := rows.Scan(&id, &found, &trip.DeletedAt, &probe.NameTaken, &probe.IDUsed); err != nil {
			return packing.TripInfo{}, false, nil, err
		}
		probes[id] = probe
	}
	return trip, found, probes, rows.Err()
}

func (r *packingRepo) Insert(ctx context.Context, accountID uuid.UUID, p packing.Resource) (packing.Resource, error) {
	row, err := r.scope.Queries.InsertPackingItem(ctx, dbgen.InsertPackingItemParams{
		ID: p.ID, AccountID: accountID, TripID: p.TripID, Name: p.Name, Category: string(p.Category), Quantity: p.Quantity, Notes: p.Notes, Status: string(p.Status), CreatedAt: p.CreatedAt,
	})
	if err != nil {
		return packing.Resource{}, err
	}
	return toPackingResource(row), nil
}

func (r *packingRepo) InsertBatch(ctx context.Context, accountID uuid.UUID, items []packing.Resource) ([]packing.Resource, error) {
	if len(items) == 0 {
		return []packing.Resource{}, nil
	}
	input, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	const query = `
INSERT INTO packing_items (id, account_id, trip_id, name, category, quantity, notes, status, created_at, updated_at)
SELECT input.id, $1, $2, input.name, input.category, input.quantity, input.notes, 'pending', $4, $4
FROM jsonb_to_recordset($3::jsonb) AS input(id uuid, name text, category text, quantity integer, notes text)
RETURNING id, account_id, version, created_at, updated_at, deleted_at, trip_id, name, category, quantity, notes, status`
	rows, err := r.scope.Tx.Query(ctx, query, accountID, items[0].TripID, input, items[0].CreatedAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[uuid.UUID]packing.Resource, len(items))
	for rows.Next() {
		var row dbgen.PackingItem
		if err := rows.Scan(&row.ID, &row.AccountID, &row.Version, &row.CreatedAt, &row.UpdatedAt, &row.DeletedAt,
			&row.TripID, &row.Name, &row.Category, &row.Quantity, &row.Notes, &row.Status); err != nil {
			return nil, err
		}
		byID[row.ID] = toPackingResource(row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	created := make([]packing.Resource, 0, len(items))
	for _, item := range items {
		r, ok := byID[item.ID]
		if !ok {
			return nil, fmt.Errorf("批量插入未返回物品 %s", item.ID)
		}
		created = append(created, r)
	}
	return created, nil
}

func (r *packingRepo) Update(ctx context.Context, accountID, tripID, id uuid.UUID, v packing.Values, now time.Time) (packing.Resource, error) {
	row, err := r.scope.Queries.UpdatePackingItem(ctx, dbgen.UpdatePackingItemParams{
		AccountID: accountID, TripID: tripID, ID: id, Name: v.Name, Category: string(v.Category), Quantity: v.Quantity, Notes: v.Notes, Status: string(v.Status), UpdatedAt: now,
	})
	if err != nil {
		return packing.Resource{}, err
	}
	return toPackingResource(row), nil
}

func (r *packingRepo) UpdateStatusIfVersion(ctx context.Context, accountID, tripID, id uuid.UUID, version int64, status packing.Status, now time.Time) (packing.Resource, bool, error) {
	row, err := r.scope.Queries.UpdatePackingStatusIfVersion(ctx, dbgen.UpdatePackingStatusIfVersionParams{
		AccountID: accountID, TripID: tripID, ID: id, Version: version, Status: string(status), UpdatedAt: now,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return packing.Resource{}, false, nil
	}
	if err != nil {
		return packing.Resource{}, false, err
	}
	return toPackingResource(row), true, nil
}

func (r *packingRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (packing.Resource, error) {
	row, err := r.scope.Queries.SoftDeletePackingItem(ctx, dbgen.SoftDeletePackingItemParams{AccountID: accountID, TripID: tripID, ID: id, DeletedAt: &now})
	if err != nil {
		return packing.Resource{}, err
	}
	return toPackingResource(row), nil
}

// PackingReader 是事务外只读仓储。
type PackingReader struct {
	q *dbgen.Queries
}

// NewPackingReader 创建只读仓储。
func NewPackingReader(pool *pgxpool.Pool) *PackingReader {
	return &PackingReader{q: dbgen.New(pool)}
}

var _ packing.Reader = (*PackingReader)(nil)

// Trip 实现 packing.Reader。
func (r *PackingReader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (packing.TripInfo, bool, error) {
	return packingTripInfo(ctx, r.q, accountID, tripID)
}

// Get 实现 packing.Reader。
func (r *PackingReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (packing.Resource, bool, error) {
	return getPackingItem(ctx, r.q, accountID, tripID, id, false)
}

// List 实现 packing.Reader。
func (r *PackingReader) List(ctx context.Context, accountID, tripID uuid.UUID, q packing.ListQuery) ([]packing.Resource, error) {
	p := dbgen.ListPackingItemsParams{AccountID: accountID, TripID: tripID, RowLimit: int32(q.Limit)}
	if q.Category != "" {
		c := string(q.Category)
		p.Category = &c
	}
	if q.Status != "" {
		s := string(q.Status)
		p.Status = &s
	}
	if q.After != nil {
		c := string(q.After.Category)
		at := q.After.CreatedAt
		p.CursorCategory, p.CursorCreatedAt, p.CursorID = &c, &at, uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	rows, err := r.q.ListPackingItems(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]packing.Resource, 0, len(rows))
	for _, row := range rows {
		out = append(out, toPackingResource(row))
	}
	return out, nil
}
