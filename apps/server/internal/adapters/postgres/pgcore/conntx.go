package pgcore

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// connTx 在一条已由调用方发出 BEGIN 的池连接上实现 pgx.Tx。
// pgx 自带的 BeginTx 会单独发一次 BEGIN；Writer 把 BEGIN 与首批查询放进同一条流水线，因此需要自行管理事务对象。
// Commit 与 Rollback 只标记状态并转发语句；调用方在流水线里提交后用 markClosed 记录，避免 defer 的回滚再走一次网络。
type connTx struct {
	conn      *pgxpool.Conn
	closed    bool
	savepoint int64
}

var _ pgx.Tx = (*connTx)(nil)

func (t *connTx) Begin(ctx context.Context) (pgx.Tx, error) {
	if t.closed {
		return nil, pgx.ErrTxClosed
	}
	t.savepoint++
	name := "tripfolio_sp_" + strconv.FormatInt(t.savepoint, 10)
	if _, err := t.conn.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return nil, err
	}
	return &savepointTx{parent: t, name: name}, nil
}

func (t *connTx) Commit(ctx context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	tag, err := t.conn.Exec(ctx, "COMMIT")
	if err != nil {
		return err
	}
	if tag.String() == "ROLLBACK" {
		return pgx.ErrTxCommitRollback
	}
	return nil
}

func (t *connTx) Rollback(ctx context.Context) error {
	if t.closed {
		return pgx.ErrTxClosed
	}
	t.closed = true
	_, err := t.conn.Exec(ctx, "ROLLBACK")
	return err
}

// markClosed 记录事务已在流水线内提交，之后的 Commit／Rollback 不再发送任何语句。
func (t *connTx) markClosed() { t.closed = true }

func (t *connTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	if t.closed {
		return 0, pgx.ErrTxClosed
	}
	return t.conn.CopyFrom(ctx, tableName, columnNames, rowSrc)
}

func (t *connTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	if t.closed {
		return closedBatchResults{}
	}
	return t.conn.SendBatch(ctx, b)
}

func (t *connTx) LargeObjects() pgx.LargeObjects {
	// pgx 未导出 LargeObjects 的构造方式；本项目不使用大对象。
	panic("pgcore: 写事务不支持 LargeObjects")
}

func (t *connTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	if t.closed {
		return nil, pgx.ErrTxClosed
	}
	return t.conn.Conn().Prepare(ctx, name, sql)
}

func (t *connTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if t.closed {
		return pgconn.CommandTag{}, pgx.ErrTxClosed
	}
	return t.conn.Exec(ctx, sql, arguments...)
}

func (t *connTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if t.closed {
		return nil, pgx.ErrTxClosed
	}
	return t.conn.Query(ctx, sql, args...)
}

func (t *connTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if t.closed {
		return closedRow{}
	}
	return t.conn.QueryRow(ctx, sql, args...)
}

func (t *connTx) Conn() *pgx.Conn { return t.conn.Conn() }

// savepointTx 是 connTx.Begin 返回的嵌套事务：以 SAVEPOINT 实现，语义与 pgx 自带的嵌套事务一致。
type savepointTx struct {
	parent *connTx
	name   string
	closed bool
}

var _ pgx.Tx = (*savepointTx)(nil)

func (s *savepointTx) Begin(ctx context.Context) (pgx.Tx, error) {
	if s.closed {
		return nil, pgx.ErrTxClosed
	}
	return s.parent.Begin(ctx)
}

func (s *savepointTx) Commit(ctx context.Context) error {
	if s.closed {
		return pgx.ErrTxClosed
	}
	s.closed = true
	_, err := s.parent.Exec(ctx, "RELEASE SAVEPOINT "+s.name)
	return err
}

func (s *savepointTx) Rollback(ctx context.Context) error {
	if s.closed {
		return pgx.ErrTxClosed
	}
	s.closed = true
	_, err := s.parent.Exec(ctx, "ROLLBACK TO SAVEPOINT "+s.name)
	return err
}

func (s *savepointTx) CopyFrom(ctx context.Context, tableName pgx.Identifier, columnNames []string, rowSrc pgx.CopyFromSource) (int64, error) {
	if s.closed {
		return 0, pgx.ErrTxClosed
	}
	return s.parent.CopyFrom(ctx, tableName, columnNames, rowSrc)
}

func (s *savepointTx) SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults {
	if s.closed {
		return closedBatchResults{}
	}
	return s.parent.SendBatch(ctx, b)
}

func (s *savepointTx) LargeObjects() pgx.LargeObjects { return s.parent.LargeObjects() }

func (s *savepointTx) Prepare(ctx context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	if s.closed {
		return nil, pgx.ErrTxClosed
	}
	return s.parent.Prepare(ctx, name, sql)
}

func (s *savepointTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if s.closed {
		return pgconn.CommandTag{}, pgx.ErrTxClosed
	}
	return s.parent.Exec(ctx, sql, arguments...)
}

func (s *savepointTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if s.closed {
		return nil, pgx.ErrTxClosed
	}
	return s.parent.Query(ctx, sql, args...)
}

func (s *savepointTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if s.closed {
		return closedRow{}
	}
	return s.parent.QueryRow(ctx, sql, args...)
}

func (s *savepointTx) Conn() *pgx.Conn { return s.parent.Conn() }

type closedRow struct{}

func (closedRow) Scan(...any) error { return pgx.ErrTxClosed }

type closedBatchResults struct{}

func (closedBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, pgx.ErrTxClosed
}
func (closedBatchResults) Query() (pgx.Rows, error) { return nil, pgx.ErrTxClosed }
func (closedBatchResults) QueryRow() pgx.Row        { return closedRow{} }
func (closedBatchResults) Close() error             { return pgx.ErrTxClosed }
