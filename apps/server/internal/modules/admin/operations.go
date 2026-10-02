package admin

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
)

type AuditFilter struct {
	AsOf                             time.Time
	ActorID, SubjectID               *uuid.UUID
	Action, Result, DateFrom, DateTo string
	Page, PageSize                   int
}

type AuditEntry struct {
	ID           uuid.UUID  `json:"id"`
	ActorID      *uuid.UUID `json:"actor_account_id"`
	SubjectID    *uuid.UUID `json:"subject_account_id"`
	ResourceID   *uuid.UUID `json:"resource_id"`
	Action       string     `json:"action"`
	ResourceType string     `json:"resource_type"`
	Result       string     `json:"result"`
	RequestID    string     `json:"request_id"`
	SourceIP     string     `json:"source_ip"`
	OccurredAt   time.Time  `json:"occurred_at"`
	Summary      string     `json:"summary"`
	ResultCount  *int64     `json:"result_count"`
}

type AuditPage struct {
	AsOf     time.Time    `json:"as_of"`
	Data     []AuditEntry `json:"data"`
	Total    int64        `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
}

type ComponentStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type JobCounts struct {
	Running         int64 `json:"running"`
	Pending         int64 `json:"pending"`
	Retryable       int64 `json:"retryable"`
	Discarded       int64 `json:"discarded"`
	DeletionPending int64 `json:"deletion_pending"`
	DeletionFailed  int64 `json:"deletion_failed"`
}

type RuntimeStatus struct {
	Status     string            `json:"status"`
	StartedAt  time.Time         `json:"started_at"`
	AsOf       time.Time         `json:"as_of"`
	Components []ComponentStatus `json:"components"`
	Jobs       JobCounts         `json:"jobs"`
}

type JobFilter struct {
	State          string
	Page, PageSize int
}

type Job struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	State        string     `json:"state"`
	Attempt      int        `json:"attempt"`
	MaxAttempts  int        `json:"max_attempts"`
	CreatedAt    time.Time  `json:"created_at"`
	AttemptedAt  *time.Time `json:"attempted_at"`
	ScheduledAt  time.Time  `json:"scheduled_at"`
	ErrorSummary string     `json:"error_summary"`
}

type JobPage struct {
	Data     []Job `json:"data"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
}

type DeletionJob struct {
	Retryable      bool       `json:"retryable"`
	ID             uuid.UUID  `json:"id"`
	OwnerID        uuid.UUID  `json:"owner_account_id"`
	TripID         *uuid.UUID `json:"target_trip_id"`
	Scope          string     `json:"scope"`
	Status         string     `json:"status"`
	Stage          string     `json:"stage"`
	ProcessedItems int64      `json:"processed_items"`
	TotalItems     *int64     `json:"total_items"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	ErrorSummary   string     `json:"error_summary"`
}

type DeletionJobPage struct {
	Data     []DeletionJob `json:"data"`
	Total    int64         `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

func auditAction(value string) string {
	switch value {
	case "trip.edit", "trip.archive", "trip.trash", "trip.restore", "trip.purge", "trip.retry", "trip.purge.start", "trip.purge.complete", "trip.purge.failed", "trip.invalid", "login", "login.rate_limited", "authentication", "request.invalid", "request.origin", "request.csrf",
		"session.current", "session.list", "session.revoke", "reauthenticate", "reauthenticate.rate_limited",
		"principal.grant", "principal.revoke", "setup.initialize", "overview.read", "user.list", "user.read", "trip.list", "trip.read",
		"user.ban", "user.unban", "user.force-logout", "trip.sharing.read", "trip.sharing.restrict", "trip.sharing.release",
		"audit.list", "runtime.read", "job.list", "deletion_job.list":
		return value
	default:
		return "unknown"
	}
}

func (s *Service) Audits(ctx context.Context, sess Session, f AuditFilter, info RequestInfo) (AuditPage, error) {
	now := s.clock.Now()
	if f.AsOf.IsZero() {
		f.AsOf = now
	}
	if f.AsOf.After(now) {
		return AuditPage{}, apperr.BadRequest("MALFORMED_REQUEST", "审计查询时间不能晚于当前时间")
	}
	f.Action = strings.TrimSpace(f.Action)
	if !validPage(f.Page, f.PageSize) || (f.Action != "" && auditAction(f.Action) == "unknown") || (f.Result != "" && f.Result != "success" && f.Result != "failure" && f.Result != "denied") {
		return AuditPage{}, apperr.BadRequest("MALFORMED_REQUEST", "审计筛选条件或分页参数无效")
	}
	for _, date := range []string{f.DateFrom, f.DateTo} {
		if date != "" {
			if _, err := time.Parse(time.DateOnly, date); err != nil {
				return AuditPage{}, apperr.BadRequest("MALFORMED_REQUEST", "审计日期无效")
			}
		}
	}
	if f.DateFrom != "" && f.DateTo != "" && f.DateFrom > f.DateTo {
		return AuditPage{}, apperr.BadRequest("MALFORMED_REQUEST", "起始日期不能晚于结束日期")
	}
	out, err := s.store.Audits(ctx, f)
	if err != nil {
		return AuditPage{}, apperr.Internal(err)
	}
	ids := make([]string, 0, len(out.Data))
	for i := range out.Data {
		entry := &out.Data[i]
		ids = append(ids, entry.ID.String())
		entry.Action = auditAction(entry.Action)
		switch entry.ResourceType {
		case "", "account", "trip", "admin_session", "admin_principal", "admin_audit", "job", "deletion_job":
		default:
			entry.ResourceType = "unknown"
		}
		// 自定义请求编号与浏览器标识可能由客户端携带秘密；只展示标准请求编号和有效 IP。
		if _, err := uuid.Parse(strings.TrimPrefix(entry.RequestID, "req_")); err != nil {
			entry.RequestID = ""
		}
		if net.ParseIP(entry.SourceIP) == nil {
			entry.SourceIP = ""
		}
		entry.Summary = ""
		if entry.ResultCount != nil {
			entry.Summary = fmt.Sprintf("查询返回 %d 条记录", *entry.ResultCount)
		}
		if entry.Action == "trip.read" {
			entry.Summary = "查看旅行概况"
		}
	}
	a := s.event(&sess, "audit.list", "success", info)
	a.SubjectID, a.ResourceType = f.SubjectID, "admin_audit"
	a.Details = map[string]any{"actor_account_id": f.ActorID, "subject_account_id": f.SubjectID, "action": f.Action, "result": f.Result, "date_from": f.DateFrom, "date_to": f.DateTo, "page": f.Page, "page_size": f.PageSize, "result_count": len(ids), "resource_ids": ids}
	a.Details["as_of"] = f.AsOf
	if err = s.recordQuery(ctx, a); err != nil {
		return AuditPage{}, err
	}
	return out, nil
}

func (s *Service) recordQuery(ctx context.Context, a Audit) error {
	if err := s.store.Audit(ctx, a); err != nil {
		return apperr.New(503, "ADMIN_AUDIT_UNAVAILABLE", "操作记录暂不可用，请稍后重试").WithCause(err)
	}
	return nil
}

func (s *Service) Runtime(ctx context.Context, sess Session, status RuntimeStatus, info RequestInfo) (RuntimeStatus, error) {
	counts, err := s.store.JobCounts(ctx)
	if err != nil {
		return RuntimeStatus{}, apperr.Internal(err)
	}
	status.Jobs, status.AsOf = counts, s.clock.Now()
	if err := s.Record(ctx, &sess, "runtime.read", "success", info); err != nil {
		return RuntimeStatus{}, err
	}
	return status, nil
}

// 仅输出固定错误类别，绝不回传原始异常、URL、堆栈或参数。
func errorSummary(raw string) string {
	switch raw {
	case "UPLOAD_AUTHORIZATION_ACTIVE":
		return "正在等待已有上传授权到期，随后继续清理"
	case "OBJECTSTORE_UNAVAILABLE":
		return "对象存储未配置，清理尚未执行"
	case "OBJECT_LIST_FAILED", "OBJECT_DELETE_FAILED", "OBJECT_VERIFY_FAILED", "OBJECTS_REMAIN":
		return "对象存储清理或校验失败，可在恢复依赖后重试"
	case "OBJECT_SCOPE_MISMATCH", "PURGE_SCOPE_MISMATCH":
		return "清理范围校验失败，需要排查关联关系"
	case "ADMIN_AUDIT_UNAVAILABLE":
		return "审计暂不可写，清理未完成"
	case "PURGE_ROWS_FAILED", "PURGE_PREPARE_FAILED", "PURGE_STATE_UNAVAILABLE":
		return "数据库清理未完成，可在排查后重试"
	}

	if raw == "" {
		return ""
	}
	raw = strings.ToLower(raw)
	switch {
	case strings.Contains(raw, "timeout"), strings.Contains(raw, "deadline exceeded"):
		return "任务执行超时"
	case strings.Contains(raw, "context canceled"), strings.Contains(raw, "cancelled"):
		return "任务执行被中断"
	case strings.Contains(raw, "connection refused"), strings.Contains(raw, "no such host"), strings.Contains(raw, "connection reset"):
		return "无法连接依赖服务"
	case strings.Contains(raw, "unauthorized"), strings.Contains(raw, "access denied"), strings.Contains(raw, "forbidden"):
		return "依赖服务拒绝访问"
	default:
		return "任务执行失败，详细信息已隐藏"
	}
}

func (s *Service) Jobs(ctx context.Context, sess Session, f JobFilter, info RequestInfo) (JobPage, error) {
	if !validPage(f.Page, f.PageSize) || (f.State != "" && f.State != "retryable" && f.State != "discarded") {
		return JobPage{}, apperr.BadRequest("MALFORMED_REQUEST", "失败任务筛选条件无效")
	}
	out, err := s.store.Jobs(ctx, f)
	if err != nil {
		return JobPage{}, apperr.Internal(err)
	}
	ids := make([]string, 0, len(out.Data))
	for i := range out.Data {
		job := &out.Data[i]
		ids = append(ids, job.ID)
		switch job.Kind {
		case "ping", "trip_purge", "asset_verify", "route_recalculate":
		default:
			job.Kind = "unknown"
		}
		job.ErrorSummary = errorSummary(job.ErrorSummary)
	}
	if err := s.recordJobQuery(ctx, sess, "job.list", "job", f, ids, nil, info); err != nil {
		return JobPage{}, err
	}
	return out, nil
}

func (s *Service) DeletionJobs(ctx context.Context, sess Session, f JobFilter, info RequestInfo) (DeletionJobPage, error) {
	if !validPage(f.Page, f.PageSize) || (f.State != "" && f.State != "queued" && f.State != "running" && f.State != "completed" && f.State != "failed") {
		return DeletionJobPage{}, apperr.BadRequest("MALFORMED_REQUEST", "清理任务筛选条件无效")
	}
	out, err := s.store.DeletionJobs(ctx, f)
	if err != nil {
		return DeletionJobPage{}, apperr.Internal(err)
	}
	ids, owners := make([]string, 0, len(out.Data)), make([]string, 0, len(out.Data))
	for i := range out.Data {
		job := &out.Data[i]
		ids, owners = append(ids, job.ID.String()), append(owners, job.OwnerID.String())
		job.ErrorSummary = errorSummary(job.ErrorSummary)
	}
	if err := s.recordJobQuery(ctx, sess, "deletion_job.list", "deletion_job", f, ids, owners, info); err != nil {
		return DeletionJobPage{}, err
	}
	return out, nil
}

func (s *Service) recordJobQuery(ctx context.Context, sess Session, action, resource string, f JobFilter, ids, owners []string, info RequestInfo) error {
	a := s.event(&sess, action, "success", info)
	a.SubjectID, a.ResourceType = nil, resource
	a.Details = map[string]any{"state": f.State, "page": f.Page, "page_size": f.PageSize, "result_count": len(ids), "resource_ids": ids, "owner_ids": owners}
	return s.recordQuery(ctx, a)
}
