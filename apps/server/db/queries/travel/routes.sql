-- 旅行路线偏好、相邻路段与列表汇总。

-- name: GetRoutePlanTrip :one
SELECT id, route_short_mode, route_short_distance_meters, deleted_at
FROM trips
WHERE account_id = sqlc.arg(account_id) AND id = sqlc.arg(trip_id);

-- name: ListRoutePlanPoints :many
SELECT id, title, scheduled_on, sort_order, latitude, longitude
FROM itinerary_items
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND deleted_at IS NULL
ORDER BY scheduled_on, sort_order, id;

-- name: ListRoutePlanLegs :many
SELECT id, account_id, trip_id, from_item_id, to_item_id, version, mode, mode_source,
       direct_distance_meters, route_distance_meters, route_duration_seconds,
       status, error_code, calculated_at, created_at, updated_at
FROM itinerary_route_legs
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id)
ORDER BY created_at, id;

-- name: GetRouteSummary :one
SELECT * FROM trip_route_summaries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id);

-- name: InitEmptyRouteSummary :exec
INSERT INTO trip_route_summaries (account_id, trip_id, revision, status, updated_at)
VALUES (sqlc.arg(account_id), sqlc.arg(trip_id), 1, 'empty', sqlc.arg(updated_at))
ON CONFLICT (account_id, trip_id) DO NOTHING;

-- name: GetRouteSummaryForUpdate :one
SELECT * FROM trip_route_summaries
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id)
FOR UPDATE;

-- name: InvalidateRouteSummary :one
INSERT INTO trip_route_summaries (account_id, trip_id, revision, status, updated_at)
VALUES (sqlc.arg(account_id), sqlc.arg(trip_id), 1, 'stale', sqlc.arg(updated_at))
ON CONFLICT (account_id, trip_id) DO UPDATE
SET revision = trip_route_summaries.revision + 1,
    status = 'stale',
    total_distance_meters = NULL,
    total_duration_seconds = NULL,
    calculated_at = NULL,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: SetRouteSummaryCalculating :exec
UPDATE trip_route_summaries
SET status = 'calculating', updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND revision = sqlc.arg(revision);

-- name: ReplaceRouteSummary :execrows
UPDATE trip_route_summaries
SET status = sqlc.arg(status),
    total_distance_meters = sqlc.narg(total_distance_meters),
    total_duration_seconds = sqlc.narg(total_duration_seconds),
    ready_leg_count = sqlc.arg(ready_leg_count),
    total_leg_count = sqlc.arg(total_leg_count),
    missing_point_count = sqlc.arg(missing_point_count),
    calculated_at = sqlc.narg(calculated_at),
    updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND revision = sqlc.arg(revision);

-- name: DeleteRoutePlanLegs :exec
DELETE FROM itinerary_route_legs
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id);

-- name: InsertRoutePlanLeg :one
INSERT INTO itinerary_route_legs (
    id, account_id, trip_id, from_item_id, to_item_id, version, mode, mode_source,
    direct_distance_meters, route_distance_meters, route_duration_seconds,
    status, error_code, calculated_at, created_at, updated_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(account_id), sqlc.arg(trip_id), sqlc.arg(from_item_id), sqlc.arg(to_item_id), sqlc.arg(version),
    sqlc.arg(mode), sqlc.arg(mode_source), sqlc.arg(direct_distance_meters), sqlc.narg(route_distance_meters),
    sqlc.narg(route_duration_seconds), sqlc.arg(status), sqlc.narg(error_code),
    sqlc.narg(calculated_at), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: GetRoutePlanLegForUpdate :one
SELECT * FROM itinerary_route_legs
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateRouteLegMode :one
UPDATE itinerary_route_legs
SET mode = sqlc.arg(mode), mode_source = sqlc.arg(mode_source), status = 'stale',
    route_distance_meters = NULL, route_duration_seconds = NULL,
    error_code = NULL, calculated_at = NULL, version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id) AND trip_id = sqlc.arg(trip_id) AND id = sqlc.arg(id)
RETURNING *;
