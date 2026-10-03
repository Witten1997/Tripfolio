package travelpg

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
	assetspg "tripfolio/server/internal/adapters/postgres/assets"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/travel/album"
	"tripfolio/server/internal/modules/travel/content"
)

func toPhoto(row dbgen.Photo) (album.Resource, error) {
	lat, err := contentNumber(row.Latitude)
	if err != nil {
		return album.Resource{}, err
	}
	lon, err := contentNumber(row.Longitude)
	if err != nil {
		return album.Resource{}, err
	}
	return album.Resource{ID: row.ID, TripID: row.TripID, Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
		AssetID:      row.AssetID,
		TakenAtLocal: contentLocal(row.TakenAtLocal),
		RecordedOn:   types.DateOf(row.RecordedOn),
		Caption:      row.Caption,
		SortOrder:    row.SortOrder,
		PlaceName:    row.PlaceName,
		Address:      row.Address,
		Latitude:     lat,
		Longitude:    lon,
	}, nil
}

type photoReader struct{ contentReader }

func NewPhotoReader(pool *pgxpool.Pool) album.Reader {
	return &photoReader{contentReader{dbgen.New(pool)}}
}
func (r *photoReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (album.Resource, bool, error) {
	row, err := r.q.GetPhoto(ctx, dbgen.GetPhotoParams{AccountID: accountID, TripID: tripID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		gone, e := r.q.ContentTombstoneExists(ctx, dbgen.ContentTombstoneExistsParams{AccountID: accountID, TripID: uuid.NullUUID{UUID: tripID, Valid: true}, EntityType: "photo", EntityID: id})
		if e != nil {
			return album.Resource{}, false, e
		}
		if gone {
			return album.Resource{}, false, content.Gone()
		}
		return album.Resource{}, false, nil
	}
	if err != nil {
		return album.Resource{}, false, err
	}
	v, err := toPhoto(row)
	return v, err == nil, err
}

type photoRepo struct {
	photoReader
	scope *pgcore.TxScope
}

func NewPhotoUnitOfWork(w *pgcore.Writer) write.UnitOfWork[album.Repo] {
	return pgcore.NewUnitOfWork(w, func(s *pgcore.TxScope) album.Repo {
		return &photoRepo{photoReader: photoReader{contentReader{s.Queries}}, scope: s}
	})
}
func (r *photoRepo) MergeSource() write.MergeSource { return r.scope }
func (r *photoRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.q.PhotoIDExists(ctx, dbgen.PhotoIDExistsParams{ID: id, AccountID: r.scope.AccountID})
}
func (r *photoRepo) Assets() assets.Repo { return assetspg.Bind(r.scope) }
func (r *photoRepo) Insert(ctx context.Context, accountID uuid.UUID, v album.Resource) (album.Resource, error) {
	row, err := r.q.InsertPhoto(ctx, dbgen.InsertPhotoParams{AccountID: accountID, TripID: v.TripID, ID: v.ID, CreatedAt: v.CreatedAt,
		AssetID:      v.AssetID,
		TakenAtLocal: contentTime(v.TakenAtLocal),
		RecordedOn:   v.RecordedOn.Time(),
		Caption:      v.Caption,
		SortOrder:    v.SortOrder,
		PlaceName:    v.PlaceName,
		Address:      v.Address,
		Latitude:     contentNumeric(v.Latitude),
		Longitude:    contentNumeric(v.Longitude),
	})
	if err != nil {
		return album.Resource{}, err
	}
	return toPhoto(row)
}
func (r *photoRepo) Update(ctx context.Context, accountID uuid.UUID, v album.Resource) (album.Resource, error) {
	row, err := r.q.UpdatePhoto(ctx, dbgen.UpdatePhotoParams{AccountID: accountID, TripID: v.TripID, ID: v.ID, UpdatedAt: v.UpdatedAt,
		AssetID:      v.AssetID,
		TakenAtLocal: contentTime(v.TakenAtLocal),
		RecordedOn:   v.RecordedOn.Time(),
		Caption:      v.Caption,
		SortOrder:    v.SortOrder,
		PlaceName:    v.PlaceName,
		Address:      v.Address,
		Latitude:     contentNumeric(v.Latitude),
		Longitude:    contentNumeric(v.Longitude),
	})
	if err != nil {
		return album.Resource{}, err
	}
	return toPhoto(row)
}
func (r *photoRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (album.Resource, error) {
	row, err := r.q.SoftDeletePhoto(ctx, dbgen.SoftDeletePhotoParams{AccountID: accountID, TripID: tripID, ID: id, UpdatedAt: &now})
	if err != nil {
		return album.Resource{}, err
	}
	return toPhoto(row)
}
func (r *photoReader) List(ctx context.Context, accountID, tripID uuid.UUID, q album.ListQuery) ([]album.Resource, error) {
	p := dbgen.ListPhotosParams{AccountID: accountID, TripID: tripID, PageLimit: int32(q.Limit), DateFrom: contentDate(q.DateFrom), DateTo: contentDate(q.DateTo), AfterTime: time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)}
	if q.After != nil {
		p.AfterID = q.After.ID
		p.AfterTime = q.After.Time
		p.AfterSort = q.After.Sort
		t := q.After.Day.Time()
		p.AfterDay = &t
	}
	rows, err := r.q.ListPhotos(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]album.Resource, 0, len(rows))
	for _, row := range rows {
		v, err := toPhoto(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
