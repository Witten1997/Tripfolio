-- 账号级账单分类。

-- name: InsertExpenseCategory :one
INSERT INTO expense_categories (id, account_id, name, icon, sort_order, is_preset, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING *;

-- name: CountExpenseCategories :one
SELECT count(*) AS n FROM expense_categories
WHERE account_id = $1 AND deleted_at IS NULL;

-- name: ListExpenseCategories :many
SELECT * FROM expense_categories
WHERE account_id = $1 AND deleted_at IS NULL
ORDER BY sort_order, id;

-- name: GetExpenseCategory :one
SELECT * FROM expense_categories
WHERE account_id = $1 AND id = $2;

-- name: GetExpenseCategoryForUpdate :one
SELECT * FROM expense_categories
WHERE account_id = $1 AND id = $2
FOR UPDATE;

-- name: MaxExpenseCategorySortOrder :one
SELECT coalesce(max(sort_order), -1)::integer AS max_sort FROM expense_categories
WHERE account_id = $1 AND deleted_at IS NULL;

-- name: UpdateExpenseCategory :one
UPDATE expense_categories
SET name = $3, icon = $4, sort_order = $5, version = version + 1, updated_at = $6
WHERE account_id = $1 AND id = $2
RETURNING *;

-- name: SoftDeleteExpenseCategory :one
UPDATE expense_categories
SET deleted_at = $3, version = version + 1, updated_at = $3
WHERE account_id = $1 AND id = $2
RETURNING *;

-- name: CountLedgerEntriesUsingCategory :one
SELECT count(*) AS n FROM ledger_entries
WHERE account_id = $1 AND category_id = $2 AND deleted_at IS NULL;

-- name: ExpenseCategoryIDExists :one
SELECT EXISTS (SELECT 1 FROM expense_categories WHERE id = $1) AS exists;
