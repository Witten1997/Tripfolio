package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var excludedData = []string{"public.account_sessions", "public.admin_sessions", "public.auth_challenges", "public.trip_shares", "public.river_job", "public.river_leader", "public.river_client", "public.river_client_queue", "public.river_queue", "public.admin_backup_settings", "public.admin_backup_runs"}

type boundedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *boundedWriter) Write(b []byte) (int, error) {
	if int64(len(b)) > w.remaining {
		return 0, errors.New("BACKUP_TOO_LARGE")
	}
	n, e := w.writer.Write(b)
	w.remaining -= int64(n)
	return n, e
}

func (s *Service) dump(ctx context.Context, id uuid.UUID, filename, databaseID string) (Manifest, error) {
	m := Manifest{Format: 1, ID: id, Schemas: []string{"public", "tripfolio_private"}, ExcludedData: excludedData, KeyID: keyID(s.key)}
	// Export a single database snapshot and keep it alive until pg_dump finishes.
	conn, err := pgx.Connect(ctx, s.options.DatabaseURL)
	if err != nil {
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	defer conn.Close(context.Background())
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	defer tx.Rollback(context.Background())
	var snapshot string
	var actualID string
	if err = tx.QueryRow(ctx, `SELECT database_id::text FROM admin_backup_settings WHERE id=1`).Scan(&actualID); err != nil || actualID != databaseID {
		return m, errors.New("BACKUP_DATABASE_MISMATCH")
	}
	err = tx.QueryRow(ctx, `SELECT pg_export_snapshot(),clock_timestamp(),current_setting('server_version_num')::integer,(SELECT coalesce(max(version_id),0) FROM goose_db_version WHERE is_applied),(SELECT coalesce(max(version),0) FROM river_migration)`).Scan(&snapshot, &m.SnapshotAt, &m.ServerVersion, &m.GooseVersion, &m.RiverVersion)
	if err != nil {
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	version, err := exec.CommandContext(versionCtx, "pg_dump", "--version").Output()
	cancel()
	if err != nil {
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	match := regexp.MustCompile(`PostgreSQL\) (\d+)`).FindStringSubmatch(string(version))
	if len(match) != 2 {
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	major, _ := strconv.Atoi(match[1])
	if major < m.ServerVersion/10000 {
		return m, errors.New("BACKUP_VERSION_MISMATCH")
	}
	parsed, err := url.Parse(s.options.DatabaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	password := ""
	if parsed.User != nil {
		password, _ = parsed.User.Password()
		parsed.User = url.User(parsed.User.Username())
	}
	q := parsed.Query()
	for _, key := range []string{"default_query_exec_mode", "statement_cache_capacity", "description_cache_capacity"} {
		q.Del(key)
	}
	q.Set("connect_timeout", "15")
	parsed.RawQuery = q.Encode()
	args := []string{"--format=custom", "--no-password", "--lock-wait-timeout=10s", "--snapshot=" + snapshot, "--schema=public", "--schema=tripfolio_private", "--dbname=" + parsed.String()}
	for _, table := range excludedData {
		args = append(args, "--exclude-table-data="+table)
	}
	tmp := filename + ".partial"
	if stat, e := os.Lstat(tmp); e == nil {
		if !stat.Mode().IsRegular() {
			return m, errors.New("BACKUP_FILE_MISSING")
		}
		if e = os.Remove(tmp); e != nil {
			return m, errors.New("BACKUP_FILE_MISSING")
		}
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return m, errors.New("BACKUP_SPACE_LIMIT")
	}
	defer func() { _ = f.Close(); _ = os.Remove(tmp) }()
	h := sha256.New()
	limited := &boundedWriter{io.MultiWriter(f, h), s.options.MaxBytes}
	recipient, err := age.NewScryptRecipient(s.passphrase())
	if err != nil {
		return m, errors.New("BACKUP_KEY_INVALID")
	}
	encrypted, err := age.Encrypt(limited, recipient)
	if err != nil {
		return m, errors.New("BACKUP_SPACE_LIMIT")
	}
	cmd := exec.CommandContext(ctx, "pg_dump", args...)
	cmd.Stdout = encrypted
	cmd.Stderr = io.Discard
	for _, env := range os.Environ() {
		key, _, _ := strings.Cut(env, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "PG") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "PGPASSWORD="+password, "PGAPPNAME=tripfolio-backup", "PGOPTIONS=-c statement_timeout=0 -c idle_in_transaction_session_timeout=0")
	if err = cmd.Run(); err != nil {
		if limited.remaining < 65536 {
			return m, errors.New("BACKUP_TOO_LARGE")
		}
		return m, errors.New("BACKUP_DUMP_FAILED")
	}
	if err = encrypted.Close(); err != nil {
		return m, errors.New("BACKUP_SPACE_LIMIT")
	}
	if err = f.Sync(); err != nil {
		return m, errors.New("BACKUP_SPACE_LIMIT")
	}
	if err = f.Close(); err != nil {
		return m, errors.New("BACKUP_SPACE_LIMIT")
	}
	// Retry may replace only its own previously incomplete archive.
	if st, e := os.Lstat(filename); e == nil {
		if !st.Mode().IsRegular() {
			return m, errors.New("BACKUP_FILE_MISSING")
		}
		if e = os.Remove(filename); e != nil {
			return m, errors.New("BACKUP_FILE_MISSING")
		}
	}
	if err = os.Rename(tmp, filename); err != nil {
		return m, errors.New("BACKUP_SPACE_LIMIT")
	}
	m.Size = s.options.MaxBytes - limited.remaining
	m.SHA256 = hex.EncodeToString(h.Sum(nil))
	return m, nil
}
