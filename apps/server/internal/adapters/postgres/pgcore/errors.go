package pgcore

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// ConstraintViolation 返回违反的约束名；不是唯一约束或外键错误时返回空串。
func ConstraintViolation(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505", "23503", "23514":
			return pgErr.ConstraintName
		}
	}
	return ""
}

// UTC 把数据库读出的时间统一为 UTC，避免 JSON 输出带本地偏移。
func UTC(t time.Time) time.Time { return t.UTC() }

// UTCPtr 同 UTC，处理可空时间。
func UTCPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
