package httpapi

import (
	"context"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/finance"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// ListExpenseCategories 实现 GET /expense-categories。
func (h *Handler) ListExpenseCategories(ctx context.Context, _ generated.ListExpenseCategoriesRequestObject) (generated.ListExpenseCategoriesResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	items, err := h.categories.List(ctx, a)
	if err != nil {
		return nil, err
	}
	return generated.ListExpenseCategories200JSONResponse{Data: items}, nil
}

// GetExpenseCategory 实现 GET /expense-categories/{category_id}。
func (h *Handler) GetExpenseCategory(ctx context.Context, req generated.GetExpenseCategoryRequestObject) (generated.GetExpenseCategoryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	c, err := h.categories.Get(ctx, a, uuid.UUID(req.CategoryId))
	if err != nil {
		return nil, err
	}
	tag := etag(c.Version)
	return generated.GetExpenseCategory200JSONResponse{
		Body: generated.ExpenseCategoryResponse{Data: c}, Headers: generated.GetExpenseCategory200ResponseHeaders{ETag: &tag},
	}, nil
}

// CreateExpenseCategory 实现 POST /expense-categories。
func (h *Handler) CreateExpenseCategory(ctx context.Context, req generated.CreateExpenseCategoryRequestObject) (generated.CreateExpenseCategoryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	cmd := finance.CreateCategoryCommand{ID: uuid.UUID(req.Body.Id), Name: req.Body.Name}
	if req.Body.Icon.IsSpecified() && !req.Body.Icon.IsNull() {
		icon, _ := req.Body.Icon.Get()
		cmd.Icon = &icon
	}
	if req.Body.SortOrder != nil {
		so := int32(*req.Body.SortOrder)
		cmd.SortOrder = &so
	}
	res, err := h.categories.Create(ctx, a, uuid.UUID(req.Params.IdempotencyKey), cmd)
	if err != nil {
		return nil, err
	}
	return generated.CreateExpenseCategory201JSONResponse{Data: res}, nil
}

// UpdateExpenseCategory 实现 PATCH /expense-categories/{category_id}。
func (h *Handler) UpdateExpenseCategory(ctx context.Context, req generated.UpdateExpenseCategoryRequestObject) (generated.UpdateExpenseCategoryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	base, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	patch := finance.CategoryPatch{Name: req.Body.Name}
	if req.Body.Icon.IsSpecified() {
		patch.IconSet = true
		if !req.Body.Icon.IsNull() {
			icon, _ := req.Body.Icon.Get()
			patch.Icon = &icon
		}
	}
	if req.Body.SortOrder != nil {
		so := int32(*req.Body.SortOrder)
		patch.SortOrder = &so
	}
	res, err := h.categories.Update(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.CategoryId), base, patch)
	if err != nil {
		return nil, err
	}
	return generated.UpdateExpenseCategory200JSONResponse{Data: res}, nil
}

// DeleteExpenseCategory 实现 DELETE /expense-categories/{category_id}。
func (h *Handler) DeleteExpenseCategory(ctx context.Context, req generated.DeleteExpenseCategoryRequestObject) (generated.DeleteExpenseCategoryResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	res, err := h.categories.Delete(ctx, a, uuid.UUID(req.Params.IdempotencyKey), uuid.UUID(req.CategoryId), version)
	if err != nil {
		return nil, err
	}
	return generated.DeleteExpenseCategory200JSONResponse{Data: res}, nil
}
