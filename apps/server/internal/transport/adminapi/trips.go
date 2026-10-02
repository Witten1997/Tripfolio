package adminapi

import (
	"github.com/google/uuid"
	"net/http"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) AdminTrips(w http.ResponseWriter, r *http.Request, p generated.AdminTripsParams) {
	f := admin.TripFilter{AccountID: p.AccountId, Page: 1, PageSize: 20, Archived: "all", Trash: "exclude"}
	if p.Q != nil {
		f.Query = *p.Q
	}
	if p.Phase != nil {
		f.Phase = string(*p.Phase)
	}
	if p.Archived != nil {
		f.Archived = string(*p.Archived)
	}
	if p.Trash != nil {
		f.Trash = string(*p.Trash)
	}
	if p.DateFrom != nil {
		f.DateFrom = p.DateFrom.Format("2006-01-02")
	}
	if p.DateTo != nil {
		f.DateTo = p.DateTo.Format("2006-01-02")
	}
	if p.Page != nil {
		f.Page = *p.Page
	}
	if p.PageSize != nil {
		f.PageSize = *p.PageSize
	}
	result, err := h.options.Service.Trips(r.Context(), current(r), f, requestInfo(r))
	if err != nil {
		h.queryError(w, r, err)
		return
	}
	writeJSON(w, result)
}
func (h *Handler) AdminTrip(w http.ResponseWriter, r *http.Request, id uuid.UUID) {
	result, err := h.options.Service.Trip(r.Context(), current(r), id, requestInfo(r))
	if err != nil {
		h.queryError(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": result})
}
