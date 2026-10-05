package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tripfolio/server/db"
	"tripfolio/server/internal/foundation/apperr"
)

func (s *Service) RestoreReadiness() (bool, string) {
	if s.restores == nil || s.restoreURL == "" {
		return false, "恢复服务未启用"
	}
	if s.passphrase() == "" {
		return false, "服务器尚未配置备份密码"
	}
	for _, tool := range []string{"pg_restore", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			return false, "服务器未安装 PostgreSQL " + tool + " 客户端"
		}
	}
	return true, "恢复工具已就绪"
}

func (s *Service) RestoreStatus(ctx context.Context, id uuid.UUID, token string) (RestoreJob, error) {
	if s.restores == nil || len(token) != 43 {
		return RestoreJob{}, apperr.NotFound()
	}
	h := sha256.Sum256([]byte(token))
	return s.restores.RestoreStatus(ctx, id, h[:])
}

// The executor is independent of River because the restore replaces River's tables.
func (s *Service) RunRestores(ctx context.Context) {
	if s.restores == nil {
		return
	}
	t := time.NewTicker(time.Second)
	defer t.Stop()
	tick := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.restoreNext(ctx, tick%60 == 0)
			tick++
		}
	}
}

func (s *Service) restoreNext(parent context.Context, sweep bool) {
	job, err := s.restores.PendingRestore(parent)
	if err != nil || (job == nil && !sweep) {
		return
	}
	ctx, cancel := context.WithTimeout(parent, s.options.Timeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, s.restoreURL)
	if err != nil {
		return
	}
	defer conn.Close(context.Background())
	// 24 serializes executors; 23 drains all application activity; 22 excludes backups.
	keys := []int{24, 23, 22}
	if job == nil {
		keys = []int{24}
	}
	for _, key := range keys {
		var locked bool
		if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(781241,$1)`, key).Scan(&locked); err != nil || !locked {
			return
		}
	}
	s.cleanupRestoreFiles()
	if job == nil {
		return
	}
	job, err = s.restores.PendingRestore(ctx)
	if err != nil || job == nil {
		return
	}
	if job.State != "queued" {
		_ = s.restores.RestoreProgress(ctx, job.ID, "failed", "RESTORE_INTERRUPTED")
		return
	}
	if err = s.restores.RestoreProgress(ctx, job.ID, "preparing", ""); err != nil {
		return
	}
	var actualID string
	var goose, river int64
	err = conn.QueryRow(ctx, `SELECT database_id::text,(SELECT coalesce(max(version_id),0) FROM goose_db_version WHERE is_applied),(SELECT coalesce(max(version),0) FROM river_migration) FROM admin_backup_settings WHERE id=1`).Scan(&actualID, &goose, &river)
	if err != nil || actualID != job.Config.DatabaseID {
		err = errors.New("RESTORE_DATABASE_MISMATCH")
	} else if !restoreVersionCompatible(job.Manifest.GooseVersion, goose) || job.Manifest.RiverVersion != river {
		err = errors.New("RESTORE_VERSION_MISMATCH")
	}
	var mu sync.Mutex
	heartbeat, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-heartbeat.Done():
				return
			case <-t.C:
				mu.Lock()
				ping, end := context.WithTimeout(heartbeat, 3*time.Second)
				e := conn.Ping(ping)
				end()
				mu.Unlock()
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { stop(); <-done }()
	if err == nil {
		err = s.restore(ctx, *job, goose, func() error {
			mu.Lock()
			defer mu.Unlock()
			// Pending state keeps new work out while the psql transaction takes these locks.
			_, e := conn.Exec(ctx, `SELECT pg_advisory_unlock(781241,22),pg_advisory_unlock(781241,23)`)
			return e
		})
	}
	s.restores.ResetConnections()
	if err != nil {
		code := err.Error()
		if ctx.Err() != nil {
			code = "RESTORE_TIMEOUT"
		}
		finish, end := context.WithTimeout(context.Background(), 10*time.Second)
		defer end()
		// A committed restore marks success in the same transaction; never overwrite it.
		_ = s.restores.RestoreProgress(finish, job.ID, "failed", code)
	}
}

func restoreVersionCompatible(source, target int64) bool {
	return source == target || (source >= 22 && source < target && target <= 26)
}

func (s *Service) restore(ctx context.Context, job RestoreJob, targetVersion int64, handoff func() error) error {
	if job.Manifest.ID != job.BackupID || job.Manifest.Format != 1 || job.Manifest.Size <= 0 || job.Manifest.Size > s.options.MaxBytes || len(job.Manifest.SHA256) != 64 {
		return errors.New("RESTORE_ARCHIVE_INVALID")
	}
	if err := s.prepareDirectory(); err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	dir, err := os.MkdirTemp(s.options.Directory, "restore-"+job.ID.String()+"-")
	if err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	defer os.RemoveAll(dir)
	d, err := s.dav(job.Config)
	if err != nil {
		return err
	}
	defer d.close()
	resp, err := d.request(ctx, "GET", job.BackupID.String()+".dump.age", nil, 0)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New("RESTORE_ARCHIVE_INVALID")
	}
	encrypted, err := os.OpenFile(filepath.Join(dir, "backup.age"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	defer encrypted.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(encrypted, hash), io.LimitReader(resp.Body, job.Manifest.Size+1))
	if err != nil || n != job.Manifest.Size || hex.EncodeToString(hash.Sum(nil)) != job.Manifest.SHA256 {
		return errors.New("RESTORE_ARCHIVE_INVALID")
	}
	if _, err = encrypted.Seek(0, io.SeekStart); err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	password := s.passphrase()
	if job.Secret != "" {
		password, err = s.unseal(job.Secret)
		if err != nil {
			return errors.New("RESTORE_PASSWORD_INVALID")
		}
	}
	dumpPath := filepath.Join(dir, "backup.dump")
	dump, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	err = Decrypt(password, encrypted, &boundedWriter{writer: dump, remaining: s.options.MaxBytes * 4})
	closeErr := dump.Close()
	if err != nil {
		return errors.New("RESTORE_PASSWORD_INVALID")
	}
	if closeErr != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	// Render the complete archive before touching the current schema.
	sqlPath := filepath.Join(dir, "restore.sql")
	sqlFile, err := os.OpenFile(sqlPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	cmd := exec.CommandContext(ctx, "pg_restore", "--clean", "--if-exists", "--no-owner", "--no-privileges", "--file=-", dumpPath)
	cmd.Stdout = &boundedWriter{writer: sqlFile, remaining: s.options.MaxBytes * 4}
	cmd.Stderr = io.Discard
	err = cmd.Run()
	closeErr = sqlFile.Close()
	if err != nil {
		return errors.New("RESTORE_ARCHIVE_INVALID")
	}
	if closeErr != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	before := filepath.Join(dir, "before.sql")
	after := filepath.Join(dir, "after.sql")
	beforeSQL := restoreBefore
	afterSQL := fmt.Sprintf(`DO $restore$ BEGIN
 IF (SELECT coalesce(max(version_id),0) FROM public.goose_db_version WHERE is_applied) <> %d
 OR (SELECT coalesce(max(version),0) FROM public.river_migration) <> %d THEN
  RAISE EXCEPTION 'Archive manifest version mismatch';
 END IF;
END $restore$;
`, job.Manifest.GooseVersion, job.Manifest.RiverVersion)
	if targetVersion >= 24 && job.Manifest.GooseVersion < 24 {
		// Version 23 lives outside the archive. Apply the only missing public migration
		// inside the restore transaction; future migrations require explicit compatibility.
		migration, e := db.Migrations.ReadFile("migrations/00024_site_settings.sql")
		if e != nil {
			return errors.New("RESTORE_VERSION_MISMATCH")
		}
		up, _, ok := strings.Cut(string(migration), "-- +goose Down")
		if !ok {
			return errors.New("RESTORE_VERSION_MISMATCH")
		}
		beforeSQL += "DROP TABLE public.admin_site_settings;\n"
		afterSQL += "SET LOCAL search_path TO public;\n" + up + "\n"
	}
	if targetVersion >= 25 && job.Manifest.GooseVersion < 25 {
		migration, e := db.Migrations.ReadFile("migrations/00025_account_deletion.sql")
		if e != nil {
			return errors.New("RESTORE_VERSION_MISMATCH")
		}
		up, _, ok := strings.Cut(string(migration), "-- +goose Down")
		if !ok {
			return errors.New("RESTORE_VERSION_MISMATCH")
		}
		afterSQL += "SET LOCAL search_path TO public;\n" + up + "\n"
	}
	if targetVersion >= 26 && job.Manifest.GooseVersion < 26 {
		migration, e := db.Migrations.ReadFile("migrations/00026_sync_epoch.sql")
		if e != nil {
			return errors.New("RESTORE_VERSION_MISMATCH")
		}
		up, _, ok := strings.Cut(string(migration), "-- +goose Down")
		if !ok {
			return errors.New("RESTORE_VERSION_MISMATCH")
		}
		afterSQL += "SET LOCAL search_path TO public;\n" + up + "\n"
	}
	afterSQL += fmt.Sprintf(restoreAfter, job.ID.String(), job.ID.String(), job.ID.String())
	if err = os.WriteFile(before, []byte(beforeSQL), 0600); err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	if err = os.WriteFile(after, []byte(afterSQL), 0600); err != nil {
		return errors.New("RESTORE_SPACE_LIMIT")
	}
	if err = s.restores.RestoreProgress(ctx, job.ID, "restoring", ""); err != nil {
		return errors.New("BACKUP_AUDIT_FAILED")
	}
	if err = handoff(); err != nil {
		return errors.New("RESTORE_FAILED")
	}
	args, env, err := restoreConnection(s.restoreURL)
	if err != nil {
		return err
	}
	args = append(args, "--no-psqlrc", "--no-password", "--set=ON_ERROR_STOP=1", "--single-transaction", "--file="+before, "--file="+sqlPath, "--file="+after)
	cmd = exec.CommandContext(ctx, "psql", args...)
	cmd.Env, cmd.Stdout, cmd.Stderr = env, io.Discard, io.Discard
	if err = cmd.Run(); err != nil {
		return errors.New("RESTORE_FAILED")
	}
	return nil
}

// Only remove this executor's UUID-named directories while holding all leases.
// This also clears decrypted files left by a process crash after commit.
func (s *Service) cleanupRestoreFiles() {
	st, err := os.Lstat(s.options.Directory)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return
	}
	entries, err := os.ReadDir(s.options.Directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "restore-") || len(name) < 45 || name[44] != '-' || !entry.IsDir() {
			continue
		}
		if _, err := uuid.Parse(name[8:44]); err != nil {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.options.Directory, name))
	}
}

func restoreConnection(raw string) ([]string, []string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return nil, nil, errors.New("RESTORE_DATABASE_MISMATCH")
	}
	password := ""
	if u.User != nil {
		password, _ = u.User.Password()
		u.User = url.User(u.User.Username())
	}
	q := u.Query()
	for _, key := range []string{"default_query_exec_mode", "statement_cache_capacity", "description_cache_capacity"} {
		q.Del(key)
	}
	q.Set("connect_timeout", "15")
	u.RawQuery = q.Encode()
	var env []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "PG") {
			env = append(env, value)
		}
	}
	env = append(env, "PGPASSWORD="+password, "PGAPPNAME=tripfolio-restore", "PGOPTIONS=-c statement_timeout=0 -c idle_in_transaction_session_timeout=0 -c lock_timeout=15000")
	return []string{"--dbname=" + u.String()}, env, nil
}

const restoreBefore = `
SELECT pg_advisory_xact_lock(781241,23),pg_advisory_xact_lock(781241,22);
LOCK public.river_job IN ACCESS EXCLUSIVE MODE;
CREATE TEMP TABLE restore_keep_job_sequence AS SELECT nextval(pg_get_serial_sequence('public.river_job','id')) AS next_id;
CREATE TEMP TABLE restore_keep_settings AS TABLE public.admin_backup_settings;
CREATE TEMP TABLE restore_keep_runs AS TABLE public.admin_backup_runs;
CREATE TEMP TABLE restore_keep_audits AS TABLE public.admin_audit_events;
CREATE TEMP TABLE restore_keep_versions AS TABLE public.goose_db_version;
`

const restoreAfter = `
DO $restore$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.admin_principals p JOIN public.accounts a ON a.id=p.account_id WHERE p.revoked_at IS NULL AND a.status='active') THEN
  RAISE EXCEPTION 'Restored database has no available administrator';
 END IF;
END $restore$;
DELETE FROM public.account_sessions;
DELETE FROM public.admin_sessions;
DELETE FROM public.auth_challenges;
DELETE FROM public.trip_shares;
UPDATE public.account_sync_state SET sync_epoch=gen_random_uuid(),updated_at=clock_timestamp();
UPDATE public.data_snapshots SET status='invalidated';
DO $restore$ DECLARE tables text; BEGIN
 SELECT string_agg('public.' || quote_ident(t),',') INTO tables
 FROM unnest(ARRAY['river_job','river_leader','river_client','river_client_queue','river_queue']) t
 WHERE to_regclass('public.' || t) IS NOT NULL;
 IF tables IS NOT NULL THEN EXECUTE 'TRUNCATE ' || tables; END IF;
END $restore$;
SELECT setval(pg_get_serial_sequence('public.river_job','id'),greatest((SELECT next_id FROM restore_keep_job_sequence),nextval(pg_get_serial_sequence('public.river_job','id'))));
DELETE FROM public.admin_backup_settings;
INSERT INTO public.admin_backup_settings SELECT * FROM restore_keep_settings;
UPDATE public.admin_backup_settings SET config=jsonb_set(config,'{enabled}','false'),next_at=NULL,version=version+1;
DELETE FROM public.admin_backup_runs;
INSERT INTO public.admin_backup_runs SELECT * FROM restore_keep_runs;
INSERT INTO public.admin_audit_events SELECT * FROM restore_keep_audits ON CONFLICT (id) DO NOTHING;
DELETE FROM public.goose_db_version;
INSERT INTO public.goose_db_version SELECT * FROM restore_keep_versions;
SELECT setval(pg_get_serial_sequence('public.goose_db_version','id'),coalesce((SELECT max(id) FROM public.goose_db_version),1));
INSERT INTO public.admin_audit_events(id,actor_account_id,action,resource_type,resource_id,result,details,occurred_at)
 SELECT gen_random_uuid(),actor_id,'backup.restore.completed','database_backup',backup_id,'success',jsonb_build_object('restore_id',id),clock_timestamp() FROM tripfolio_restore.jobs WHERE id='%s';
UPDATE tripfolio_restore.jobs SET state='succeeded',finished_at=clock_timestamp(),secret='',error_code='' WHERE id='%s';
UPDATE tripfolio_restore.control SET epoch=epoch+1;
DO $restore$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM tripfolio_restore.jobs WHERE id='%s' AND state='succeeded') THEN RAISE EXCEPTION 'Restore state missing'; END IF;
END $restore$;
`
