package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/assets"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 资产处理器（接口设计 2.7）。只做参数转换与分发，上传状态机在 assets.Service。
// 下载授权响应固定 Cache-Control: no-store：签名地址不能被中间缓存留存。

var noStore = "no-store"

// toAssetWriteResult 把统一写结果与上传授权合成契约的 AssetWriteResult。
func toAssetWriteResult(res write.Result, auth *assets.UploadAuthorization) generated.AssetWriteResult {
	out := generated.AssetWriteResult{
		OperationId: openapi_types.UUID(res.OperationID),
		Replayed:    &res.Replayed,
		Warnings:    &res.Warnings,
	}
	if res.Primary != nil {
		out.Primary = toEntityRef(*res.Primary)
	}
	affected := make([]generated.EntityRef, 0, len(res.Affected))
	for _, ref := range res.Affected {
		affected = append(affected, toEntityRef(ref))
	}
	out.Affected = &affected
	if res.CommitCursor != nil {
		out.CommitCursor = nullable.NewNullableWithValue(*res.CommitCursor)
	} else {
		out.CommitCursor = nullable.NewNullNullable[string]()
	}
	if data, ok := res.Data.(assets.Resource); ok {
		out.Data = nullable.NewNullableWithValue(data)
	} else {
		out.Data = nullable.NewNullNullable[assets.Resource]()
	}
	if auth != nil {
		out.UploadAuthorization = nullable.NewNullableWithValue(generated.UploadAuthorization{
			UploadAttempt:   int(auth.UploadAttempt),
			Method:          generated.UploadAuthorizationMethod(auth.Method),
			Url:             auth.URL,
			RequiredHeaders: auth.RequiredHeaders,
			ExpiresAt:       auth.ExpiresAt,
		})
	} else {
		out.UploadAuthorization = nullable.NewNullNullable[generated.UploadAuthorization]()
	}
	return out
}

func toEntityRef(ref write.EntityRef) generated.EntityRef {
	out := generated.EntityRef{Type: ref.Type, Id: openapi_types.UUID(ref.ID)}
	if ref.Version != nil {
		out.Version = nullable.NewNullableWithValue(ref.Version.String())
	} else {
		out.Version = nullable.NewNullNullable[string]()
	}
	return out
}

func toDownloadAuthorization(a assets.DownloadAuthorization) generated.DownloadAuthorization {
	out := generated.DownloadAuthorization{
		AssetId:         openapi_types.UUID(a.AssetID),
		Status:          generated.AssetStatus(a.Status),
		ThumbnailStatus: generated.ThumbnailStatus(a.ThumbnailStatus),
		FileName:        a.FileName,
		Url:             nullable.NewNullNullable[string](),
		ExpiresAt:       nullable.NewNullNullable[time.Time](),
		MediaType:       nullable.NewNullNullable[string](),
		ByteSize:        nullable.NewNullNullable[int](),
	}
	if a.URL != nil {
		out.Url = nullable.NewNullableWithValue(*a.URL)
	}
	if a.ExpiresAt != nil {
		out.ExpiresAt = nullable.NewNullableWithValue(*a.ExpiresAt)
	}
	if a.MediaType != nil {
		out.MediaType = nullable.NewNullableWithValue(*a.MediaType)
	}
	if a.ByteSize != nil {
		out.ByteSize = nullable.NewNullableWithValue(int(*a.ByteSize))
	}
	return out
}

func downloadVariant(v *generated.DownloadVariant) assets.Variant {
	if v == nil {
		return assets.VariantOriginal
	}
	return assets.Variant(*v)
}

func downloadDisposition(d *generated.DownloadDisposition) assets.Disposition {
	if d == nil {
		return assets.DispositionInline
	}
	return assets.Disposition(*d)
}

// CreateAsset 实现 POST /assets。
func (h *Handler) CreateAsset(ctx context.Context, req generated.CreateAssetRequestObject) (generated.CreateAssetResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	in := assets.CreateInput{
		ID: uuid.UUID(b.Id), Scope: assets.Scope(b.Scope), TripID: optionalUUID(b.TripId),
		OriginalName: b.OriginalName, ExpectedSize: int64(b.ExpectedSize), DeclaredMediaType: b.DeclaredMediaType,
	}
	if b.ClientSha256 != nil {
		s := string(*b.ClientSha256)
		in.ClientSHA256 = &s
	}
	res, auth, err := h.assets.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), in)
	if err != nil {
		return nil, err
	}
	return generated.CreateAsset201JSONResponse{Data: toAssetWriteResult(res, auth)}, nil
}

// GetAsset 实现 GET /assets/{asset_id}。
func (h *Handler) GetAsset(ctx context.Context, req generated.GetAssetRequestObject) (generated.GetAssetResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	r, err := h.assets.Get(ctx, a, uuid.UUID(req.AssetId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetAsset200JSONResponse{
		Body:    generated.AssetResponse{Data: r},
		Headers: generated.GetAsset200ResponseHeaders{ETag: &tag},
	}, nil
}

// AuthorizeAssetUpload 实现 POST /assets/{asset_id}/upload-authorization。
func (h *Handler) AuthorizeAssetUpload(ctx context.Context, req generated.AuthorizeAssetUploadRequestObject) (generated.AuthorizeAssetUploadResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	res, auth, err := h.assets.AuthorizeUpload(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.AssetId))
	if err != nil {
		return nil, err
	}
	return generated.AuthorizeAssetUpload200JSONResponse{Data: toAssetWriteResult(res, auth)}, nil
}

// ConfirmAssetUpload 实现 POST /assets/{asset_id}/confirm。
func (h *Handler) ConfirmAssetUpload(ctx context.Context, req generated.ConfirmAssetUploadRequestObject) (generated.ConfirmAssetUploadResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	res, err := h.assets.Confirm(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.AssetId), int32(req.Body.UploadAttempt))
	if err != nil {
		return nil, err
	}
	return generated.ConfirmAssetUpload202JSONResponse{Data: res}, nil
}

// AuthorizeAssetDownload 实现 GET /assets/{asset_id}/download。
func (h *Handler) AuthorizeAssetDownload(ctx context.Context, req generated.AuthorizeAssetDownloadRequestObject) (generated.AuthorizeAssetDownloadResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	auth, err := h.assets.AuthorizeDownload(ctx, a, assets.DownloadRequest{
		AssetID: uuid.UUID(req.AssetId), Variant: downloadVariant(req.Params.Variant),
	}, downloadDisposition(req.Params.Disposition))
	if err != nil {
		return nil, err
	}
	return generated.AuthorizeAssetDownload200JSONResponse{
		Body:    generated.DownloadAuthorizationResponse{Data: toDownloadAuthorization(auth)},
		Headers: generated.AuthorizeAssetDownload200ResponseHeaders{CacheControl: &noStore},
	}, nil
}

// AuthorizeAssetDownloads 实现 POST /assets/download-authorizations。
func (h *Handler) AuthorizeAssetDownloads(ctx context.Context, req generated.AuthorizeAssetDownloadsRequestObject) (generated.AuthorizeAssetDownloadsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	items := make([]assets.DownloadRequest, 0, len(req.Body.Items))
	for _, it := range req.Body.Items {
		items = append(items, assets.DownloadRequest{AssetID: uuid.UUID(it.AssetId), Variant: downloadVariant(it.Variant)})
	}
	auths, err := h.assets.AuthorizeDownloads(ctx, a, items, downloadDisposition(req.Body.Disposition))
	if err != nil {
		return nil, err
	}
	out := make([]generated.DownloadAuthorization, 0, len(auths))
	for _, auth := range auths {
		out = append(out, toDownloadAuthorization(auth))
	}
	return generated.AuthorizeAssetDownloads200JSONResponse{
		Body:    generated.DownloadAuthorizationBatchResponse{Data: generated.DownloadAuthorizationBatch{Items: out}},
		Headers: generated.AuthorizeAssetDownloads200ResponseHeaders{CacheControl: &noStore},
	}, nil
}

// ListTripAssets 实现 GET /trips/{trip_id}/assets。
func (h *Handler) ListTripAssets(ctx context.Context, req generated.ListTripAssetsRequestObject) (generated.ListTripAssetsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.assets == nil {
		return nil, notWired()
	}
	p := req.Params
	f := assets.ListFilters{IDs: deref(p.Ids), Limit: pageLimit(p.Limit), Cursor: deref(p.Cursor)}
	if p.Status != nil {
		f.Status = string(*p.Status)
	}
	page, err := h.assets.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListTripAssets200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}
