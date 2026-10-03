package adminpg

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/modules/admin"
	"tripfolio/server/internal/modules/backup"
)

type BackupStore struct {
	pool  *pgxpool.Pool
	queue *river.Client[pgx.Tx]
}

func NewBackupStore(pool *pgxpool.Pool, queue *river.Client[pgx.Tx]) *BackupStore {
	return &BackupStore{pool, queue}
}

func backupSettings(ctx context.Context, tx pgx.Tx) (backup.Settings, backup.Config, error) {
	var out backup.Settings
	var cfg backup.Config
	var raw []byte
	// A restored archive excludes destination credentials and schedules. Recreate a disabled setting.
	_, err := tx.Exec(ctx, `INSERT INTO admin_backup_settings(id,config) VALUES(1,'{"enabled":false,"time":"03:00","retain":7,"url":"","username":"","secret":"","destination":""}') ON CONFLICT DO NOTHING`)
	if err != nil {
		return out, cfg, err
	}
	var databaseID string
	err = tx.QueryRow(ctx, `SELECT config,version,next_at,database_id::text FROM admin_backup_settings WHERE id=1 FOR UPDATE`).Scan(&raw, &out.Version, &out.NextAt, &databaseID)
	if err != nil {
		return out, cfg, err
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return out, cfg, err
	}
	cfg.DatabaseID = databaseID
	out.Enabled, out.Time, out.Retain, out.URL, out.Username, out.PasswordSet = cfg.Enabled, cfg.Time, cfg.Retain, cfg.URL, cfg.Username, cfg.Secret != ""
	return out, cfg, nil
}

func backupConflict(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) && p.Code == "23505" {
		return apperr.Conflicted("BACKUP_BUSY", "已有备份正在排队或执行")
	}
	return err
}

func (s *BackupStore) BackupSettings(ctx context.Context, sess admin.Session, a admin.Audit, in *backup.Update, sealed string) (backup.Settings, backup.Config, error) {
	var out backup.Settings
	var cfg backup.Config
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, cfg, err
	}
	defer tx.Rollback(ctx)
	if err = lockControl(ctx, tx, sess, sess.AccountID, false); err != nil {
		return out, cfg, err
	}
	out, cfg, err = backupSettings(ctx, tx)
	if err != nil {
		return out, cfg, err
	}
	if in != nil {
		if out.Version != in.Version {
			return out, cfg, apperr.Conflicted("VERSION_CONFLICT", "设置已变更，请刷新后重试")
		}
		changed := cfg.URL != in.URL || cfg.Username != in.Username
		if (changed || cfg.Secret == "") && sealed == "" {
			return out, cfg, apperr.BadRequest("BACKUP_PASSWORD_REQUIRED", "首次配置或更换目标时请填写 WebDAV 密码")
		}
		if changed || cfg.Destination == "" {
			cfg.Destination = uuid.NewString()
		}
		cfg.Enabled, cfg.Time, cfg.Retain, cfg.URL, cfg.Username = in.Enabled, in.Time, in.Retain, in.URL, in.Username
		if sealed != "" {
			cfg.Secret = sealed
		}
		raw, e := json.Marshal(cfg)
		if e != nil {
			return out, cfg, e
		}
		var next *time.Time
		if cfg.Enabled {
			n := backup.Next(time.Now(), cfg.Time)
			next = &n
		}
		_, err = tx.Exec(ctx, `UPDATE admin_backup_settings SET config=$1,version=version+1,next_at=$2,updated_at=clock_timestamp() WHERE id=1`, raw, next)
		if err != nil {
			return out, cfg, err
		}
		a.Details = map[string]any{"enabled": cfg.Enabled, "retain": cfg.Retain, "destination_changed": changed, "password_changed": sealed != ""}
		out, cfg, err = backupSettings(ctx, tx)
		if err != nil {
			return out, cfg, err
		}
	}
	return out, cfg, auditedControl(ctx, tx, a)
}

func (s *BackupStore) enqueue(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := s.queue.InsertTx(ctx, tx, backup.Args{ID: id}, &river.InsertOpts{Queue: "backup", MaxAttempts: 3})
	return err
}

func (s *BackupStore) BackupRequest(ctx context.Context, sess admin.Session, a admin.Audit, retry *uuid.UUID) (out backup.Run, err error) {
	defer func() { err = backupConflict(err) }()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockControl(ctx, tx, sess, sess.AccountID, retry != nil); err != nil {
		return out, err
	}
	_, cfg, err := backupSettings(ctx, tx)
	if err != nil {
		return out, err
	}
	var restoring bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tripfolio_restore.jobs WHERE state IN ('queued','preparing','restoring'))`).Scan(&restoring); err != nil {
		return out, err
	}
	if restoring {
		return out, apperr.Conflicted("BACKUP_BUSY", "数据库正在恢复，请稍后重试")
	}
	if cfg.Secret == "" || cfg.URL == "" {
		return out, apperr.BadRequest("BACKUP_NOT_CONFIGURED", "请先保存 WebDAV 设置")
	}
	id := uuid.New()
	if retry != nil {
		id = *retry
		// Serialize retries against the same row and refuse active River jobs.
		var state string
		var destination string
		err = tx.QueryRow(ctx, `SELECT state,config->>'destination' FROM admin_backup_runs WHERE id=$1 FOR UPDATE`, id).Scan(&state, &destination)
		if errors.Is(err, pgx.ErrNoRows) {
			return out, apperr.NotFound()
		}
		if err != nil {
			return out, err
		}
		if state != "failed" || destination != cfg.Destination {
			return out, apperr.Conflicted("BACKUP_RETRY_UNAVAILABLE", "只能重试当前存储目标中的失败备份")
		}
		var active bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM river_job WHERE kind='database_backup' AND args->>'id'=$1 AND state IN ('available','running','retryable','scheduled','pending'))`, id.String()).Scan(&active); err != nil {
			return out, err
		}
		if active {
			return out, apperr.Conflicted("BACKUP_BUSY", "任务尚未结束，请稍后重试")
		}
		raw, _ := json.Marshal(cfg)
		_, err = tx.Exec(ctx, `UPDATE admin_backup_runs SET state='queued',error_code='',cleanup_error='',started_at=NULL,finished_at=NULL,config=$2 WHERE id=$1`, id, raw)
	} else {
		raw, _ := json.Marshal(cfg)
		_, err = tx.Exec(ctx, `INSERT INTO admin_backup_runs(id,trigger,state,config) VALUES($1,'manual','queued',$2)`, id, raw)
	}
	if err != nil {
		return out, err
	}
	if err = s.enqueue(ctx, tx, id); err != nil {
		return out, err
	}
	a.ResourceID = &id
	if err = auditedControl(ctx, tx, a); err != nil {
		return out, err
	}
	return s.LoadRun(ctx, id)
}

const backupColumns = `id,trigger,state,created_at,started_at,snapshot_at,finished_at,size_bytes,sha256,error_code,cleanup_error,remote_deleted_at,config,manifest`

type backupScanner interface{ Scan(...any) error }

func scanBackup(row backupScanner) (backup.Run, error) {
	var r backup.Run
	var cfg, manifest []byte
	err := row.Scan(&r.ID, &r.Trigger, &r.State, &r.CreatedAt, &r.StartedAt, &r.SnapshotAt, &r.FinishedAt, &r.Size, &r.SHA256, &r.ErrorCode, &r.CleanupError, &r.RemoteDeletedAt, &cfg, &manifest)
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(cfg, &r.Config); err != nil {
		return r, err
	}
	err = json.Unmarshal(manifest, &r.Manifest)
	return r, err
}
func (s *BackupStore) LoadRun(ctx context.Context, id uuid.UUID) (backup.Run, error) {
	return scanBackup(s.pool.QueryRow(ctx, `SELECT `+backupColumns+` FROM admin_backup_runs WHERE id=$1`, id))
}

func (s *BackupStore) BackupRuns(ctx context.Context, sess admin.Session, a admin.Audit, page int) (backup.Page, error) {
	out := backup.Page{Data: []backup.Run{}, Page: page, PageSize: 20}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = lockControl(ctx, tx, sess, sess.AccountID, false); err != nil {
		return out, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM admin_backup_runs`).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT `+backupColumns+` FROM admin_backup_runs ORDER BY created_at DESC,id DESC LIMIT 20 OFFSET $1`, (page-1)*20)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		r, e := scanBackup(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Data = append(out.Data, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	a.Details = map[string]any{"page": page, "result_count": len(out.Data)}
	return out, auditedControl(ctx, tx, a)
}

func (s *BackupStore) Schedule(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	out, cfg, err := backupSettings(ctx, tx)
	if err != nil {
		return err
	}
	var restoring bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM tripfolio_restore.jobs WHERE state IN ('queued','preparing','restoring'))`).Scan(&restoring); err != nil {
		return err
	}
	if restoring {
		return tx.Commit(ctx)
	}
	// If a process could not persist its terminal state, surface that failure after River finishes.
	rows, err := tx.Query(ctx, `UPDATE admin_backup_runs b SET state='failed',error_code='BACKUP_INTERRUPTED',finished_at=clock_timestamp()
      WHERE b.state IN ('queued','running','uploading','verifying','retrying') AND b.created_at<now()-interval '1 minute'
      AND NOT EXISTS(SELECT 1 FROM river_job r WHERE r.kind='database_backup' AND r.args->>'id'=b.id::text AND r.state IN ('available','running','retryable','scheduled','pending')) RETURNING b.id`)
	if err != nil {
		return err
	}
	var orphans []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		orphans = append(orphans, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range orphans {
		if err = insertAudit(ctx, tx, backupEvent(id, "backup.failed", "failure")); err != nil {
			return err
		}
	}
	if !cfg.Enabled || out.NextAt == nil || out.NextAt.After(time.Now()) {
		return tx.Commit(ctx)
	}
	var active bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM admin_backup_runs WHERE state IN ('queued','running','uploading','verifying','retrying'))`).Scan(&active); err != nil {
		return err
	}
	if active {
		return tx.Commit(ctx)
	}
	id := uuid.New()
	raw, _ := json.Marshal(cfg)
	if _, err = tx.Exec(ctx, `INSERT INTO admin_backup_runs(id,trigger,state,config) VALUES($1,'scheduled','queued',$2)`, id, raw); err != nil {
		return err
	}
	if err = s.enqueue(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE admin_backup_settings SET next_at=$1 WHERE id=1`, backup.Next(time.Now(), cfg.Time)); err != nil {
		return err
	}
	return auditedControl(ctx, tx, backupEvent(id, "backup.scheduled", "success"))
}

func backupEvent(id uuid.UUID, action, result string) admin.Audit {
	return admin.Audit{ID: uuid.New(), ResourceType: "database_backup", ResourceID: &id, Action: action, Result: result, OccurredAt: time.Now().UTC(), Details: map[string]any{"executor": "worker"}}
}

func (s *BackupStore) Progress(ctx context.Context, id uuid.UUID, state string, m *backup.Manifest) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if m != nil {
		raw, _ := json.Marshal(m)
		_, err = tx.Exec(ctx, `UPDATE admin_backup_runs SET state=$2,snapshot_at=$3,size_bytes=$4,sha256=$5,manifest=$6 WHERE id=$1`, id, state, m.SnapshotAt, m.Size, m.SHA256, raw)
	} else {
		_, err = tx.Exec(ctx, `UPDATE admin_backup_runs SET state=$2,started_at=coalesce(started_at,clock_timestamp()),error_code='' WHERE id=$1`, id, state)
	}
	if err != nil {
		return err
	}
	if state == "running" {
		return auditedControl(ctx, tx, backupEvent(id, "backup.started", "success"))
	}
	return tx.Commit(ctx)
}
func (s *BackupStore) Finish(ctx context.Context, id uuid.UUID, state, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE admin_backup_runs SET state=$2,error_code=$3,cleanup_error=CASE WHEN $2='succeeded' THEN '' ELSE cleanup_error END,finished_at=CASE WHEN $2='retrying' THEN NULL ELSE clock_timestamp() END WHERE id=$1`, id, state, code)
	if err != nil {
		return err
	}
	action, result := "backup.failed", "failure"
	if state == "succeeded" {
		action, result = "backup.completed", "success"
	}
	return auditedControl(ctx, tx, backupEvent(id, action, result))
}
func (s *BackupStore) Acquire(ctx context.Context) (context.Context, func(), error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return ctx, nil, err
	}
	var ok bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(781241,22)`).Scan(&ok)
	if err != nil || !ok {
		conn.Release()
		if err == nil {
			err = backup.ErrBusy
		}
		return ctx, nil, err
	}
	lease, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-lease.Done():
				return
			case <-t.C:
				p, c := context.WithTimeout(lease, 5*time.Second)
				e := conn.Conn().Ping(p)
				c()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	release := func() {
		cancel()
		<-done
		c, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if _, e := conn.Exec(c, `SELECT pg_advisory_unlock(781241,22)`); e != nil {
			_ = conn.Hijack().Close(c)
		} else {
			conn.Release()
		}
	}
	return lease, release, nil
}
func (s *BackupStore) Expired(ctx context.Context, cfg backup.Config) ([]backup.Run, error) {
	var destination string
	var retain int
	if err := s.pool.QueryRow(ctx, `SELECT config->>'destination',(config->>'retain')::integer FROM admin_backup_settings WHERE id=1`).Scan(&destination, &retain); err != nil {
		return nil, err
	}
	// A queued run must not apply an obsolete retention limit after settings change.
	if destination != cfg.Destination {
		return []backup.Run{}, nil
	}
	if retain < 1 {
		return nil, errors.New("BACKUP_SETTINGS_INVALID")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+backupColumns+` FROM admin_backup_runs WHERE state='succeeded' AND remote_deleted_at IS NULL AND config->>'destination'=$1 ORDER BY snapshot_at DESC,id DESC OFFSET $2`, cfg.Destination, retain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []backup.Run{}
	for rows.Next() {
		r, e := scanBackup(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *BackupStore) Deleted(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE admin_backup_runs SET remote_deleted_at=clock_timestamp(),cleanup_error='' WHERE id=$1 AND state='succeeded'`, id)
	if err != nil {
		return err
	}
	return auditedControl(ctx, tx, backupEvent(id, "backup.pruned", "success"))
}

func (s *BackupStore) BeforeDelete(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	return auditedControl(ctx, tx, backupEvent(id, "backup.prune.request", "success"))
}
func (s *BackupStore) CleanupError(ctx context.Context, id uuid.UUID, code string) error {
	_, err := s.pool.Exec(ctx, `UPDATE admin_backup_runs SET cleanup_error=$2 WHERE id=$1`, id, code)
	return err
}
