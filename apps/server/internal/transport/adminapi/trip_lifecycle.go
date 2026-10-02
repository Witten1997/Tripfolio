package adminapi

import (
	"github.com/google/uuid"
	"net/http"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) AdminMutateTrip(w http.ResponseWriter, r *http.Request, id uuid.UUID, _ generated.AdminMutateTripParams) {
	var body admin.TripMutation
	if !h.decode(w, r, &body) {
		return
	}
	result, err := h.options.Service.MutateTrip(r.Context(), current(r), id, body, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": result})
}
