package adminapi

import (
	"net/http"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/transport/adminapi/generated"
)

func (h *Handler) AdminAudits(w http.ResponseWriter, r *http.Request, p generated.AdminAuditsParams) {
	f := admin.AuditFilter{ActorID: p.ActorAccountId, SubjectID: p.SubjectAccountId, Page: 1, PageSize: 20}
	if p.AsOf != nil {
		f.AsOf = *p.AsOf
	}
	if p.Action != nil {
		f.Action = *p.Action
	}
	if p.Result != nil {
		f.Result = string(*p.Result)
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
	out, err := h.options.Service.Audits(r.Context(), current(r), f, requestInfo(r))
	if err != nil {
		h.queryError(w, r, err)
		return
	}
	writeJSON(w, out)
}

func (h *Handler) AdminRuntime(w http.ResponseWriter, r *http.Request) {
	if h.options.Runtime == nil {
		h.fail(w, r, apperr.New(503, "DEPENDENCY_UNAVAILABLE", "运行状态暂不可用"))
		return
	}
	out, err := h.options.Service.Runtime(r.Context(), current(r), h.options.Runtime(r.Context()), requestInfo(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, map[string]any{"data": out})
}

func (h *Handler) AdminJobs(w http.ResponseWriter, r *http.Request, p generated.AdminJobsParams) {
	f := admin.JobFilter{Page: 1, PageSize: 20}
	if p.State != nil {
		f.State = string(*p.State)
	}
	if p.Page != nil {
		f.Page = *p.Page
	}
	if p.PageSize != nil {
		f.PageSize = *p.PageSize
	}
	out, err := h.options.Service.Jobs(r.Context(), current(r), f, requestInfo(r))
	if err != nil {
		h.queryError(w, r, err)
		return
	}
	writeJSON(w, out)
}

func (h *Handler) AdminDeletionJobs(w http.ResponseWriter, r *http.Request, p generated.AdminDeletionJobsParams) {
	f := admin.JobFilter{Page: 1, PageSize: 20}
	if p.State != nil {
		f.State = string(*p.State)
	}
	if p.Page != nil {
		f.Page = *p.Page
	}
	if p.PageSize != nil {
		f.PageSize = *p.PageSize
	}
	out, err := h.options.Service.DeletionJobs(r.Context(), current(r), f, requestInfo(r))
	if err != nil {
		h.queryError(w, r, err)
		return
	}
	writeJSON(w, out)
}
