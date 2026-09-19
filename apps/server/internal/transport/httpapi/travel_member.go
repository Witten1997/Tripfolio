package httpapi

import (
	"context"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/member"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 旅行成员处理器（接口设计 2.5）。只做参数转换与分发，规则在 member.Service。

// ListTripMembers 实现 GET /trips/{trip_id}/members。
func (h *Handler) ListTripMembers(ctx context.Context, req generated.ListTripMembersRequestObject) (generated.ListTripMembersResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.members == nil {
		return nil, notWired()
	}
	rows, err := h.members.List(ctx, a, uuid.UUID(req.TripId))
	if err != nil {
		return nil, err
	}
	return generated.ListTripMembers200JSONResponse{Data: rows}, nil
}

// SaveTripMembers 实现 PUT /trips/{trip_id}/members。
func (h *Handler) SaveTripMembers(ctx context.Context, req generated.SaveTripMembersRequestObject) (generated.SaveTripMembersResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.members == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	cmd := member.SaveCommand{Members: make([]member.Input, 0, len(req.Body.Members))}
	for _, m := range req.Body.Members {
		cmd.Members = append(cmd.Members, member.Input{ID: uuid.UUID(m.Id), Name: m.Name, SharePercent: m.SharePercent})
	}
	res, err := h.members.Save(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.SaveTripMembers200JSONResponse{Data: res}, nil
}
