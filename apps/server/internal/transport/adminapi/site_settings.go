package adminapi

import (
	"net/http"

	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) AdminSiteSettings(w http.ResponseWriter, r *http.Request) {
	out, err := h.options.Service.SiteSettings(r.Context(), current(r), nil, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": out})
}

func (h *Handler) AdminUpdateSiteSettings(w http.ResponseWriter, r *http.Request, _ generated.AdminUpdateSiteSettingsParams) {
	var in admin.SiteSettings
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.options.Service.SiteSettings(r.Context(), current(r), &in, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": out})
}
