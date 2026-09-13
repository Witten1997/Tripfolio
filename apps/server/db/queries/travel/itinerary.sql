-- 每日行程项目（数据库设计表 5）。时间由应用时钟传入；currency_code 不存列，由适配器按旅行币种派生。

-- name: GetTripContentInfo :one
SELECT currency_code, timezone, start_date, end_date, deleted_at FROM trips
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id);

-- name: GetItineraryItem :one
SELECT * FROM itinerary_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id);

-- name: GetItineraryItemForUpdate :one
SELECT * FROM itinerary_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: ItineraryItemIDExists :one
SELECT EXISTS (SELECT 1 FROM itinerary_items i WHERE i.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'itinerary_item' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: MaxItinerarySortOrder :one
SELECT COALESCE(max(sort_order), 0)::int AS max_order, count(*)::int AS item_count FROM itinerary_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND scheduled_on = sqlc.arg(scheduled_on) AND deleted_at IS NULL;

-- name: InsertItineraryItem :one
INSERT INTO itinerary_items (id, account_id, trip_id, title, kind, scheduled_on, sort_order,
    planned_start_local, planned_end_local, planned_duration_minutes, place_name, address, latitude, longitude,
    estimated_amount, notes, status, actual_start_local, actual_end_local, actual_notes, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(title), sqlc.arg(kind), sqlc.arg(scheduled_on), sqlc.arg(sort_order),
    sqlc.narg(planned_start_local), sqlc.narg(planned_end_local), sqlc.narg(planned_duration_minutes), sqlc.arg(place_name), sqlc.arg(address),
    sqlc.narg(latitude), sqlc.narg(longitude), sqlc.narg(estimated_amount), sqlc.arg(notes), sqlc.arg(status),
    sqlc.narg(actual_start_local), sqlc.narg(actual_end_local), sqlc.arg(actual_notes), sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: UpdateItineraryItem :one
UPDATE itinerary_items
SET title = sqlc.arg(title), kind = sqlc.arg(kind),
    planned_start_local = sqlc.narg(planned_start_local), planned_end_local = sqlc.narg(planned_end_local), planned_duration_minutes = sqlc.narg(planned_duration_minutes),
    place_name = sqlc.arg(place_name), address = sqlc.arg(address), latitude = sqlc.narg(latitude), longitude = sqlc.narg(longitude),
    estimated_amount = sqlc.narg(estimated_amount), notes = sqlc.arg(notes), status = sqlc.arg(status),
    actual_start_local = sqlc.narg(actual_start_local), actual_end_local = sqlc.narg(actual_end_local), actual_notes = sqlc.arg(actual_notes),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SoftDeleteItineraryItem :one
UPDATE itinerary_items
SET deleted_at = sqlc.arg(deleted_at), version = version + 1, updated_at = sqlc.arg(deleted_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: RepositionItineraryItem :one
UPDATE itinerary_items
SET scheduled_on = sqlc.arg(scheduled_on), sort_order = sqlc.arg(sort_order), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: ListItineraryItemsForDays :many
SELECT * FROM itinerary_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
  AND scheduled_on = ANY(sqlc.arg(dates)::date[])
ORDER BY scheduled_on, sort_order, id
FOR UPDATE;

-- 列表：接口设计 3.9 ItineraryFilters；游标用行比较走 (account_id, trip_id, scheduled_on, sort_order, id) 部分索引。
-- name: ListItineraryItems :many
SELECT * FROM itinerary_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
  AND (sqlc.narg(date_from)::date IS NULL OR scheduled_on >= sqlc.narg(date_from)::date)
  AND (sqlc.narg(date_to)::date IS NULL OR scheduled_on <= sqlc.narg(date_to)::date)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(cursor_scheduled_on)::date IS NULL
       OR (scheduled_on, sort_order, id) > (sqlc.narg(cursor_scheduled_on)::date, sqlc.narg(cursor_sort_order)::int, sqlc.narg(cursor_id)::uuid))
ORDER BY scheduled_on, sort_order, id
LIMIT sqlc.arg(row_limit);
