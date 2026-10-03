package bootstrap

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"tripfolio/server/internal/adapters/postgres/pgcore"
)

func maintenanceHTTP(gate *pgcore.MaintenanceGate, adminPath string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Status has a narrowly scoped random token and survives invalidated sessions.
		if !strings.HasPrefix(r.URL.Path, "/api/") || (r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, adminPath+"/restores/")) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, release, err := gate.Enter(r.Context())
		if err != nil {
			w.Header().Set("Content-Type", "application/problem+json")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(http.StatusServiceUnavailable)
			message := `{"status":503,"code":"DEPENDENCY_UNAVAILABLE","detail":"数据库暂时不可用，请稍后重试"}`
			if errors.Is(err, pgcore.ErrMaintenance) {
				message = `{"status":503,"code":"DATABASE_RESTORING","detail":"数据库正在恢复，暂时无法操作，请稍后重试"}`
			}
			_, _ = w.Write([]byte(message))
			return
		}
		defer release()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func maintenanceWorker(gate *pgcore.MaintenanceGate, pool *pgxpool.Pool) rivertype.Middleware {
	return river.WorkerMiddlewareFunc(func(ctx context.Context, job *rivertype.JobRow, next func(context.Context) error) error {
		work, release, err := gate.Enter(ctx)
		if err != nil {
			return river.JobSnooze(5 * time.Second)
		}
		defer release()
		// A previously fetched job must not execute against the restored snapshot.
		var current bool
		if err = pool.QueryRow(work, `SELECT EXISTS(SELECT 1 FROM river_job WHERE id=$1 AND created_at=$2 AND kind=$3 AND args=$4::jsonb AND state='running')`, job.ID, job.CreatedAt, job.Kind, job.EncodedArgs).Scan(&current); err != nil {
			return river.JobSnooze(5 * time.Second)
		}
		if !current {
			return nil
		}
		return next(work)
	})
}
