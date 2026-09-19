package httpapi

import (
	"context"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 账目与统计处理器（接口设计 2.5）。只做参数转换与分发，规则在 finance.LedgerService 与 StatisticsService。

// nullableUUID 把 nullable UUID 字段转为 (set, value)：set 表示字段出现（含显式 null）。
func nullableUUID(n nullable.Nullable[openapi_types.UUID]) (bool, *uuid.UUID) {
	if !n.IsSpecified() {
		return false, nil
	}
	if n.IsNull() {
		return true, nil
	}
	v, _ := n.Get()
	id := uuid.UUID(v)
	return true, &id
}

func optionalUUID(p *openapi_types.UUID) *uuid.UUID {
	if p == nil {
		return nil
	}
	id := uuid.UUID(*p)
	return &id
}

func toUUIDs(ids *[]openapi_types.UUID) []uuid.UUID {
	if ids == nil {
		return nil
	}
	out := make([]uuid.UUID, len(*ids))
	for i, id := range *ids {
		out[i] = uuid.UUID(id)
	}
	return out
}

func splitModeString(m *generated.SplitMode) *string {
	if m == nil {
		return nil
	}
	s := string(*m)
	return &s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ListLedgerEntries 实现 GET /trips/{trip_id}/ledger-entries。
func (h *Handler) ListLedgerEntries(ctx context.Context, req generated.ListLedgerEntriesRequestObject) (generated.ListLedgerEntriesResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	p := req.Params
	f := finance.LedgerFilters{
		DateFrom: deref(p.DateFrom), DateTo: deref(p.DateTo), CategoryID: optionalUUID(p.CategoryId),
		RefundedEntryID: optionalUUID(p.RefundedEntryId), Limit: pageLimit(p.Limit), Cursor: deref(p.Cursor),
	}
	if p.Kind != nil {
		f.Kind = string(*p.Kind)
	}
	page, err := h.ledger.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListLedgerEntries200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

// CreateLedgerEntry 实现 POST /trips/{trip_id}/ledger-entries。
func (h *Handler) CreateLedgerEntry(ctx context.Context, req generated.CreateLedgerEntryRequestObject) (generated.CreateLedgerEntryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	cmd := finance.CreateLedgerCommand{
		ID: uuid.UUID(b.Id), Kind: string(b.Kind), Amount: b.Amount, CurrencyCode: b.CurrencyCode,
		CategoryID: uuid.UUID(b.CategoryId), OccurredOn: b.OccurredOn, Notes: b.Notes,
		AttachmentAssetIDs: toUUIDs(b.AttachmentAssetIds),
		PayerMemberID:      optionalUUID(b.PayerMemberId), SplitMode: splitModeString(b.SplitMode), ParticipantMemberIDs: toUUIDs(b.ParticipantMemberIds),
	}
	_, cmd.RefundedEntryID = nullableUUID(b.RefundedEntryId)
	res, err := h.ledger.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateLedgerEntry201JSONResponse{Data: res}, nil
}

// GetLedgerEntry 实现 GET /trips/{trip_id}/ledger-entries/{entry_id}。
func (h *Handler) GetLedgerEntry(ctx context.Context, req generated.GetLedgerEntryRequestObject) (generated.GetLedgerEntryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	r, err := h.ledger.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.EntryId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetLedgerEntry200JSONResponse{
		Body: generated.LedgerEntryResponse{Data: r}, Headers: generated.GetLedgerEntry200ResponseHeaders{ETag: &tag},
	}, nil
}

// UpdateLedgerEntry 实现 PATCH /trips/{trip_id}/ledger-entries/{entry_id}。
func (h *Handler) UpdateLedgerEntry(ctx context.Context, req generated.UpdateLedgerEntryRequestObject) (generated.UpdateLedgerEntryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := req.Body
	patch := finance.LedgerPatch{
		Amount: b.Amount, CurrencyCode: b.CurrencyCode, CategoryID: optionalUUID(b.CategoryId),
		OccurredOn: b.OccurredOn, Notes: b.Notes,
		PayerMemberID: optionalUUID(b.PayerMemberId), SplitMode: splitModeString(b.SplitMode),
	}
	if b.ParticipantMemberIds != nil {
		ids := toUUIDs(b.ParticipantMemberIds)
		patch.ParticipantMemberIDs = &ids
	}
	patch.RefundedSet, patch.RefundedEntryID = nullableUUID(b.RefundedEntryId)
	if b.AttachmentAssetIds != nil {
		ids := toUUIDs(b.AttachmentAssetIds)
		patch.AttachmentAssetIDs = &ids
	}
	res, err := h.ledger.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.EntryId), base, patch)
	if err != nil {
		return nil, err
	}
	return generated.UpdateLedgerEntry200JSONResponse{Data: res}, nil
}

// DeleteLedgerEntry 实现 DELETE /trips/{trip_id}/ledger-entries/{entry_id}。
func (h *Handler) DeleteLedgerEntry(ctx context.Context, req generated.DeleteLedgerEntryRequestObject) (generated.DeleteLedgerEntryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.ledger == nil {
		return nil, notWired()
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.ledger.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.EntryId), version)
	if err != nil {
		return nil, err
	}
	return generated.DeleteLedgerEntry200JSONResponse{Data: res}, nil
}

// GetTripSettlement 实现 GET /trips/{trip_id}/settlement。
func (h *Handler) GetTripSettlement(ctx context.Context, req generated.GetTripSettlementRequestObject) (generated.GetTripSettlementResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.settlement == nil {
		return nil, notWired()
	}
	s, err := h.settlement.Get(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.GetTripSettlement200JSONResponse{Data: s}, nil
}

// GetTripStatistics 实现 GET /trips/{trip_id}/statistics。
func (h *Handler) GetTripStatistics(ctx context.Context, req generated.GetTripStatisticsRequestObject) (generated.GetTripStatisticsResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.statistics == nil {
		return nil, notWired()
	}
	p := req.Params
	f := finance.StatisticsFilters{
		DateFrom: deref(p.DateFrom), DateTo: deref(p.DateTo), CategoryID: optionalUUID(p.CategoryId),
		DailyLimit: pageLimit(p.DailyLimit), DailyCursor: deref(p.DailyCursor),
	}
	s, err := h.statistics.Get(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.GetTripStatistics200JSONResponse{Data: s}, nil
}
