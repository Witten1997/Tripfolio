package write

import (
	"context"
	"sync"

	"github.com/google/uuid"

	"tripfolio/server/internal/foundation/apperr"
)

// Snapshotter 由内存仓储实现，使 MemoryUnitOfWork 能在业务函数失败时回滚到调用前的状态。
type Snapshotter interface {
	Snapshot() (restore func())
}

// memoryScope 是内存写事务内的记录器。
type memoryScope struct {
	changes  []Change
	warnings []string
	primary  *EntityRef
	affected []EntityRef
	jobs     []JobArgs
}

var _ Scope = (*memoryScope)(nil)

func (s *memoryScope) Record(c Change) { s.changes = append(s.changes, c) }

func (s *memoryScope) Warn(code string) {
	for _, w := range s.warnings {
		if w == code {
			return
		}
	}
	s.warnings = append(s.warnings, code)
}

func (s *memoryScope) SetPrimary(ref EntityRef) { s.primary = &ref }

func (s *memoryScope) AddAffected(ref EntityRef) { s.affected = append(s.affected, ref) }

func (s *memoryScope) Enqueue(args JobArgs) error {
	s.jobs = append(s.jobs, args)
	return nil
}

type memoryReceipt struct {
	fingerprint [32]byte
	result      Result
}

// MemoryUnitOfWork 是 UnitOfWork 的内存实现：串行执行、按操作编号去重并比对指纹、记录变更，
// 并以自身的变更记录实现 MergeSource。供模块单元测试使用，不涉及数据库。
// 变更历史单独加锁：业务函数在 Run 持锁期间会通过 MergeSource 读取历史。
type MemoryUnitOfWork[T any] struct {
	mu       sync.Mutex
	repo     T
	receipts map[uuid.UUID]memoryReceipt
	jobs     []JobArgs

	logMu   sync.Mutex
	changes []Change
}

// NewMemoryUnitOfWork 创建内存写事务；repo 实现 Snapshotter 时失败会回滚。
func NewMemoryUnitOfWork[T any](repo T) *MemoryUnitOfWork[T] {
	return &MemoryUnitOfWork[T]{repo: repo, receipts: map[uuid.UUID]memoryReceipt{}}
}

var _ MergeSource = (*MemoryUnitOfWork[any])(nil)

// Changes 返回按提交顺序记录的全部变更副本。
func (u *MemoryUnitOfWork[T]) Changes() []Change {
	u.logMu.Lock()
	defer u.logMu.Unlock()
	return append([]Change(nil), u.changes...)
}

// Jobs 返回已登记的后台任务副本。
func (u *MemoryUnitOfWork[T]) Jobs() []JobArgs {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]JobArgs(nil), u.jobs...)
}

// ChangedFieldsSince 实现 MergeSource：从已提交的变更中取 (base, current] 各版本 changed_fields 的并集。
func (u *MemoryUnitOfWork[T]) ChangedFieldsSince(_ context.Context, _ uuid.UUID, entityType string, entityID uuid.UUID, base, current int64) ([]string, bool, error) {
	u.logMu.Lock()
	defer u.logMu.Unlock()
	seen := map[string]struct{}{}
	versions := map[int64]struct{}{}
	var out []string
	for _, c := range u.changes {
		if c.EntityType != entityType || c.EntityID != entityID || c.Kind != ChangeUpsert || c.Version <= base || c.Version > current {
			continue
		}
		versions[c.Version] = struct{}{}
		for _, f := range c.ChangedFields {
			if _, ok := seen[f]; !ok {
				seen[f] = struct{}{}
				out = append(out, f)
			}
		}
	}
	return out, int64(len(versions)) == current-base, nil
}

// Run 实现 UnitOfWork。与 pgcore.Writer 相同：成功重试按收据重放并重新读取 data，指纹不同返回 409。
func (u *MemoryUnitOfWork[T]) Run(ctx context.Context, req Request, fn func(ctx context.Context, scope Scope, repo T) error, reload func(ctx context.Context, repo T) (any, error)) (Result, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if r, ok := u.receipts[req.OperationID]; ok {
		if r.fingerprint != req.Fingerprint {
			return Result{}, apperr.Conflicted("IDEMPOTENCY_CONFLICT", "同一操作编号携带了不同的内容")
		}
		result := r.result
		result.Replayed = true
		if reload != nil {
			if data, err := reload(ctx, u.repo); err == nil {
				result.Data = data
			} else if e, ok := apperr.As(err); !ok || e.Status != 404 && e.Status != 410 {
				return Result{}, err
			}
		}
		return result, nil
	}

	var restore func()
	if s, ok := any(u.repo).(Snapshotter); ok {
		restore = s.Snapshot()
	}
	rollback := func() {
		if restore != nil {
			restore()
		}
	}
	scope := &memoryScope{}
	if err := fn(ctx, scope, u.repo); err != nil {
		rollback()
		if _, ok := apperr.As(err); ok {
			return Result{}, err
		}
		return Result{}, apperr.Internal(err)
	}
	result := Result{
		OperationID: req.OperationID, Primary: scope.primary,
		Affected: AffectedRefs(scope.primary, scope.changes, scope.affected),
		Warnings: scope.warnings, Replayed: false,
	}
	if result.Warnings == nil {
		result.Warnings = []string{}
	}
	if reload != nil {
		data, err := reload(ctx, u.repo)
		if err != nil {
			rollback()
			return Result{}, err
		}
		result.Data = data
	}
	stored := result
	stored.Data = nil
	u.receipts[req.OperationID] = memoryReceipt{fingerprint: req.Fingerprint, result: stored}
	u.logMu.Lock()
	u.changes = append(u.changes, scope.changes...)
	u.logMu.Unlock()
	u.jobs = append(u.jobs, scope.jobs...)
	return result, nil
}
