package travelpg

import (
	"context"
	"errors"
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

func (r *packingRepo) Insert(ctx context.Context, accountID uuid.UUID, p packing.Resource) (packing.Resource, error) {
	row, err := r.scope.Queries.InsertPackingItem(ctx, dbgen.InsertPackingItemParams{
		ID: p.ID, AccountID: accountID, TripID: p.TripID, Name: p.Name, Category: string(p.Category), Quantity: p.Quantity, Notes: p.Notes, Status: string(p.Status), CreatedAt: p.CreatedAt,
	})
	if err != nil {
		return packing.Resource{}, err
	}
	return toPackingResource(row), nil
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
