package httpapi

import (
	"context"
	"net/http"
	"net/url"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/paging"
	"tripfolio/server/internal/modules/collectionbaseline"
	"tripfolio/server/internal/transport/httpapi/generated"
)

// Validate the raw query before generated binding can discard unknown or repeated values.
func collectionBaselineRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/collection-baselines" {
			next.ServeHTTP(w, r)
			return
		}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeAppError(w, r, apperr.BadRequest("MALFORMED_REQUEST", "查询参数格式无效"))
			return
		}
		for key, values := range query {
			if (key != "kind" && key != "scope_id" && key != "limit" && key != "cursor") || len(values) != 1 {
				writeAppError(w, r, apperr.BadRequest("MALFORMED_REQUEST", "查询参数未知或重复"))
				return
			}
			if values[0] == "" {
				if key == "cursor" {
					writeAppError(w, r, paging.InvalidCursor())
				} else {
					writeAppError(w, r, apperr.BadRequest("MALFORMED_REQUEST", "查询参数不能为空"))
				}
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) ListCollectionBaselines(ctx context.Context, req generated.ListCollectionBaselinesRequestObject) (generated.ListCollectionBaselinesResponseObject, error) {
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	limit := paging.DefaultLimit
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
		if limit < 1 || limit > paging.MaxLimit {
			return nil, apperr.Validation(apperr.Field("limit", "INVALID", "limit 须在 1–100 之间"))
		}
	}
	if h.collectionBaselines == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "集合读取暂不可用")
	}
	q := collectionbaseline.Query{Kind: string(req.Params.Kind), ScopeID: req.Params.ScopeId, Limit: limit}
	if req.Params.Cursor != nil {
		q.Cursor = *req.Params.Cursor
	}
	page, err := h.collectionBaselines.List(ctx, a, q)
	if err != nil {
		return nil, err
	}
	return generated.ListCollectionBaselines200JSONResponse(page), nil
}
