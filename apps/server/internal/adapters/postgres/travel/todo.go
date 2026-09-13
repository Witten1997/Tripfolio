package travelpg

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tripfolio/server/internal/adapters/postgres/dbgen"
	"tripfolio/server/internal/adapters/postgres/pgcore"
	"tripfolio/server/internal/foundation/types"
	"tripfolio/server/internal/foundation/write"
	"tripfolio/server/internal/modules/travel/todo"
)

func toTodoResource(row dbgen.TodoItem) todo.Resource {
	var due *types.Date
	if row.DueOn != nil {
		d := types.DateOf(*row.DueOn)
		due = &d
	}
	completedAt := pgcore.UTCPtr(row.CompletedAt)
	return todo.Resource{
		ID: row.ID, TripID: row.TripID, Title: row.Title, DueOn: due, Notes: row.Notes,
		Completed: completedAt != nil, CompletedAt: completedAt, Version: types.Version(row.Version),
		CreatedAt: pgcore.UTC(row.CreatedAt), UpdatedAt: pgcore.UTC(row.UpdatedAt), DeletedAt: pgcore.UTCPtr(row.DeletedAt),
	}
}

func dueTime(d *types.Date) *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time()
	return &t
}

func todoTripInfo(ctx context.Context, q *dbgen.Queries, accountID, tripID uuid.UUID) (todo.TripInfo, bool, error) {
	info, err := q.GetTripContentInfo(ctx, dbgen.GetTripContentInfoParams{AccountID: accountID, ID: tripID})
	if errors.Is(err, pgx.ErrNoRows) {
		return todo.TripInfo{}, false, nil
	}
	if err != nil {
		return todo.TripInfo{}, false, err
	}
	return todo.TripInfo{Timezone: info.Timezone, DeletedAt: pgcore.UTCPtr(info.DeletedAt)}, true, nil
}

func getTodoItem(ctx context.Context, q *dbgen.Queries, accountID, tripID, id uuid.UUID, forUpdate bool) (todo.Resource, bool, error) {
	var row dbgen.TodoItem
	var err error
	if forUpdate {
		row, err = q.GetTodoItemForUpdate(ctx, dbgen.GetTodoItemForUpdateParams{AccountID: accountID, TripID: tripID, ID: id})
	} else {
		row, err = q.GetTodoItem(ctx, dbgen.GetTodoItemParams{AccountID: accountID, TripID: tripID, ID: id})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return todo.Resource{}, false, nil
	}
	if err != nil {
		return todo.Resource{}, false, err
	}
	return toTodoResource(row), true, nil
}

// todoRepo 绑定到一次写事务。
type todoRepo struct {
	scope *pgcore.TxScope
}

var _ todo.Repo = (*todoRepo)(nil)

// NewTodoUnitOfWork 创建待办写事务入口。
func NewTodoUnitOfWork(writer *pgcore.Writer) write.UnitOfWork[todo.Repo] {
	return pgcore.NewUnitOfWork(writer, func(scope *pgcore.TxScope) todo.Repo {
		return &todoRepo{scope: scope}
	})
}

func (r *todoRepo) MergeSource() write.MergeSource { return r.scope }

func (r *todoRepo) Trip(ctx context.Context, accountID, tripID uuid.UUID) (todo.TripInfo, bool, error) {
	return todoTripInfo(ctx, r.scope.Queries, accountID, tripID)
}

func (r *todoRepo) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (todo.Resource, bool, error) {
	return getTodoItem(ctx, r.scope.Queries, accountID, tripID, id, false)
}

func (r *todoRepo) GetForUpdate(ctx context.Context, accountID, tripID, id uuid.UUID) (todo.Resource, bool, error) {
	return getTodoItem(ctx, r.scope.Queries, accountID, tripID, id, true)
}

func (r *todoRepo) IDExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.scope.Queries.TodoItemIDExists(ctx, dbgen.TodoItemIDExistsParams{ID: id, AccountID: r.scope.AccountID})
	return boolOf(exists), err
}

func (r *todoRepo) Insert(ctx context.Context, accountID uuid.UUID, t todo.Resource) (todo.Resource, error) {
	row, err := r.scope.Queries.InsertTodoItem(ctx, dbgen.InsertTodoItemParams{
		ID: t.ID, AccountID: accountID, TripID: t.TripID, Title: t.Title, DueOn: dueTime(t.DueOn), Notes: t.Notes, CompletedAt: t.CompletedAt, CreatedAt: t.CreatedAt,
	})
	if err != nil {
		return todo.Resource{}, err
	}
	return toTodoResource(row), nil
}

func (r *todoRepo) Update(ctx context.Context, accountID, tripID, id uuid.UUID, v todo.Values, now time.Time) (todo.Resource, error) {
	row, err := r.scope.Queries.UpdateTodoItem(ctx, dbgen.UpdateTodoItemParams{
		AccountID: accountID, TripID: tripID, ID: id, Title: v.Title, DueOn: dueTime(v.DueOn), Notes: v.Notes, CompletedAt: v.CompletedAt, UpdatedAt: now,
	})
	if err != nil {
		return todo.Resource{}, err
	}
	return toTodoResource(row), nil
}

func (r *todoRepo) SoftDelete(ctx context.Context, accountID, tripID, id uuid.UUID, now time.Time) (todo.Resource, error) {
	row, err := r.scope.Queries.SoftDeleteTodoItem(ctx, dbgen.SoftDeleteTodoItemParams{AccountID: accountID, TripID: tripID, ID: id, DeletedAt: &now})
	if err != nil {
		return todo.Resource{}, err
	}
	return toTodoResource(row), nil
}

// TodoReader 是事务外只读仓储。
type TodoReader struct {
	q *dbgen.Queries
}

// NewTodoReader 创建只读仓储。
func NewTodoReader(pool *pgxpool.Pool) *TodoReader {
	return &TodoReader{q: dbgen.New(pool)}
}

var _ todo.Reader = (*TodoReader)(nil)

// Trip 实现 todo.Reader。
func (r *TodoReader) Trip(ctx context.Context, accountID, tripID uuid.UUID) (todo.TripInfo, bool, error) {
	return todoTripInfo(ctx, r.q, accountID, tripID)
}

// Get 实现 todo.Reader。
func (r *TodoReader) Get(ctx context.Context, accountID, tripID, id uuid.UUID) (todo.Resource, bool, error) {
	return getTodoItem(ctx, r.q, accountID, tripID, id, false)
}

// List 实现 todo.Reader。
func (r *TodoReader) List(ctx context.Context, accountID, tripID uuid.UUID, q todo.ListQuery) ([]todo.Resource, error) {
	state := string(q.State)
	if state == "" {
		state = string(todo.StateAll)
	}
	p := dbgen.ListTodoItemsParams{AccountID: accountID, TripID: tripID, State: state, Today: q.Today.Time(), RowLimit: int32(q.Limit)}
	if q.After != nil {
		k := q.After.DueKey.Time()
		p.CursorDueKey, p.CursorID = &k, uuid.NullUUID{UUID: q.After.ID, Valid: true}
	}
	rows, err := r.q.ListTodoItems(ctx, p)
	if err != nil {
		return nil, err
	}
	out := make([]todo.Resource, 0, len(rows))
	for _, row := range rows {
		out = append(out, toTodoResource(row))
	}
	return out, nil
}
