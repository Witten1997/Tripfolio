package adminapi

import (
	"net/http"

	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) AdminSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, generated.SetupStatus{Required: true})
}

func (h *Handler) AdminInitialize(w http.ResponseWriter, r *http.Request) {
	var body generated.SetupRequest
	if !h.decode(w, r, &body) {
		return
	}
	var nickname string
	if body.Nickname != nil {
		nickname = *body.Nickname
	}
	if err := h.options.Service.Initialize(r.Context(), admin.SetupInput{
		Mode: string(body.Mode), Email: string(body.Email), Nickname: nickname, Password: body.Password,
	}, requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
