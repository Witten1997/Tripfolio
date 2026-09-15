// Package assetspg 实现 assets 模块的 PostgreSQL 适配：事务内仓储与只读仓储。
// 暂存键、最终键与声明信息只在本包与对象键推导之间流转，不进入公开资源。
package assetspg

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
)

// toResource 把行转为公开资源。EXIF 坐标 NUMERIC 以字符串返回，转回 float64；
// 拍摄时间 timestamp(0) 无时区，按原样映射为 LocalDateTime。
func toResource(row dbgen.Asset) (assets.Resource, error) {
	lat, err := floatPtr(row.ExifLatitude)
	if err != nil {
		return assets.Resource{}, err
	}
	lng, err := floatPtr(row.ExifLongitude)
	if err != nil {
		return assets.Resource{}, err
	}
	var tripID *uuid.UUID
	if row.TripID.Valid {
		id := row.TripID.UUID
		tripID = &id
	}
	var sum *string
	if len(row.Sha256) > 0 {
		s := hex.EncodeToString(row.Sha256)
		sum = &s
	}
	return assets.Resource{
		ID: row.ID, TripID: tripID, Scope: assets.Scope(row.Scope), OriginalName: row.OriginalName,
		Status: assets.Status(row.Status), UploadAttempt: row.UploadAttempt, UploadExpiresAt: pgcore.UTCPtr(row.UploadExpiresAt),
		MediaType: row.MediaType, ByteSize: row.ByteSize, SHA256: sum, Width: row.Width, Height: row.Height,
		ExifTakenAt: localPtr(row.ExifTakenAtLocal), ExifLatitude: lat, ExifLongitude: lng,
		ThumbnailStatus: assets.ThumbnailStatus(row.ThumbnailStatus), ErrorCode: row.ErrorCode,
		Version: types.Version(row.Version), CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt),
		DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}, nil
}

func toAttempt(row dbgen.Asset) assets.AttemptInfo {
	info := assets.AttemptInfo{ClientSHA256: row.ClientSha256}
	if row.ExpectedSize != nil {
		info.ExpectedSize = *row.ExpectedSize
	}
	if row.DeclaredMediaType != nil {
		info.DeclaredMediaType = *row.DeclaredMediaType
	}
	return info
}

func localPtr(t *time.Time) *types.LocalDateTime {
	if t == nil {
		return nil
	}
	l := types.LocalDateTimeOf(*t)
	return &l
}

func localTime(l *types.LocalDateTime) *time.Time {
	if l == nil {
		return nil
	}
	t := l.Time()
	return &t
}

func floatPtr(s *string) (*float64, error) {
	if s == nil {
		return nil, nil
	}
	v, err := strconv.ParseFloat(*s, 64)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func floatText(v *float64) *string {
	if v == nil {
		return nil
	}
	s := strconv.FormatFloat(*v, 'f', 6, 64)
	return &s
}

func tripInfo(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (assets.TripInfo, bool, error) {
	deletedAt, err := q.GetAssetTripInfo(ctx, dbgen.GetAssetTripInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return assets.TripInfo{}, false, nil
	}
	if err != nil {
		return assets.TripInfo{}, false, err
	}
	return assets.TripInfo{DeletedAt: pgcore.UTCPtr(deletedAt)}, true, nil
}

func getAsset(ctx context.Context, q *dbgen.Queries, accountID, id uuid.UUID, forUpdate bool) (assets.Resource, bool, error) {
	var row dbgen.Asset
	var err error
	if forUpdate {
		row, err = q.GetAssetForUpdate(ctx, dbgen.GetAssetForUpdateParams{AccountID: accountID, ID: id})
	} else {
		row, err = q.GetAsset(ctx, dbgen.GetAssetParams{AccountID: accountID, ID: id})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return assets.Resource{}, false, nil
	}
	if err != nil {
		return assets.Resource{}, false, err
	}
	res, err := toResource(row)
	return res, err == nil, err
}

// repo 是事务内仓储。
type repo struct {
	scope *pgcore.TxScope
}

var _ assets.Repo = (*repo)(nil)

// NewUnitOfWork 创建资产写事务入口。
func NewUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[assets.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) assets.Repo {
		return &repo{scope: scope}
	})
}

func (r *repo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.AssetIDExists(ctx, dbgen.AssetIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return exists != nil && *exists, err
}

func (r *repo) Trip(ctx context.Context, accountID, tripID uuid.UUID) (assets.TripInfo, bool, error) {
	return tripInfo(ctx, r.scope.Queries, accountID, tripID)
}

func (r *repo) Insert(ctx context.Context, accountID uuid.UUID, res assets.Resource, attempt assets.AttemptInfo, stagingKey string) (assets.Resource, error) {
	var tripID uuid.NullUUID
	if res.TripID != nil {
		tripID = uuid.NullUUID{UUID: *res.TripID, Valid: true}
	}
	var expiresAt time.Time
	if res.UploadExpiresAt != nil {
		expiresAt = *res.UploadExpiresAt
	}
	row, err := r.scope.Queries.InsertAsset(ctx, dbgen.InsertAssetParams{
		ID: res.ID, AccountID: accountID, CreatedAt: res.CreatedAt, Scope: string(res.Scope), TripID: tripID,
		OriginalName: res.OriginalName, StagingObjectKey: &stagingKey, ExpectedSize: &attempt.ExpectedSize,
		DeclaredMediaType: &attempt.DeclaredMediaType, ClientSha256: attempt.ClientSHA256, UploadExpiresAt: &expiresAt,
	})
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

func (r *repo) GetForUpdate(ctx context.Context, accountID, assetID uuid.UUID) (assets.Resource, bool, error) {
	return getAsset(ctx, r.scope.Queries, accountID, assetID, true)
}

func (r *repo) StartAttempt(ctx context.Context, accountID, assetID uuid.UUID, attempt int32, stagingKey string, expiresAt, now time.Time) (assets.Resource, error) {
	row, err := r.scope.Queries.StartAssetAttempt(ctx, dbgen.StartAssetAttemptParams{
		AccountID: accountID, ID: assetID, UploadAttempt: attempt, StagingObjectKey: &stagingKey,
		UploadExpiresAt: &expiresAt, UpdatedAt: now,
	})
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

func (r *repo) RenewAttempt(ctx context.Context, accountID, assetID uuid.UUID, expiresAt, now time.Time) (assets.Resource, error) {
	row, err := r.scope.Queries.RenewAssetAttempt(ctx, dbgen.RenewAssetAttemptParams{
		AccountID: accountID, ID: assetID, UploadExpiresAt: &expiresAt, UpdatedAt: now,
	})
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

func (r *repo) MarkProcessing(ctx context.Context, accountID, assetID uuid.UUID, now time.Time) (assets.Resource, error) {
	row, err := r.scope.Queries.MarkAssetProcessing(ctx, dbgen.MarkAssetProcessingParams{
		AccountID: accountID, ID: assetID, ConfirmedAt: &now, UpdatedAt: now,
	})
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

func (r *repo) MarkReady(ctx context.Context, accountID, assetID uuid.UUID, info assets.VerifiedInfo, now time.Time) (assets.Resource, error) {
	p := dbgen.MarkAssetReadyParams{
		AccountID: accountID, ID: assetID, ObjectKey: &info.FinalKey, MediaType: &info.MediaType,
		ByteSize: &info.ByteSize, Sha256: info.SHA256, ThumbnailStatus: string(assets.ThumbnailNone), UpdatedAt: now,
	}
	// 图片走缩略图流程；PDF 没有尺寸与 EXIF，缩略图状态保持 none。
	if img := info.Image; img != nil {
		p.Width, p.Height = &img.Width, &img.Height
		p.ExifTakenAtLocal = localTime(img.TakenAt)
		p.ExifLatitude, p.ExifLongitude = floatText(img.Latitude), floatText(img.Longitude)
		p.ThumbnailStatus = string(assets.ThumbnailProcessing)
	}
	row, err := r.scope.Queries.MarkAssetReady(ctx, p)
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

func (r *repo) MarkFailed(ctx context.Context, accountID, assetID uuid.UUID, errorCode string, now time.Time) (assets.Resource, error) {
	row, err := r.scope.Queries.MarkAssetFailed(ctx, dbgen.MarkAssetFailedParams{
		AccountID: accountID, ID: assetID, ErrorCode: &errorCode, UpdatedAt: now,
	})
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

func (r *repo) SetThumbnail(ctx context.Context, accountID, assetID uuid.UUID, status assets.ThumbnailStatus, thumbnailKey string, now time.Time) (assets.Resource, error) {
	var key *string
	if thumbnailKey != "" {
		key = &thumbnailKey
	}
	row, err := r.scope.Queries.SetAssetThumbnail(ctx, dbgen.SetAssetThumbnailParams{
		AccountID: accountID, ID: assetID, ThumbnailStatus: string(status), ThumbnailObjectKey: key, UpdatedAt: now,
	})
	if err != nil {
		return assets.Resource{}, err
	}
	return toResource(row)
}

// Reader 是事务外只读仓储。
type Reader struct {
	q *dbgen.Queries
}

// NewReader 创建只读仓储。
func NewReader(pool *pgxpool.Pool) *Reader {
	return &Reader{q: dbgen.New(pool)}
}

var _ assets.Reader = (*Reader)(nil)

// Get 实现 assets.Reader。
func (r *Reader) Get(ctx context.Context, accountID, assetID uuid.UUID) (assets.Resource, bool, error) {
	return getAsset(ctx, r.q, accountID, assetID, false)
}

// Attempt 实现 assets.Reader。
func (r *Reader) Attempt(ctx context.Context, accountID, assetID uuid.UUID) (assets.AttemptInfo, bool, error) {
	row, err := r.q.GetAsset(ctx, dbgen.GetAssetParams{AccountID: accountID, ID: assetID})
	if errors.Is(err, pgx.ErrNoRows) {
		return assets.AttemptInfo{}, false, nil
	}
	if err != nil {
		return assets.AttemptInfo{}, false, err
	}
	return toAttempt(row), true, nil
}

// GetMany 实现 assets.Reader：只返回属于本账号的行，顺序与输入一致。
func (r *Reader) GetMany(ctx context.Context, accountID uuid.UUID, ids []uuid.UUID) ([]assets.Resource, error) {
	if len(ids) == 0 {
		return []assets.Resource{}, nil
	}
	rows, err := r.q.GetAssetsByIDs(ctx, dbgen.GetAssetsByIDsParams{Ids: ids, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	out := make([]assets.Resource, 0, len(rows))
	for _, row := range rows {
		res, err := toResource(row)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// ListByTrip 实现 assets.Reader。
func (r *Reader) ListByTrip(ctx context.Context, accountID, tripID uuid.UUID, q assets.ListQuery) ([]assets.Resource, error) {
	p := dbgen.ListTripAssetsParams{
		AccountID: accountID, TripID: uuid.NullUUID{UUID: tripID, Valid: true}, Ids: q.IDs, RowLimit: int32(q.Limit),
	}
	if p.Ids == nil {
		p.Ids = []uuid.UUID{}
	}
	if q.Status != nil {
		s := string(*q.Status)
		p.Status = &s
	}
	if q.After != nil {
		createdAt := q.After.CreatedAt
		p.CursorCreatedAt = &createdAt
		p.CursorID = uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	rows, err := r.q.ListTripAssets(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]assets.Resource, 0, len(rows))
	for _, row := range rows {
		res, err := toResource(row)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, nil
}

// Trip 实现 assets.Reader。
func (r *Reader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (assets.TripInfo, bool, error) {
	return tripInfo(ctx, r.q, accountID, tripID)
}
