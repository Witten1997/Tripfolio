-- Snapshot reads all share the builder's single REPEATABLE READ transaction.

-- name: SnapshotTrips :many
SELECT * FROM trips WHERE account_id=$1 AND purge_requested_at IS NULL ORDER BY id;

-- name: SnapshotCategories :many
SELECT * FROM expense_categories WHERE account_id=$1 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotMembers :many
SELECT * FROM trip_members WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotItinerary :many
SELECT * FROM itinerary_items WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotPacking :many
SELECT * FROM packing_items WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotTodos :many
SELECT * FROM todo_items WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotLedger :many
SELECT * FROM ledger_entries WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotReservations :many
SELECT * FROM reservations WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotDocuments :many
SELECT * FROM documents WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotPhotos :many
SELECT * FROM photos WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;

-- name: SnapshotAssets :many
SELECT * FROM assets WHERE account_id=$1 AND trip_id=$2 AND deleted_at IS NULL ORDER BY id;
