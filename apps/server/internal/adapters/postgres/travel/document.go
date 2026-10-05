package travelpg

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/content"
	"tripfolio/server/internal/modules/travel/document"
)

func toDocument(row dbgen.Document) (document.Resource, error) {
	return document.Resource{ID: row.ID, TripID: row.TripID, Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
		Title:         row.Title,
		Notes:         row.Notes,
		AssetID:       row.AssetID,
		ReservationID: contentUUIDPtr(row.ReservationID),
	}, nil
}

type documentReader struct{ contentReader }

func NewDocumentReader(pool *pgxpool.Pool) document.Reader {
	return &documentReader{contentReader{dbgen.New(pool)}}
}
func (r *documentReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (document.Resource, bool, error) {
	row, err := r.q.GetDocument(ctx, dbgen.GetDocumentParams{AccountID: accountID, TripID: tripID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		gone, e := r.q.ContentTombstoneExists(ctx, dbgen.ContentTombstoneExistsParams{AccountID: accountID, TripID: uuid.NullUUID{UUID: tripID, Valid: true}, EntityType: "document", EntityID: id})
		if e != nil {
			return document.Resource{}, false, e
		}
		if gone {
			return document.Resource{}, false, content.Gone()
		}
		return document.Resource{}, false, nil
	}
	if err != nil {
		return document.Resource{}, false, err
	}
	v, err := toDocument(row)
	return v, err == nil, err
}

type documentRepo struct {
	documentReader
	scope *pgcore.TxScope
}

func NewDocumentUnitOfWork(w *pgcore.Writer) write.UnitOfWork[document.Repo] {
	return pgcore.NewUnitOfWork(w, func(s *pgcore.TxScope) document.Repo {
		return &documentRepo{documentReader: documentReader{contentReader{s.Queries}}, scope: s}
	})
}
func (r *documentRepo) MergeSource() write.MergeSource { return r.scope }
func (r *documentRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.q.DocumentIDExists(ctx, dbgen.DocumentIDExistsParams{ID: id, AccountID: r.scope.AccountID})
}
func (r *documentRepo) Insert(ctx context.Context, accountID uuid.UUID, v document.Resource) (document.Resource, error) {
	row, err := r.q.InsertDocument(ctx, dbgen.InsertDocumentParams{AccountID: accountID, TripID: v.TripID, ID: v.ID, CreatedAt: v.CreatedAt,
		Title:         v.Title,
		Notes:         v.Notes,
		AssetID:       v.AssetID,
		ReservationID: contentUUID(v.ReservationID),
	})
	if err != nil {
		return document.Resource{}, err
	}
	return toDocument(row)
}
func (r *documentRepo) Update(ctx context.Context, accountID uuid.UUID, v document.Resource) (document.Resource, error) {
	row, err := r.q.UpdateDocument(ctx, dbgen.UpdateDocumentParams{AccountID: accountID, TripID: v.TripID, ID: v.ID, UpdatedAt: v.UpdatedAt,
		Title:         v.Title,
		Notes:         v.Notes,
		AssetID:       v.AssetID,
		ReservationID: contentUUID(v.ReservationID),
	})
	if err != nil {
		return document.Resource{}, err
	}
	return toDocument(row)
}
func (r *documentRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (document.Resource, error) {
	row, err := r.q.SoftDeleteDocument(ctx, dbgen.SoftDeleteDocumentParams{AccountID: accountID, TripID: tripID, ID: id, UpdatedAt: &now})
	if err != nil {
		return document.Resource{}, err
	}
	return toDocument(row)
}
func (r *documentReader) List(ctx context.Context, accountID, tripID uuid.UUID, q document.ListQuery) ([]document.Resource, error) {
	p := dbgen.ListDocumentsParams{AccountID: accountID, TripID: tripID, PageLimit: int32(q.Limit), ReservationID: contentUUID(q.ReservationID)}
	if q.After != nil {
		p.AfterID = q.After.ID
		p.AfterTime = &q.After.Time
	}
	rows, err := r.q.ListDocuments(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]document.Resource, 0, len(rows))
	for _, row := range rows {
		v, err := toDocument(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func NewDocumentRepository(scope *pgcore.TxScope) document.Repo {
	return &documentRepo{documentReader: documentReader{contentReader{scope.Queries}}, scope: scope}
}
