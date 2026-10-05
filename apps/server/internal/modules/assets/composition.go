package assets

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
)

type tripReader interface {
	Trip(context.Context, uuid.UUID, uuid.UUID) (TripInfo, bool, error)
}

func checkAssetTrip(ctx context.Context, reader tripReader, accountID uuid.UUID, res Resource) error {
	if res.TripID == nil {
		return nil
	}
	trip, found, err := reader.Trip(ctx, accountID, *res.TripID)
	if err != nil {
		return apperr.Internal(err)
	}
	if !found {
		return apperr.NotFound()
	}
	if trip.DeletedAt != nil {
		return apperr.Gone("TRIP_DELETED", "旅行已在回收站中")
	}
	return nil
}

// ValidatePhotoCreate reuses asset limits before composing a photo transaction.
func (s *Service) ValidatePhotoCreate(in CreateInput) error {
	if err := s.storageReady(); err != nil {
		return err
	}
	if err := s.validateCreate(in); err != nil {
		return err
	}
	switch normalizeMediaType(in.DeclaredMediaType) {
	case "image/jpeg", "image/png", "image/webp":
		return nil
	}
	return apperr.Validation(apperr.Field("asset.declared_media_type", "INVALID", "照片只允许图片资产"))
}

// CreateInTransaction registers the asset in the caller's account write transaction.
// It does not perform object storage IO or commit independently.
func (s *Service) CreateInTransaction(ctx context.Context, scope write.Scope, repo Repo, accountID uuid.UUID, in CreateInput) (Resource, error) {
	if err := s.storageReady(); err != nil {
		return Resource{}, err
	}
	return s.RegisterInTransaction(ctx, scope, repo, accountID, in)
}

// RegisterInTransaction stores metadata and the first attempt without signing
// URLs or making object storage calls. The caller owns the account transaction.
func (s *Service) RegisterInTransaction(ctx context.Context, scope write.Scope, repo Repo, accountID uuid.UUID, in CreateInput) (Resource, error) {
	if s.keys == nil {
		return Resource{}, apperr.Dependency(errors.New("对象键生成器未配置"))
	}
	if err := s.validateCreate(in); err != nil {
		return Resource{}, err
	}
	exists, err := repo.IDExists(ctx, in.ID)
	if err != nil {
		return Resource{}, err
	}
	if exists {
		return Resource{}, apperr.Conflicted("ID_ALREADY_USED", "该 ID 已被使用")
	}
	if in.Scope == ScopeTrip {
		info, found, err := repo.Trip(ctx, accountID, *in.TripID)
		if err != nil {
			return Resource{}, err
		}
		if !found {
			return Resource{}, apperr.NotFound()
		}
		if info.DeletedAt != nil {
			return Resource{}, apperr.Gone("TRIP_DELETED", "旅行已在回收站中")
		}
	}
	now := s.clock.Now()
	expires := now.Add(UploadWindow)
	attempt := AttemptInfo{ExpectedSize: in.ExpectedSize, DeclaredMediaType: normalizeMediaType(in.DeclaredMediaType)}
	if in.ClientSHA256 != nil {
		attempt.ClientSHA256 = decodeHex(*in.ClientSHA256)
	}
	res := Resource{ID: in.ID, TripID: in.TripID, Scope: in.Scope, OriginalName: strings.TrimSpace(in.OriginalName), Status: StatusUploading, UploadAttempt: 1, UploadExpiresAt: &expires, ThumbnailStatus: ThumbnailNone, Version: 1, CreatedAt: now, UpdatedAt: now}
	res, err = repo.Insert(ctx, accountID, res, attempt, s.keys.KeysFor(accountID, in.ID, 1).Staging)
	if err != nil {
		return Resource{}, err
	}
	recordWrite(scope, res, nil)
	return res, nil
}

// CurrentUploadAuthorization runs after commit; no temporary URL enters a receipt or snapshot.
func (s *Service) CurrentUploadAuthorization(ctx context.Context, a actor.Actor, id uuid.UUID) (*UploadAuthorization, error) {
	res, err := s.Get(ctx, a, id)
	if err != nil {
		if e, ok := apperr.As(err); ok && (e.Status == 404 || e.Status == 410) {
			return nil, nil
		}
		return nil, err
	}
	if res.Status != StatusUploading || res.UploadExpiresAt == nil || !res.UploadExpiresAt.After(s.clock.Now()) {
		return nil, nil
	}
	if err := s.storageReady(); err != nil {
		return nil, err
	}
	attempt, found, err := s.reader.Attempt(ctx, a.AccountID, id)
	if err != nil {
		return nil, apperr.Internal(err)
	}
	if !found {
		return nil, nil
	}
	return s.authorizeUploadFor(ctx, a.AccountID, res, attempt)
}
