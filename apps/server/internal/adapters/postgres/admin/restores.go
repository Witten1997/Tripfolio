package adminpg

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/modules/backup"
)

func (s *BackupStore) RequestRestore(ctx context.Context, sess admin.Session, id uuid.UUID, secret string, remote *backup.RemoteRestore, a admin.Audit) (backup.RestoreJob, error) {
	var job backup.RestoreJob
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return job, err
	}
	defer tx.Rollback(ctx)
	if err = lockControl(ctx, tx, sess, sess.AccountID, true); err != nil {
		return job, err
	}
	settings, cfg, err := backupSettings(ctx, tx)
	if err != nil {
		return job, err
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_backup_runs WHERE state NOT IN ('succeeded','failed')) OR EXISTS(SELECT 1 FROM tripfolio_restore.jobs WHERE state IN ('queued','preparing','restoring'))`).Scan(&active); err != nil {
		return job, err
	}
	if active {
		return job, apperr.Conflicted("BACKUP_BUSY", "已有备份或恢复任务，请等待完成")
	}
	var sourceConfig backup.Config
	var sourceManifest backup.Manifest
	if remote != nil {
		if settings.Version != remote.SettingsVersion || cfg.DatabaseID != remote.Config.DatabaseID {
			return job, apperr.Conflicted("VERSION_CONFLICT", "WebDAV 设置已变更，请重新查找备份")
		}
		// The target identity stays local; only the source directory comes from WebDAV.
		sourceConfig, sourceManifest = cfg, remote.Manifest
		sourceConfig.Destination = remote.Config.Destination
	} else {
		r, err := scanBackup(tx.QueryRow(ctx, `SELECT `+backupColumns+` FROM admin_backup_runs WHERE id=$1 FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return job, apperr.NotFound()
		}
		if err != nil {
			return job, err
		}
		if r.State != "succeeded" || r.RemoteDeletedAt != nil || r.Config.Destination != cfg.Destination {
			return job, apperr.Conflicted("RESTORE_UNAVAILABLE", "只能恢复当前 WebDAV 目标中尚未清理的成功备份")
		}
		sourceConfig, sourceManifest = cfg, r.Manifest
	}
	job.ID, job.BackupID, job.State = uuid.New(), id, "queued"
	rawToken := make([]byte, 32)
	if _, err = rand.Read(rawToken); err != nil {
		return job, err
	}
	job.Token = base64.RawURLEncoding.EncodeToString(rawToken)
	hash := sha256.Sum256([]byte(job.Token))
	config, _ := json.Marshal(sourceConfig)
	manifest, _ := json.Marshal(sourceManifest)
	err = tx.QueryRow(ctx, `INSERT INTO tripfolio_restore.jobs(id,backup_id,actor_id,state,token_hash,secret,config,manifest) VALUES($1,$2,$3,'queued',$4,$5,$6,$7) RETURNING created_at`, job.ID, id, sess.AccountID, hash[:], secret, config, manifest).Scan(&job.CreatedAt)
	if err != nil {
		return job, err
	}
	a.ResourceID = &id
	a.Details = map[string]any{"restore_id": job.ID, "snapshot_at": sourceManifest.SnapshotAt, "destination": sourceConfig.Destination, "remote": remote != nil}
	return job, auditedControl(ctx, tx, a)
}

const restoreColumns = `id,backup_id,state,created_at,started_at,finished_at,error_code,secret,config,manifest`

func scanRestore(row backupScanner) (backup.RestoreJob, error) {
	var job backup.RestoreJob
	var cfg, manifest []byte
	err := row.Scan(&job.ID, &job.BackupID, &job.State, &job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.ErrorCode, &job.Secret, &cfg, &manifest)
	if err != nil {
		return job, err
	}
	if err = json.Unmarshal(cfg, &job.Config); err != nil {
		return job, err
	}
	err = json.Unmarshal(manifest, &job.Manifest)
	job.Message = backup.RestoreSummary(job.ErrorCode)
	return job, err
}

func (s *BackupStore) PendingRestore(ctx context.Context) (*backup.RestoreJob, error) {
	job, err := scanRestore(s.pool.QueryRow(ctx, `SELECT `+restoreColumns+` FROM tripfolio_restore.jobs WHERE state IN ('queued','preparing','restoring') ORDER BY created_at LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &job, err
}

func (s *BackupStore) RestoreStatus(ctx context.Context, id uuid.UUID, hash []byte) (backup.RestoreJob, error) {
	job, err := scanRestore(s.pool.QueryRow(ctx, `SELECT `+restoreColumns+` FROM tripfolio_restore.jobs WHERE id=$1 AND token_hash=$2 AND created_at>clock_timestamp()-interval '24 hours'`, id, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return job, apperr.NotFound()
	}
	return job, err
}

func (s *BackupStore) RestoreProgress(ctx context.Context, id uuid.UUID, state, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE tripfolio_restore.jobs SET state=$2,error_code=$3,started_at=coalesce(started_at,clock_timestamp()),finished_at=CASE WHEN $2='failed' THEN clock_timestamp() ELSE NULL END,secret=CASE WHEN $2='failed' THEN '' ELSE secret END WHERE id=$1 AND state NOT IN ('succeeded','failed')`, id, state, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	if state == "failed" {
		a := backupEvent(id, "backup.restore.failed", "failure")
		a.Details = map[string]any{"error_code": code}
		return auditedControl(ctx, tx, a)
	}
	return tx.Commit(ctx)
}

func (s *BackupStore) ResetConnections() { s.pool.Reset() }
