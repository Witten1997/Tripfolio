package adminapi

import (
	"github.com/google/uuid"
	"net/http"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) userControl(w http.ResponseWriter, r *http.Request, id uuid.UUID, action string) {
	var in generated.ControlReason
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.options.Service.ControlUser(r.Context(), current(r), id, action, in.Reason, requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	if current(r).AccountID == id {
		h.setCookie(w, "", current(r).CreatedAt)
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) AdminBanUser(w http.ResponseWriter, r *http.Request, id generated.AccountID, _ generated.AdminBanUserParams) {
	h.userControl(w, r, id, "ban")
}
func (h *Handler) AdminUnbanUser(w http.ResponseWriter, r *http.Request, id generated.AccountID, _ generated.AdminUnbanUserParams) {
	h.userControl(w, r, id, "unban")
}
func (h *Handler) AdminForceLogoutUser(w http.ResponseWriter, r *http.Request, id generated.AccountID, _ generated.AdminForceLogoutUserParams) {
	h.userControl(w, r, id, "force-logout")
}

func (h *Handler) AdminSharingRestriction(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	result, err := h.options.Service.SharingRestriction(r.Context(), current(r), id, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": result})
}
func (h *Handler) AdminControlSharing(w http.ResponseWriter, r *http.Request, id uuid.UUID, _ generated.AdminControlSharingParams) {
	var in struct {
		Reason     string `json:"reason"`
		Restricted *bool  `json:"restricted"`
		Version    *int64 `json:"version"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if in.Restricted == nil || in.Version == nil {
		sess := current(r)
		h.reject(w, r, &sess, "trip.sharing.invalid", apperr.BadRequest("MALFORMED_REQUEST", "分享限制状态和版本必填"))
		return
	}
	result, err := h.options.Service.ControlSharing(r.Context(), current(r), id, admin.SharingControl{Restricted: *in.Restricted, Version: *in.Version, Reason: in.Reason}, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": result})
}
