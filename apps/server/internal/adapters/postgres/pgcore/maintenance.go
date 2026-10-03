package pgcore

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrMaintenance = errors.New("DATABASE_RESTORING")

// One dedicated connection holds a shared lease for each in-flight request/job.
// The restore executor takes the exclusive lease before replacing any tables.
type MaintenanceGate struct {
	mu     sync.Mutex
	url    string
	pool   *pgxpool.Pool
	conn   *pgx.Conn
	lease  context.Context
	cancel context.CancelFunc
	epoch  int64
}

func NewMaintenanceGate(url string, pool *pgxpool.Pool) *MaintenanceGate {
	return &MaintenanceGate{url: url, pool: pool}
}

func (g *MaintenanceGate) disconnect() {
	if g.cancel != nil {
		g.cancel()
	}
	if g.conn != nil {
		ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		_ = g.conn.Close(ctx)
		stop()
		g.conn = nil
	}
}

func (g *MaintenanceGate) Enter(ctx context.Context) (context.Context, func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn == nil {
		c, err := pgx.Connect(ctx, g.url)
		if err != nil {
			return ctx, nil, err
		}
		g.conn = c
		g.lease, g.cancel = context.WithCancel(context.Background())
	}
	var allowed bool
	var epoch int64
	err := g.conn.QueryRow(ctx, `SELECT CASE WHEN EXISTS (SELECT 1 FROM tripfolio_restore.jobs WHERE state IN ('queued','preparing','restoring')) THEN false ELSE pg_try_advisory_lock_shared(781241,23) END, epoch FROM tripfolio_restore.control WHERE id`).Scan(&allowed, &epoch)
	if err != nil {
		g.disconnect()
		return ctx, nil, err
	}
	if !allowed {
		return ctx, nil, ErrMaintenance
	}
	if epoch != g.epoch {
		g.pool.Reset()
		g.epoch = epoch
	}
	conn := g.conn
	work, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(g.lease, cancel)
	var once sync.Once
	return work, func() {
		once.Do(func() {
			stop()
			cancel()
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.conn != conn {
				return
			}
			end, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			if _, err := conn.Exec(end, `SELECT pg_advisory_unlock_shared(781241,23)`); err != nil {
				g.disconnect()
			}
		})
	}, nil
}

func (g *MaintenanceGate) Watch(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	defer func() { g.mu.Lock(); defer g.mu.Unlock(); g.disconnect() }()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			g.mu.Lock()
			if g.conn != nil {
				ping, cancel := context.WithTimeout(ctx, 3*time.Second)
				if err := g.conn.Ping(ping); err != nil {
					g.disconnect()
				}
				cancel()
			}
			g.mu.Unlock()
		}
	}
}
