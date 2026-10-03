package adminapi

import (
	"github.com/google/uuid"
	"net/http"
	"tripfolio/server/internal/modules/backup"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) AdminBackupSettings(w http.ResponseWriter, r *http.Request) {
	out, err := h.options.Service.BackupSettings(r.Context(), current(r), nil, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": out})
}
func (h *Handler) AdminUpdateBackupSettings(w http.ResponseWriter, r *http.Request, _ generated.AdminUpdateBackupSettingsParams) {
	var in backup.Update
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.options.Service.BackupSettings(r.Context(), current(r), &in, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": out})
}
func (h *Handler) AdminTestBackupStorage(w http.ResponseWriter, r *http.Request, _ generated.AdminTestBackupStorageParams) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	if err := h.options.Service.BackupProbe(r.Context(), current(r), in.Reason, requestInfo(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) AdminBackupRuns(w http.ResponseWriter, r *http.Request, p generated.AdminBackupRunsParams) {
	page := 1
	if p.Page != nil {
		page = *p.Page
	}
	out, err := h.options.Service.BackupRuns(r.Context(), current(r), page, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, out)
}
func (h *Handler) requestBackup(w http.ResponseWriter, r *http.Request, id *uuid.UUID) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !h.decode(w, r, &in) {
		return
	}
	out, err := h.options.Service.BackupRequest(r.Context(), current(r), in.Reason, id, requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"data": out})
}
func (h *Handler) AdminRequestBackup(w http.ResponseWriter, r *http.Request, _ generated.AdminRequestBackupParams) {
	h.requestBackup(w, r, nil)
}
func (h *Handler) AdminRetryBackup(w http.ResponseWriter, r *http.Request, id uuid.UUID, _ generated.AdminRetryBackupParams) {
	h.requestBackup(w, r, &id)
}
