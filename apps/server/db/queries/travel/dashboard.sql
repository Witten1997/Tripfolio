-- name: DashboardTrips :many
SELECT id, name, start_date, end_date, currency_code
FROM trips
WHERE account_id = sqlc.arg(account_id) AND deleted_at IS NULL
  AND end_date < (sqlc.arg(as_of)::timestamptz AT TIME ZONE timezone)::date
ORDER BY start_date DESC, id;

-- name: DashboardPlaces :many
SELECT i.id, i.trip_id, i.version, i.title, i.place_name, i.kind, i.scheduled_on,
       i.address, i.latitude, i.longitude, i.footprint_excluded, i.poi_id,
       r.province_code, r.province_name, r.city_code, r.city_name
FROM itinerary_items i
JOIN trips t ON t.account_id = i.account_id AND t.id = i.trip_id
LEFT JOIN dashboard_regions r ON r.latitude = i.latitude AND r.longitude = i.longitude
WHERE i.account_id = sqlc.arg(account_id) AND i.trip_id = ANY(sqlc.arg(trip_ids)::uuid[])
  AND i.deleted_at IS NULL AND t.deleted_at IS NULL
ORDER BY i.scheduled_on DESC, i.sort_order, i.id;

-- name: DashboardCategories :many
SELECT l.trip_id, l.category_id, COALESCE(c.name, '已删除分类')::text AS category_name,
       COALESCE(SUM(CASE WHEN l.kind = 'expense' THEN l.personal_amount ELSE 0 END), 0)::numeric AS personal_expense,
       COALESCE(SUM(CASE WHEN l.kind = 'refund' THEN l.personal_amount ELSE 0 END), 0)::numeric AS personal_refund,
       COALESCE(SUM(CASE WHEN l.kind = 'expense' THEN l.amount ELSE 0 END), 0)::numeric AS whole_expense,
       COALESCE(SUM(CASE WHEN l.kind = 'refund' THEN l.amount ELSE 0 END), 0)::numeric AS whole_refund
FROM ledger_entries l
JOIN trips t ON t.account_id = l.account_id AND t.id = l.trip_id
LEFT JOIN expense_categories c ON c.account_id = l.account_id AND c.id = l.category_id
WHERE l.account_id = sqlc.arg(account_id) AND l.trip_id = ANY(sqlc.arg(trip_ids)::uuid[])
  AND l.deleted_at IS NULL AND t.deleted_at IS NULL
GROUP BY l.trip_id, l.category_id, c.name
ORDER BY l.trip_id, l.category_id;

-- name: SaveDashboardRegion :exec
INSERT INTO dashboard_regions (latitude, longitude, province_code, province_name, city_code, city_name, resolved_at)
VALUES (sqlc.arg(latitude), sqlc.arg(longitude), sqlc.arg(province_code), sqlc.arg(province_name), sqlc.arg(city_code), sqlc.arg(city_name), sqlc.arg(resolved_at))
ON CONFLICT (latitude, longitude) DO UPDATE SET
    province_code = EXCLUDED.province_code, province_name = EXCLUDED.province_name,
    city_code = EXCLUDED.city_code, city_name = EXCLUDED.city_name, resolved_at = EXCLUDED.resolved_at;
