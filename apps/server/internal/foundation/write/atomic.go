package write

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
)

// PatchCommand 只接收业务层已校验、规范化的字段和基线版本。
type PatchCommand struct {
	EntityType  string
	TripID      uuid.UUID
	EntityID    uuid.UUID
	BaseVersion int64
	Values      any
	Fields      []string
}

// AtomicPatcher 是可选的完整写入快捷入口；未处理时必须没有业务写入。
// 已处理的 Data 为当前资源的 JSON，资源已永久删除时为 nil。
type AtomicPatcher interface {
	TryPatch(context.Context, Request, PatchCommand) (Result, bool, error)
}

// TryPatch 让支持的仓储一次完成写入，并恢复模块使用的资源类型。
func TryPatch[T any](ctx context.Context, uow any, req Request, command PatchCommand) (Result, bool, error) {
	writer, ok := uow.(AtomicPatcher)
	if !ok {
		return Result{}, false, nil
	}
	result, handled, err := writer.TryPatch(ctx, req, command)
	if err != nil || !handled || result.Data == nil {
		return result, handled, err
	}
	raw, ok := result.Data.(json.RawMessage)
	if !ok {
		return Result{}, true, apperr.Internal(fmt.Errorf("原子写入返回了无效的资源类型 %T", result.Data))
	}
	var resource T
	if err := json.Unmarshal(raw, &resource); err != nil {
		return Result{}, true, apperr.Internal(fmt.Errorf("解析原子写入资源: %w", err))
	}
	result.Data = resource
	return result, true, nil
}
