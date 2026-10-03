package admin

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/backup"
)

type BackupStore interface {
	BackupSettings(context.Context, Session, Audit, *backup.Update, string) (backup.Settings, backup.Config, error)
	BackupRequest(context.Context, Session, Audit, *uuid.UUID) (backup.Run, error)
	BackupRuns(context.Context, Session, Audit, int) (backup.Page, error)
}

func (s *Service) WithBackups(store BackupStore, runner *backup.Service) *Service {
	s.backups, s.backupRunner = store, runner
	return s
}

func (s *Service) backupAudit(sess Session, action, reason string, info RequestInfo) (Audit, error) {
	a := s.event(&sess, action, "success", info)
	a.ResourceType, a.Reason = "database_backup", strings.TrimSpace(reason)
	if s.backups == nil || s.backupRunner == nil {
		return a, apperr.New(503, "BACKUP_UNAVAILABLE", "备份服务未启用")
	}
	if action != "backup.settings.read" && action != "backup.list" && (utf8.RuneCountInString(a.Reason) < 1 || utf8.RuneCountInString(a.Reason) > 500) {
		return a, apperr.BadRequest("MALFORMED_REQUEST", "请填写 1–500 字的操作原因")
	}
	return a, nil
}

func (s *Service) BackupSettings(ctx context.Context, sess Session, in *backup.Update, info RequestInfo) (result backup.Settings, err error) {
	action, reason := "backup.settings.read", ""
	if in != nil {
		action, reason = "backup.settings.update", in.Reason
	}
	a, err := s.backupAudit(sess, action, reason, info)
	defer func() {
		if err != nil {
			err = s.backupFailure(ctx, a, err)
		}
	}()
	if err != nil {
		return backup.Settings{}, err
	}
	sealed := ""
	if in != nil {
		if err = s.backupRunner.Validate(*in); err != nil {
			return backup.Settings{}, err
		}
		if in.Password != "" {
			sealed, err = s.backupRunner.Seal(in.Password)
			if err != nil {
				return backup.Settings{}, err
			}
		}
	}
	out, _, err := s.backups.BackupSettings(ctx, sess, a, in, sealed)
	out.Ready, out.Readiness = s.backupRunner.Readiness()
	return out, err
}

func (s *Service) BackupRequest(ctx context.Context, sess Session, reason string, retry *uuid.UUID, info RequestInfo) (result backup.Run, err error) {
	action := "backup.request"
	if retry != nil {
		action = "backup.retry"
	}
	a, err := s.backupAudit(sess, action, reason, info)
	defer func() {
		if err != nil {
			err = s.backupFailure(ctx, a, err)
		}
	}()
	if err != nil {
		return backup.Run{}, err
	}
	if ok, why := s.backupRunner.Readiness(); !ok {
		return backup.Run{}, apperr.New(503, "BACKUP_UNAVAILABLE", why)
	}
	return s.backups.BackupRequest(ctx, sess, a, retry)
}

func (s *Service) BackupRuns(ctx context.Context, sess Session, page int, info RequestInfo) (result backup.Page, err error) {
	a, err := s.backupAudit(sess, "backup.list", "", info)
	defer func() {
		if err != nil {
			err = s.backupFailure(ctx, a, err)
		}
	}()
	if err != nil {
		return backup.Page{}, err
	}
	if page < 1 || page > 10000 {
		return backup.Page{}, apperr.BadRequest("MALFORMED_REQUEST", "分页参数无效")
	}
	return s.backups.BackupRuns(ctx, sess, a, page)
}

func (s *Service) BackupProbe(ctx context.Context, sess Session, reason string, info RequestInfo) (err error) {
	a, err := s.backupAudit(sess, "backup.test", reason, info)
	defer func() {
		if err != nil {
			err = s.backupFailure(ctx, a, err)
		}
	}()
	if err != nil {
		return err
	}
	_, cfg, err := s.backups.BackupSettings(ctx, sess, a, nil, "")
	if err != nil {
		return err
	}
	err = s.backupRunner.Probe(ctx, cfg)
	result := "success"
	if err != nil {
		result = "failure"
	}
	if e := s.Record(ctx, &sess, "backup.test.result", result, info); e != nil {
		return e
	}
	return err
}

func (s *Service) backupFailure(ctx context.Context, a Audit, err error) error {
	a.Result, a.Details = "failure", nil
	if utf8.RuneCountInString(a.Reason) > 500 {
		a.Reason = ""
	}
	if auditErr := s.recordRead(ctx, a); auditErr != nil {
		return auditErr
	}
	return err
}
