package write

import "context"

// UnitOfWork 在统一写事务中执行业务写入。T 是模块声明的事务内仓储接口，由适配器绑定到同一事务。
// reload 在成功提交前与重放时都会被调用，用于填充 Result.Data（primary 的当前规范资源）；可为 nil。
type UnitOfWork[T any] interface {
	Run(ctx context.Context, req Request, fn func(ctx context.Context, scope Scope, repo T) error, reload func(ctx context.Context, repo T) (any, error)) (Result, error)
}

// Reader 提供事务外的只读仓储，由适配器实现。
type Reader[T any] interface {
	Repo() T
}
