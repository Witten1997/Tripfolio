package httpapi

import (
	"context"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/travel/todo"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// 待办处理器（接口设计 2.4）。只做参数转换与分发，规则在 todo.Service。

// ListTodos 实现 GET /trips/{trip_id}/todos。
func (h *Handler) ListTodos(ctx context.Context, req generated.ListTodosRequestObject) (generated.ListTodosResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.todos == nil {
		return nil, notWired()
	}
	f := todo.Filters{Limit: pageLimit(req.Params.Limit)}
	if req.Params.State != nil {
		f.State = string(*req.Params.State)
	}
	if req.Params.Cursor != nil {
		f.Cursor = *req.Params.Cursor
	}
	page, err := h.todos.List(ctx, a, uuid.UUID(req.TripId), f)
	if err != nil {
		return nil, err
	}
	return generated.ListTodos200JSONResponse{Items: page.Items, NextCursor: nextCursor(page.NextCursor)}, nil
}

// CreateTodo 实现 POST /trips/{trip_id}/todos。
func (h *Handler) CreateTodo(ctx context.Context, req generated.CreateTodoRequestObject) (generated.CreateTodoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.todos == nil {
		return nil, notWired()
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	b := req.Body
	cmd := todo.CreateCommand{ID: uuid.UUID(b.Id), Title: b.Title, Notes: b.Notes, Completed: b.Completed}
	_, cmd.DueOn = nullableString(b.DueOn)
	res, err := h.todos.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateTodo201JSONResponse{Data: res}, nil
}

// GetTodo 实现 GET /trips/{trip_id}/todos/{todo_id}。
func (h *Handler) GetTodo(ctx context.Context, req generated.GetTodoRequestObject) (generated.GetTodoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.todos == nil {
		return nil, notWired()
	}
	r, err := h.todos.Get(ctx, a, uuid.UUID(req.TripId), uuid.UUID(req.TodoId))
	if err != nil {
		return nil, err
	}
	tag := etag(r.Version)
	return generated.GetTodo200JSONResponse{Body: generated.TodoResponse{Data: r}, Headers: generated.GetTodo200ResponseHeaders{ETag: &tag}}, nil
}

// UpdateTodo 实现 PATCH /trips/{trip_id}/todos/{todo_id}。
func (h *Handler) UpdateTodo(ctx context.Context, req generated.UpdateTodoRequestObject) (generated.UpdateTodoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.todos == nil {
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
	patch := todo.Patch{Title: b.Title, Notes: b.Notes, Completed: b.Completed}
	patch.DueOnSet, patch.DueOn = nullableString(b.DueOn)
	res, err := h.todos.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.TodoId), base, patch)
	if err != nil {
		return nil, err
	}
	return generated.UpdateTodo200JSONResponse{Data: res}, nil
}

// DeleteTodo 实现 DELETE /trips/{trip_id}/todos/{todo_id}。
func (h *Handler) DeleteTodo(ctx context.Context, req generated.DeleteTodoRequestObject) (generated.DeleteTodoResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if h.todos == nil {
		return nil, notWired()
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.todos.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.TripId), uuid.UUID(req.TodoId), version)
	if err != nil {
		return nil, err
	}
	return generated.DeleteTodo200JSONResponse{Data: res}, nil
}
