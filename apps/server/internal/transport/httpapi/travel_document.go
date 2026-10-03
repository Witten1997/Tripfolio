package httpapi

import (
	"context"
	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/document"
	"tripfolio/server/internal/transport/httpapi/generated"
)

func (h *Handler) ListDocuments(ctx context.Context, req generated.ListDocumentsRequestObject) (generated.ListDocumentsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.documents == nil {
		return nil, notWired()
	}
	f := document.Filters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	if req.Params.ReservationId != nil {
		v := uuid.UUID(*req.Params.ReservationId)
		f.ReservationID = &v
	}
	page, err := h.documents.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListDocuments200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}
func (h *Handler) GetDocument(ctx context.Context, req generated.GetDocumentRequestObject) (generated.GetDocumentResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.documents == nil {
		return nil, notWired()
	}
	v, err := h.documents.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.DocumentId))
	if err != nil {
		return nil, err
	}
	tag := etag(v.Version)
	return generated.GetDocument200JSONResponse{Body: generated.DocumentResponse{Data: v}, Headers: generated.GetDocument200ResponseHeaders{ETag: &tag}}, nil
}
func (h *Handler) CreateDocument(ctx context.Context, req generated.CreateDocumentRequestObject) (generated.CreateDocumentResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.documents == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	p := document.Patch{}
	p.Title = &b.Title
	p.Notes = b.Notes
	p.AssetID = &b.AssetId
	p.ReservationIDSet, p.ReservationID = nullableUUID(b.ReservationId)
	cmd := document.CreateCommand{ID: uuid.UUID(b.Id), Patch: p}
	res, err := h.documents.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateDocument201JSONResponse{Data: res}, nil
}
func (h *Handler) UpdateDocument(ctx context.Context, req generated.UpdateDocumentRequestObject) (generated.UpdateDocumentResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.documents == nil {
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
	p := document.Patch{}
	p.Title = b.Title
	p.Notes = b.Notes
	p.AssetID = b.AssetId
	p.ReservationIDSet, p.ReservationID = nullableUUID(b.ReservationId)
	res, err := h.documents.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.DocumentId), base, p)
	if err != nil {
		return nil, err
	}
	return generated.UpdateDocument200JSONResponse{Data: res}, nil
}
func (h *Handler) DeleteDocument(ctx context.Context, req generated.DeleteDocumentRequestObject) (generated.DeleteDocumentResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.documents == nil {
		return nil, notWired()
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.documents.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.DocumentId), base)
	if err != nil {
		return nil, err
	}
	return generated.DeleteDocument200JSONResponse{Data: res}, nil
}
