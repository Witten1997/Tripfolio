package pgcore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tripfolio/server/internal/foundation/apperr"
	"tripfolio/server/internal/foundation/write"
)

const atomicPatchQuery = `SELECT tripfolio_private.patch_item(
    $1::uuid, $2::uuid, $3::text, $4::bytea, $5::text,
    $6::uuid, $7::uuid, $8::bigint, $9::jsonb, $10::text[]
)`

// TryPatch 的隐式事务在数据库内完成锁、幂等、更新、日志与提交。
func (w *Writer) TryPatch(ctx context.Context, req write.Request, command write.PatchCommand) (write.Result, bool, error) {
	payload, err := json.Marshal(command.Values)
	if err != nil {
		return write.Result{}, false, apperr.Internal(err)
	}
	started := time.Now()
	conn, err := w.pool.Acquire(ctx)
	write.RecordTiming(ctx, "pool_acquire", time.Since(started))
	if err != nil {
		return write.Result{}, false, apperr.Dependency(err)
	}
	defer conn.Release()

	started = time.Now()
	var raw []byte
	// Exec 模式省去首次调用的预编译往返；参数仍通过扩展协议绑定。
	err = conn.QueryRow(ctx, atomicPatchQuery, pgx.QueryExecModeExec,
		req.AccountID, req.OperationID, req.OperationType, req.Fingerprint[:], command.EntityType,
		command.TripID, command.EntityID, command.BaseVersion, string(payload), command.Fields,
	).Scan(&raw)
	write.RecordTiming(ctx, "atomic_patch", time.Since(started))
	if err != nil {
		// SQL 或传输错误可能发生在提交后，不能当作未处理而重新执行。
		return write.Result{}, true, apperr.Internal(err)
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return write.Result{}, false, nil
	}
	var response struct {
		write.Result
		Data      json.RawMessage `json:"data"`
		ErrorCode string          `json:"error_code"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return write.Result{}, true, apperr.Internal(fmt.Errorf("解析原子写入响应: %w", err))
	}
	switch response.ErrorCode {
	case "SESSION_EXPIRED":
		return write.Result{}, true, apperr.Unauthorized("SESSION_EXPIRED", "")
	case "ACCOUNT_DELETING":
		return write.Result{}, true, apperr.Forbidden("ACCOUNT_DELETING", "账号正在注销")
	case "IDEMPOTENCY_CONFLICT":
		return write.Result{}, true, apperr.Conflicted("IDEMPOTENCY_CONFLICT", "同一操作编号携带了不同的内容")
	case "":
	default:
		return write.Result{}, true, apperr.Internal(fmt.Errorf("未知的原子写入错误: %s", response.ErrorCode))
	}
	if response.OperationID != req.OperationID || response.Primary == nil {
		return write.Result{}, true, apperr.Internal(fmt.Errorf("原子写入返回了无效的操作收据"))
	}
	if len(response.Data) > 0 && !bytes.Equal(response.Data, []byte("null")) {
		response.Result.Data = response.Data
	}
	return response.Result, true, nil
}

func (u *UnitOfWork[T]) TryPatch(ctx context.Context, req write.Request, command write.PatchCommand) (write.Result, bool, error) {
	return u.writer.TryPatch(ctx, req, command)
}
