package httpapi

import (
	"context"
	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/modules/travel/album"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func (h *Handler) ListPhotos(ctx context.Context, req generated.ListPhotosRequestObject) (generated.ListPhotosResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.photos == nil {
		return nil, notWired()
	}
	f := album.Filters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	if req.Params.DateFrom != nil {
		f.DateFrom = *req.Params.DateFrom
	}
	if req.Params.DateTo != nil {
		f.DateTo = *req.Params.DateTo
	}
	page, err := h.photos.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListPhotos200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}
func (h *Handler) GetPhoto(ctx context.Context, req generated.GetPhotoRequestObject) (generated.GetPhotoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.photos == nil {
		return nil, notWired()
	}
	v, err := h.photos.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.PhotoId))
	if err != nil {
		return nil, err
	}
	tag := etag(v.Version)
	return generated.GetPhoto200JSONResponse{Body: generated.PhotoResponse{Data: v}, Headers: generated.GetPhoto200ResponseHeaders{ETag: &tag}}, nil
}
func (h *Handler) CreatePhoto(ctx context.Context, req generated.CreatePhotoRequestObject) (generated.CreatePhotoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.photos == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	p := album.Patch{}
	p.AssetID = b.AssetId
	p.TakenAtLocalSet, p.TakenAtLocal = contentNullableLocal(b.TakenAtLocal)
	if b.RecordedOn != nil {
		v := types.Date(*b.RecordedOn)
		p.RecordedOn = &v
	}
	p.Caption = b.Caption
	p.SortOrder = b.SortOrder
	p.PlaceName = b.PlaceName
	p.Address = b.Address
	p.LatitudeSet, p.Latitude = nullableFloat(b.Latitude)
	p.LongitudeSet, p.Longitude = nullableFloat(b.Longitude)
	cmd := album.CreateCommand{ID: uuid.UUID(b.Id), Patch: p}
	if b.Asset != nil {
		cmd.Asset = &album.InlineAsset{OriginalName: b.Asset.OriginalName, ExpectedSize: b.Asset.ExpectedSize, DeclaredMediaType: string(b.Asset.DeclaredMediaType), ClientSHA256: b.Asset.ClientSha256}
	}
	res, err := h.photos.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreatePhoto201JSONResponse{Data: res}, nil
}
func (h *Handler) UpdatePhoto(ctx context.Context, req generated.UpdatePhotoRequestObject) (generated.UpdatePhotoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.photos == nil {
		return nil, notWired()
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	p := album.Patch{}
	p.AssetID = b.AssetId
	p.TakenAtLocalSet, p.TakenAtLocal = contentNullableLocal(b.TakenAtLocal)
	if b.RecordedOn != nil {
		v := types.Date(*b.RecordedOn)
		p.RecordedOn = &v
	}
	p.Caption = b.Caption
	p.SortOrder = b.SortOrder
	p.PlaceName = b.PlaceName
	p.Address = b.Address
	p.LatitudeSet, p.Latitude = nullableFloat(b.Latitude)
	p.LongitudeSet, p.Longitude = nullableFloat(b.Longitude)
	res, err := h.photos.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.PhotoId), base, p)
	if err != nil {
		return nil, err
	}
	return generated.UpdatePhoto200JSONResponse{Data: album.WriteResult{Result: res}}, nil
}
func (h *Handler) DeletePhoto(ctx context.Context, req generated.DeletePhotoRequestObject) (generated.DeletePhotoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.photos == nil {
		return nil, notWired()
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.photos.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.PhotoId), base)
	if err != nil {
		return nil, err
	}
	return generated.DeletePhoto200JSONResponse{Data: album.WriteResult{Result: res}}, nil
}
