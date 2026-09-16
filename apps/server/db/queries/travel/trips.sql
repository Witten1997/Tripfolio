-- 旅行与回收站。时间一律由应用时钟传入；阶段按旅行时区的“今天”在 SQL 中计算，使筛选与返回值一致。

-- name: GetTrip :one
SELECT * FROM trips
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id);

-- name: GetTripForUpdate :one
SELECT * FROM trips
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: TripIDExists :one
SELECT EXISTS (SELECT 1 FROM trips t WHERE t.id = sqlc.arg(id))
    OR EXISTS (SELECT 1 FROM entity_tombstones et WHERE et.account_id = sqlc.arg(account_id) AND et.entity_type = 'trip' AND et.entity_id = sqlc.arg(id)) AS exists;

-- name: GetAccountDefaultTimezone :one
SELECT default_timezone FROM accounts
WHERE id = sqlc.arg(account_id);

-- name: InsertTrip :one
INSERT INTO trips (id, account_id, name, start_date, end_date, destination, notes, timezone, currency_code, budget_amount,
    route_short_mode, route_short_distance_meters, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(name), sqlc.arg(start_date), sqlc.arg(end_date), sqlc.arg(destination), sqlc.arg(notes),
        sqlc.arg(timezone), sqlc.arg(currency_code), sqlc.narg(budget_amount), sqlc.arg(route_short_mode),
        sqlc.arg(route_short_distance_meters), sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: UpdateTrip :one
UPDATE trips
SET name = sqlc.arg(name), start_date = sqlc.arg(start_date), end_date = sqlc.arg(end_date), destination = sqlc.arg(destination),
    notes = sqlc.arg(notes), timezone = sqlc.arg(timezone), currency_code = sqlc.arg(currency_code), budget_amount = sqlc.narg(budget_amount),
    route_short_mode = sqlc.arg(route_short_mode), route_short_distance_meters = sqlc.arg(route_short_distance_meters),
    version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: SetTripArchived :one
UPDATE trips
SET archived_at = sqlc.narg(archived_at), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: TrashTrip :one
UPDATE trips
SET deleted_at = sqlc.arg(deleted_at), purge_after_at = sqlc.arg(purge_after_at), version = version + 1, updated_at = sqlc.arg(deleted_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: RestoreTrip :one
UPDATE trips
SET deleted_at = NULL, purge_after_at = NULL, version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: RequestTripPurge :one
UPDATE trips
SET purge_requested_at = sqlc.arg(requested_at), version = version + 1, updated_at = sqlc.arg(requested_at)
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(id)
RETURNING *;

-- name: GetActiveTripDeletionJob :one
SELECT * FROM deletion_jobs
WHERE owner_account_id = sqlc.arg(account_id) AND target_trip_id = sqlc.arg(trip_id)
  AND scope = 'trip' AND status IN ('queued', 'running', 'failed');

-- name: InsertTripDeletionJob :one
INSERT INTO deletion_jobs (id, owner_account_id, scope, target_trip_id, status, stage, created_at)
VALUES (sqlc.arg(id), sqlc.arg(account_id), 'trip', sqlc.arg(trip_id), sqlc.arg(status), sqlc.arg(stage), sqlc.arg(created_at))
RETURNING *;

-- name: TripHasEstimatedAmounts :one
SELECT EXISTS (
    SELECT 1 FROM itinerary_items
    WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL AND estimated_amount IS NOT NULL
) AS exists;

-- name: TripHasLocalTimes :one
SELECT EXISTS (
    SELECT 1 FROM itinerary_items ii
    WHERE ii.account_id = sqlc.arg(account_id) AND ii.trip_id = sqlc.arg(trip_id) AND ii.deleted_at IS NULL
      AND (ii.planned_start_local IS NOT NULL OR ii.planned_end_local IS NOT NULL OR ii.actual_start_local IS NOT NULL OR ii.actual_end_local IS NOT NULL)
) OR EXISTS (
    SELECT 1 FROM reservations rs
    WHERE rs.account_id = sqlc.arg(account_id) AND rs.trip_id = sqlc.arg(trip_id) AND rs.deleted_at IS NULL
      AND (rs.start_local IS NOT NULL OR rs.end_local IS NOT NULL)
) OR EXISTS (
    SELECT 1 FROM photos ph
    WHERE ph.account_id = sqlc.arg(account_id) AND ph.trip_id = sqlc.arg(trip_id) AND ph.deleted_at IS NULL AND ph.taken_at_local IS NOT NULL
) AS exists;

-- name: TripHasItineraryOutside :one
SELECT EXISTS (
    SELECT 1 FROM itinerary_items
    WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
      AND (scheduled_on < sqlc.arg(start_date) OR scheduled_on > sqlc.arg(end_date))
) AS exists;

-- 列表：接口设计 3.9 TripFilters。q 由应用转义 LIKE 通配符；游标用行比较走 (account_id, start_date DESC, id DESC) 索引。
-- name: ListTripsByStartDate :many
SELECT sqlc.embed(t),
       CASE
           WHEN (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date < t.start_date THEN 'planned'
           WHEN (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date > t.end_date THEN 'ended'
           ELSE 'ongoing'
       END::text AS phase,
       coalesce(rs.status, 'stale')::text AS route_status,
       rs.total_distance_meters,
       rs.total_duration_seconds
FROM trips t
LEFT JOIN trip_route_summaries rs ON rs.account_id = t.account_id AND rs.trip_id = t.id
WHERE t.account_id = sqlc.arg(account_id)
  AND t.deleted_at IS NULL
  AND (sqlc.arg(archived)::text = 'all'
       OR (sqlc.arg(archived)::text = 'true' AND t.archived_at IS NOT NULL)
       OR (sqlc.arg(archived)::text = 'false' AND t.archived_at IS NULL))
  AND (sqlc.narg(q)::text IS NULL
       OR t.name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR t.destination ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(phase)::text IS NULL
       OR (sqlc.narg(phase)::text = 'planned' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date < t.start_date)
       OR (sqlc.narg(phase)::text = 'ended' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date > t.end_date)
       OR (sqlc.narg(phase)::text = 'ongoing' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date BETWEEN t.start_date AND t.end_date))
  AND (sqlc.narg(cursor_start_date)::date IS NULL
       OR (t.start_date, t.id) < (sqlc.narg(cursor_start_date)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY t.start_date DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListTripsByStartDateAsc :many
SELECT sqlc.embed(t),
       CASE
           WHEN (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date < t.start_date THEN 'planned'
           WHEN (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date > t.end_date THEN 'ended'
           ELSE 'ongoing'
       END::text AS phase,
       coalesce(rs.status, 'stale')::text AS route_status,
       rs.total_distance_meters,
       rs.total_duration_seconds
FROM trips t
LEFT JOIN trip_route_summaries rs ON rs.account_id = t.account_id AND rs.trip_id = t.id
WHERE t.account_id = sqlc.arg(account_id)
  AND t.deleted_at IS NULL
  AND (sqlc.arg(archived)::text = 'all'
       OR (sqlc.arg(archived)::text = 'true' AND t.archived_at IS NOT NULL)
       OR (sqlc.arg(archived)::text = 'false' AND t.archived_at IS NULL))
  AND (sqlc.narg(q)::text IS NULL
       OR t.name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR t.destination ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(phase)::text IS NULL
       OR (sqlc.narg(phase)::text = 'planned' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date < t.start_date)
       OR (sqlc.narg(phase)::text = 'ended' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date > t.end_date)
       OR (sqlc.narg(phase)::text = 'ongoing' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date BETWEEN t.start_date AND t.end_date))
  AND (sqlc.narg(cursor_start_date)::date IS NULL
       OR (t.start_date, t.id) > (sqlc.narg(cursor_start_date)::date, sqlc.narg(cursor_id)::uuid))
ORDER BY t.start_date ASC, t.id ASC
LIMIT sqlc.arg(row_limit);

-- name: ListTripsByUpdatedAt :many
SELECT sqlc.embed(t),
       CASE
           WHEN (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date < t.start_date THEN 'planned'
           WHEN (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date > t.end_date THEN 'ended'
           ELSE 'ongoing'
       END::text AS phase,
       coalesce(rs.status, 'stale')::text AS route_status,
       rs.total_distance_meters,
       rs.total_duration_seconds
FROM trips t
LEFT JOIN trip_route_summaries rs ON rs.account_id = t.account_id AND rs.trip_id = t.id
WHERE t.account_id = sqlc.arg(account_id)
  AND t.deleted_at IS NULL
  AND (sqlc.arg(archived)::text = 'all'
       OR (sqlc.arg(archived)::text = 'true' AND t.archived_at IS NOT NULL)
       OR (sqlc.arg(archived)::text = 'false' AND t.archived_at IS NULL))
  AND (sqlc.narg(q)::text IS NULL
       OR t.name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR t.destination ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (sqlc.narg(phase)::text IS NULL
       OR (sqlc.narg(phase)::text = 'planned' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date < t.start_date)
       OR (sqlc.narg(phase)::text = 'ended' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date > t.end_date)
       OR (sqlc.narg(phase)::text = 'ongoing' AND (sqlc.arg(now)::timestamptz AT TIME ZONE t.timezone)::date BETWEEN t.start_date AND t.end_date))
  AND (sqlc.narg(cursor_updated_at)::timestamptz IS NULL
       OR (t.updated_at, t.id) < (sqlc.narg(cursor_updated_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY t.updated_at DESC, t.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListTrashedTrips :many
SELECT * FROM trips
WHERE account_id = sqlc.arg(account_id)
  AND deleted_at IS NOT NULL
  AND (sqlc.narg(cursor_deleted_at)::timestamptz IS NULL
       OR (deleted_at, id) < (sqlc.narg(cursor_deleted_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY deleted_at DESC, id DESC
LIMIT sqlc.arg(row_limit);
