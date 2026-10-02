package bootstrap

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"tripfolio/server/internal/config"
	"tripfolio/server/internal/modules/admin"
)

func adminRuntime(pool *pgxpool.Pool, readiness *dbReadiness, worker *river.Client[pgx.Tx], cfg config.Config) func(context.Context) admin.RuntimeStatus {
	started := time.Now()
	// 只保留依赖是否装配，查询接口不持有或序列化连接串和凭证。
	objects, geo, mail := cfg.ObjectStore.Configured(), cfg.Geo.Configured(), cfg.Mail.Driver
	return func(ctx context.Context) admin.RuntimeStatus {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		out := admin.RuntimeStatus{Status: "ok", StartedAt: started, Components: []admin.ComponentStatus{{Name: "api", Status: "ok", Summary: "服务可响应"}}}
		db := admin.ComponentStatus{Name: "database", Status: "ok", Summary: "数据库连接与迁移版本正常"}
		if readiness.Check(ctx) != nil {
			db.Status, db.Summary, out.Status = "unavailable", "数据库或迁移版本检查失败", "degraded"
		}
		out.Components = append(out.Components, db)
		jobs := admin.ComponentStatus{Name: "worker", Status: "ok", Summary: "任务进程运行中"}
		select {
		case <-worker.Stopped():
			jobs.Status, jobs.Summary = "unavailable", "任务进程已停止"
		default:
			var paused, recent bool
			err := pool.QueryRow(ctx, `SELECT paused_at IS NOT NULL,updated_at>now()-interval '15 minutes' FROM river_queue WHERE name='default'`).Scan(&paused, &recent)
			if err != nil || !recent {
				jobs.Status, jobs.Summary = "unavailable", "任务队列心跳不可用"
			} else if paused {
				jobs.Status, jobs.Summary = "unavailable", "任务队列已暂停"
			}
		}
		if jobs.Status != "ok" {
			out.Status = "degraded"
		}
		out.Components = append(out.Components, jobs)
		for _, item := range []struct {
			name    string
			enabled bool
		}{{"object_store", objects}, {"maps", geo}, {"mail", mail == "smtp"}} {
			component := admin.ComponentStatus{Name: item.name, Status: "disabled", Summary: "未配置"}
			if item.enabled {
				component.Status, component.Summary = "configured", "已配置，未执行连通性探测"
			}
			if item.name == "mail" && mail == "log" {
				component.Summary = "仅写入本地日志，不发送邮件"
			}
			out.Components = append(out.Components, component)
		}
		out.Components = append(out.Components, admin.ComponentStatus{Name: "trip_purge", Status: "disabled", Summary: "永久清理尚未开放，功能完成前不执行清理"})
		return out
	}
}
