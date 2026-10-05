package sync

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/google/uuid"
	"tripfolio/server/internal/adapters/objectstore"
	assetspg "tripfolio/server/internal/adapters/postgres/assets"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	travelpg "tripfolio/server/internal/adapters/postgres/travel"
	"tripfolio/server/internal/foundation/actor"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/modules/metadata"
	syncmodule "tripfolio/server/internal/modules/sync"
	"tripfolio/server/internal/modules/travel/album"
	"tripfolio/server/internal/modules/travel/content"
)

// mediaObjectKeys adapts the existing pure key derivation; no storage client is
// created and no upload URL is stored in the operation receipt.
type mediaObjectKeys struct{}

func (mediaObjectKeys) KeysFor(accountID, assetID uuid.UUID, attempt int) assets.Keys {
	k := objectstore.KeysFor(accountID, assetID, attempt)
	return assets.Keys{Staging: k.Staging, Final: k.Final, Thumbnail: k.Thumbnail}
}

func executeMedia(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, epoch uuid.UUID, op syncmodule.PreparedOperation, base int64, deps map[uuid.UUID]*write.SyncFacts) error {
	if op.EntityType == "asset" {
		if len(op.Guards) != 0 {
			return apperr.Unprocessable("INVALID_REFERENCE", "资产注册不接受集合条件")
		}
		var p struct {
			Scope             assets.Scope `json:"scope"`
			TripID            uuid.UUID    `json:"trip_id"`
			OriginalName      string       `json:"original_name"`
			ExpectedSize      int64        `json:"expected_size"`
			DeclaredMediaType string       `json:"declared_media_type"`
			ClientSHA256      *string      `json:"client_sha256"`
		}
		if err := json.Unmarshal(op.Payload, &p); err != nil {
			return err
		}
		if p.Scope != assets.ScopeTrip || p.TripID != *op.TripID {
			return apperr.Unprocessable("INVALID_REFERENCE", "资产旅行范围不匹配")
		}
		limits := metadata.Current().UploadLimits
		svc := assets.NewService(assets.Deps{Keys: mediaObjectKeys{}, Clock: transactionClock{scope.Now}, Limits: assets.UploadLimits{ImageMaxBytes: limits.ImageMaxBytes, PDFMaxBytes: limits.PDFMaxBytes, ImageMediaTypes: limits.ImageMediaTypes, PDFMediaTypes: limits.PDFMediaTypes}})
		_, err := svc.RegisterInTransaction(ctx, scope, assetspg.Bind(scope), a.AccountID, assets.CreateInput{ID: *op.EntityID, Scope: p.Scope, TripID: op.TripID, OriginalName: p.OriginalName, ExpectedSize: p.ExpectedSize, DeclaredMediaType: p.DeclaredMediaType, ClientSHA256: p.ClientSHA256})
		return err
	}
	repo := travelpg.NewPhotoRepository(scope)
	if _, err := content.LoadTrip(ctx, repo, a.AccountID, *op.TripID); err != nil {
		return err
	}
	var p album.Patch
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(op.Payload, &p); err != nil {
		return err
	}
	if err := json.Unmarshal(op.Payload, &fields); err != nil {
		return err
	}
	_, p.TakenAtLocalSet = fields["taken_at_local"]
	_, p.LatitudeSet = fields["latitude"]
	_, p.LongitudeSet = fields["longitude"]
	days := map[types.Date]bool{}
	required := false
	if op.Type == "photo.update" || op.Type == "photo.delete" {
		current, found, err := repo.Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
		if err != nil {
			return err
		}
		if !found {
			return apperr.NotFound()
		}
		if current.DeletedAt != nil {
			return content.Gone()
		}
		days[current.RecordedOn] = true
		if op.Type == "photo.update" {
			if p.SortOrder != nil {
				return apperr.Validation(apperr.Field("sort_order", "NOT_ALLOWED", "更新顺序请使用完整集合操作"))
			}
			finalDay := current.RecordedOn
			if p.RecordedOn != nil {
				finalDay = *p.RecordedOn
			} else if p.TakenAtLocalSet && p.TakenAtLocal != nil {
				finalDay = p.TakenAtLocal.Date()
			}
			days[finalDay] = true
			required = p.RecordedOn != nil || p.TakenAtLocalSet
		}
	} else {
		if p.RecordedOn == nil {
			return apperr.Validation(apperr.Field("recorded_on", "REQUIRED", "日期必填"))
		}
		days[*p.RecordedOn] = true
		required = op.Type == "photo.reorder"
	}
	orderedDays := make([]types.Date, 0, len(days))
	for day := range days {
		orderedDays = append(orderedDays, day)
	}
	sort.Slice(orderedDays, func(i, j int) bool { return orderedDays[i] < orderedDays[j] })
	if err := checkMediaGuards(ctx, scope, epoch, op, orderedDays, required, deps); err != nil {
		return err
	}
	svc := album.NewService(boundItemUOW[album.Repo]{scope, repo}, nil, nil, transactionClock{scope.Now}, nil)
	var err error
	switch op.Type {
	case "photo.create":
		_, err = svc.Create(ctx, a, op.OperationID, *op.TripID, album.CreateCommand{ID: *op.EntityID, Patch: p})
	case "photo.update":
		_, err = svc.Update(ctx, a, op.OperationID, *op.TripID, *op.EntityID, base, p)
	case "photo.delete":
		_, err = svc.Delete(ctx, a, op.OperationID, *op.TripID, *op.EntityID, base)
	case "photo.reorder":
		var order struct {
			IDs []uuid.UUID `json:"ordered_ids"`
		}
		if err = json.Unmarshal(op.Payload, &order); err == nil {
			_, err = svc.Reorder(ctx, a, op.OperationID, *op.TripID, *p.RecordedOn, order.IDs)
		}
	default:
		return apperr.Unprocessable("OFFLINE_OPERATION_NOT_ALLOWED", "不支持的媒体操作")
	}
	if err != nil {
		if e, ok := apperr.As(err); ok && e.Code == "VERSION_CONFLICT" && e.Conflict != nil && e.Conflict.ExpectedVersion < e.Conflict.CurrentVersion && e.Conflict.ConflictingFields == nil && op.Type == "photo.update" {
			copy := *e
			copy.Code = "MERGE_HISTORY_UNAVAILABLE"
			return &copy
		}
		return err
	}
	for _, day := range orderedDays {
		if err := scope.RecordPhotoDayRevision(ctx, epoch, *op.TripID, day); err != nil {
			return err
		}
	}
	return nil
}

func checkMediaGuards(ctx context.Context, scope *pgcore.TxScope, epoch uuid.UUID, op syncmodule.PreparedOperation, days []types.Date, required bool, deps map[uuid.UUID]*write.SyncFacts) error {
	allowed := map[string]types.Date{}
	for _, day := range days {
		allowed[op.TripID.String()+"/"+string(day)] = day
	}
	seen := map[string]bool{}
	for _, guard := range op.Guards {
		_, ok := allowed[guard.ScopeID]
		if !ok || guard.Kind != "photo_day" || seen[guard.ScopeID] {
			return apperr.Unprocessable("INVALID_REFERENCE", "集合不属于本次照片操作")
		}
		seen[guard.ScopeID] = true
	}
	if required && len(seen) != len(allowed) {
		return apperr.New(428, "COLLECTION_BASE_REQUIRED", "需要全部来源和目标日期的集合版本")
	}
	for _, guard := range op.Guards {
		day := allowed[guard.ScopeID]
		expected := ""
		if guard.Revision != nil {
			expected = *guard.Revision
		} else if guard.OperationID != nil {
			if facts := deps[*guard.OperationID]; facts != nil {
				for _, revision := range facts.ScopeRevisions {
					if revision.Kind == guard.Kind && revision.ScopeID == guard.ScopeID {
						expected = revision.Revision
						break
					}
				}
			}
		}
		if expected == "" {
			return apperr.Unprocessable("INVALID_REFERENCE", "依赖没有记录目标集合版本")
		}
		current, err := scope.PhotoDayRevision(ctx, epoch, *op.TripID, day)
		if err != nil {
			return err
		}
		if current.Revision != expected {
			return apperr.New(412, "COLLECTION_CONFLICT", "照片集合已变化")
		}
	}
	return nil
}

func reloadMedia(ctx context.Context, scope *pgcore.TxScope, a actor.Actor, op syncmodule.Operation) (any, error) {
	if op.EntityID == nil {
		return nil, nil
	}
	trip, found, err := travelpg.NewTripRepository(scope).Get(ctx, a.AccountID, *op.TripID)
	if err != nil {
		return nil, err
	}
	if !found || trip.PurgeRequestedAt != nil {
		return nil, nil
	}
	var value any
	if op.EntityType == "asset" {
		value, found, err = assetspg.Bind(scope).GetForUpdate(ctx, a.AccountID, *op.EntityID)
	} else {
		value, found, err = travelpg.NewPhotoRepository(scope).Get(ctx, a.AccountID, *op.TripID, *op.EntityID)
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
