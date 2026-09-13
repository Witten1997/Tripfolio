-- 待办（数据库设计表 7）。逾期不落列，按应用传入的旅行时区“今天”在 SQL 中筛选；空截止日期用哨兵排在最后，与索引表达式一致。

-- name: GetTodoItem :one
SELECT * FROM todo_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id);

-- name: GetTodoItemForUpdate :one
SELECT * FROM todo_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: TodoItemIDExists :one
SELECT EXISTS (SELECT 1 FROM todo_items t WHERE t.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'todo' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: InsertTodoItem :one
INSERT INTO todo_items (id, account_id, trip_id, title, due_on, notes, completed_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(title), sqlc.narg(due_on), sqlc.arg(notes), sqlc.narg(completed_at),
        sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: UpdateTodoItem :one
UPDATE todo_items
SET title = sqlc.arg(title), due_on = sqlc.narg(due_on), notes = sqlc.arg(notes), completed_at = sqlc.narg(completed_at),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SoftDeleteTodoItem :one
UPDATE todo_items
SET deleted_at = sqlc.arg(deleted_at), version = version + 1, updated_at = sqlc.arg(deleted_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- 列表：接口设计 3.9 TodoFilters；state 为 all/pending/completed/overdue，today 为旅行时区的今天。
-- name: ListTodoItems :many
SELECT * FROM todo_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
  AND (sqlc.arg(state)::text = 'all'
       OR (sqlc.arg(state)::text = 'pending' AND completed_at IS NULL)
       OR (sqlc.arg(state)::text = 'completed' AND completed_at IS NOT NULL)
       OR (sqlc.arg(state)::text = 'overdue' AND completed_at IS NULL AND due_on < sqlc.arg(today)::date))
  AND (sqlc.narg(cursor_due_key)::date IS NULL
       OR (COALESCE(due_on, DATE '9999-12-31'), id) > (sqlc.narg(cursor_due_key)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY COALESCE(due_on, DATE '9999-12-31'), id
LIMIT sqlc.arg(row_limit);
