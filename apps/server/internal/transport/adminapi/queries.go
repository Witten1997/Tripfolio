package adminapi

import (
	"net/http"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) queryError(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := apperr.As(err); ok && e.Status == 400 {
		sess := current(r)
		h.reject(w, r, &sess, "request.invalid", err)
		return
	}
	h.fail(w, r, err)
}

func (h *Handler) AdminOverview(w http.ResponseWriter, r *http.Request) {
	result, err := h.options.Service.Overview(r.Context(), current(r), requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": result})
}

func (h *Handler) AdminUsers(w http.ResponseWriter, r *http.Request, p generated.AdminUsersParams) {
	filter := admin.UserFilter{Page: 1, PageSize: 20}
	if p.Q != nil {
		filter.Query = *p.Q
	}
	if p.Status != nil {
		filter.Status = string(*p.Status)
	}
	if p.Page != nil {
		filter.Page = *p.Page
	}
	if p.PageSize != nil {
		filter.PageSize = *p.PageSize
	}
	if p.RegisteredFrom != nil {
		filter.RegisteredFrom = p.RegisteredFrom.Format("2006-01-02")
	}
	if p.RegisteredTo != nil {
		filter.RegisteredTo = p.RegisteredTo.Format("2006-01-02")
	}
	result, err := h.options.Service.Users(r.Context(), current(r), filter, requestInfo(r))
	if err != nil {
		h.queryError(w, r, err)
		return
	}
	writeJSON(w, result)
}

func (h *Handler) AdminUser(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	result, err := h.options.Service.User(r.Context(), current(r), id, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": result})
}
