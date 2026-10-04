package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/transport/httpapi/generated"
	"tripfolio/server/internal/transport/httpapi/middleware"
)

func (h *Handler) GetDeletionJob(ctx context.Context, req generated.GetDeletionJobRequestObject) (generated.GetDeletionJobResponseObject, error) {
	if h.deletions == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理服务未启用")
	}
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.deletions.GetTrip(ctx, a, req.JobId, false)
	if err != nil {
		return nil, err
	}
	return generated.GetDeletionJob200JSONResponse{Data: out}, nil
}
func (h *Handler) GetTripDeletionJob(ctx context.Context, req generated.GetTripDeletionJobRequestObject) (generated.GetTripDeletionJobResponseObject, error) {
	if h.deletions == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理服务未启用")
	}
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.deletions.GetTrip(ctx, a, req.TripId, true)
	if err != nil {
		return nil, err
	}
	return generated.GetTripDeletionJob200JSONResponse{Data: out}, nil
}
func (h *Handler) RenewDeletionReceipt(ctx context.Context, req generated.RenewDeletionReceiptRequestObject) (generated.RenewDeletionReceiptResponseObject, error) {
	if h.deletions == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理服务未启用")
	}
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	out, err := h.deletions.Renew(ctx, a, req.JobId)
	if err != nil {
		return nil, err
	}
	return generated.RenewDeletionReceipt200JSONResponse{Data: out}, nil
}
func (h *Handler) RequestAccountDeletion(ctx context.Context, req generated.RequestAccountDeletionRequestObject) (generated.RequestAccountDeletionResponseObject, error) {
	if h.deletions == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理服务未启用")
	}
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	out, err := h.deletions.Request(ctx, a, req.Params.IdempotencyKey, version, bool(req.Body.Confirm))
	if err != nil {
		return nil, err
	}
	return generated.RequestAccountDeletion202JSONResponse{Data: out}, nil
}
func (h *Handler) RetryDeletionJob(ctx context.Context, req generated.RetryDeletionJobRequestObject) (generated.RetryDeletionJobResponseObject, error) {
	if h.deletions == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理服务未启用")
	}
	a, err := mustActor(ctx)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, apperr.BadRequest("MALFORMED_REQUEST", "缺少请求正文")
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	out, err := h.deletions.RetryTrip(ctx, a, req.JobId, req.Params.IdempotencyKey, version, bool(req.Body.Confirm))
	if err != nil {
		return nil, err
	}
	return generated.RetryDeletionJob202JSONResponse{Data: out}, nil
}
func (h *Handler) GetAccountDeletion(ctx context.Context, req generated.GetAccountDeletionRequestObject) (generated.GetAccountDeletionResponseObject, error) {

	r := middleware.RequestFrom(ctx)
	token := ""
	if r != nil {
		scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if ok && strings.EqualFold(scheme, "Deletion") {
			token = strings.TrimSpace(value)
		}
	}
	if token == "" {
		return nil, apperr.Unauthorized("AUTH_REQUIRED", "需要注销查询凭证")
	}
	if h.deletions == nil {
		return nil, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "清理服务未启用")
	}
	out, err := h.deletions.GetAccount(ctx, req.JobId, token)
	if err != nil {
		return nil, err
	}
	return generated.GetAccountDeletion200JSONResponse{Data: out}, nil
}

func deletionRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1")
		relevant := strings.HasPrefix(path, "/account/deletion") || strings.HasPrefix(path, "/deletion-jobs/") || strings.HasSuffix(path, "/deletion-job")
		if relevant {
			w.Header().Set("Cache-Control", "no-store")
		}
		confirmation := r.Method == http.MethodPost && (path == "/account/deletion" || (strings.HasPrefix(path, "/deletion-jobs/") && strings.HasSuffix(path, "/retry")))
		if confirmation {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
			if err != nil {
				WriteProblem(w, r, 413, "REQUEST_TOO_LARGE", "")
				return
			}
			if err = validateContentObject(body, contentBodyRule{fields: map[string]bool{"confirm": false}, required: []string{"confirm"}}); err != nil {
				if e, ok := apperr.As(err); ok {
					writeAppError(w, r, e)
				}
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		next.ServeHTTP(w, r)
	})
}
