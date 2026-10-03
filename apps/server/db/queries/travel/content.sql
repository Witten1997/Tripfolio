-- name: GetContentAssetReference :one
SELECT COALESCE(media_type,declared_media_type,'')::text AS media_type, deleted_at FROM assets
WHERE account_id=$1 AND trip_id=$2 AND id=$3 AND scope='trip' FOR UPDATE;

-- name: GetContentReservationReference :one
SELECT deleted_at FROM reservations WHERE account_id=$1 AND trip_id=$2 AND id=$3;

-- name: ContentTombstoneExists :one
SELECT EXISTS(SELECT 1 FROM entity_tombstones WHERE account_id=$1 AND trip_id=$2 AND entity_type=$3 AND entity_id=$4)::boolean;

-- name: UnlinkReservationDocuments :many
UPDATE documents SET reservation_id=NULL,version=version+1,updated_at=sqlc.arg(updated_at)
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND reservation_id=sqlc.arg(reservation_id) AND deleted_at IS NULL RETURNING *;

-- name: GetReservation :one
SELECT * FROM reservations WHERE account_id=$1 AND trip_id=$2 AND id=$3;

-- name: ReservationIDExists :one
SELECT (EXISTS(SELECT 1 FROM reservations WHERE id=sqlc.arg(id)) OR EXISTS(SELECT 1 FROM entity_tombstones t WHERE t.account_id=sqlc.arg(account_id) AND t.entity_type='reservation' AND t.entity_id=sqlc.arg(id)))::boolean AS used;

-- name: InsertReservation :one
INSERT INTO reservations (id,account_id,trip_id,kind,title,booking_reference,transport_number,provider_name,start_local,end_local,origin,destination,address,contact_name,contact_phone,notes,created_at,updated_at)
VALUES (sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(trip_id),sqlc.arg(kind),sqlc.arg(title),sqlc.arg(booking_reference),sqlc.narg(transport_number),sqlc.narg(provider_name),sqlc.narg(start_local),sqlc.narg(end_local),sqlc.narg(origin),sqlc.narg(destination),sqlc.arg(address),sqlc.narg(contact_name),sqlc.narg(contact_phone),sqlc.arg(notes),sqlc.arg(created_at),sqlc.arg(created_at)) RETURNING *;

-- name: UpdateReservation :one
UPDATE reservations SET kind=sqlc.arg(kind),title=sqlc.arg(title),booking_reference=sqlc.arg(booking_reference),transport_number=sqlc.narg(transport_number),provider_name=sqlc.narg(provider_name),start_local=sqlc.narg(start_local),end_local=sqlc.narg(end_local),origin=sqlc.narg(origin),destination=sqlc.narg(destination),address=sqlc.arg(address),contact_name=sqlc.narg(contact_name),contact_phone=sqlc.narg(contact_phone),notes=sqlc.arg(notes),version=version+1,updated_at=sqlc.arg(updated_at)
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND id=sqlc.arg(id) RETURNING *;

-- name: SoftDeleteReservation :one
UPDATE reservations SET deleted_at=sqlc.arg(updated_at),updated_at=sqlc.arg(updated_at),version=version+1
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND id=sqlc.arg(id) RETURNING *;

-- name: ListReservations :many
SELECT * FROM reservations WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND deleted_at IS NULL
AND (sqlc.narg(kind)::text IS NULL OR kind=sqlc.narg(kind))
AND (sqlc.narg(after_time)::timestamp IS NULL OR (COALESCE(start_local,TIMESTAMP '9999-12-31 23:59:59'),id)>(sqlc.narg(after_time)::timestamp,sqlc.arg(after_id)::uuid))
ORDER BY COALESCE(start_local,TIMESTAMP '9999-12-31 23:59:59'),id LIMIT sqlc.arg(page_limit);

-- name: GetDocument :one
SELECT * FROM documents WHERE account_id=$1 AND trip_id=$2 AND id=$3;

-- name: DocumentIDExists :one
SELECT (EXISTS(SELECT 1 FROM documents WHERE id=sqlc.arg(id)) OR EXISTS(SELECT 1 FROM entity_tombstones t WHERE t.account_id=sqlc.arg(account_id) AND t.entity_type='document' AND t.entity_id=sqlc.arg(id)))::boolean AS used;

-- name: InsertDocument :one
INSERT INTO documents (id,account_id,trip_id,title,notes,asset_id,reservation_id,created_at,updated_at)
VALUES (sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(trip_id),sqlc.arg(title),sqlc.arg(notes),sqlc.arg(asset_id),sqlc.narg(reservation_id),sqlc.arg(created_at),sqlc.arg(created_at)) RETURNING *;

-- name: UpdateDocument :one
UPDATE documents SET title=sqlc.arg(title),notes=sqlc.arg(notes),asset_id=sqlc.arg(asset_id),reservation_id=sqlc.narg(reservation_id),version=version+1,updated_at=sqlc.arg(updated_at)
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND id=sqlc.arg(id) RETURNING *;

-- name: SoftDeleteDocument :one
UPDATE documents SET deleted_at=sqlc.arg(updated_at),updated_at=sqlc.arg(updated_at),version=version+1
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND id=sqlc.arg(id) RETURNING *;

-- name: ListDocuments :many
SELECT * FROM documents WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND deleted_at IS NULL
AND (sqlc.narg(reservation_id)::uuid IS NULL OR reservation_id=sqlc.narg(reservation_id))
AND (sqlc.narg(after_time)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(after_time)::timestamptz,sqlc.arg(after_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);

-- name: GetPhoto :one
SELECT * FROM photos WHERE account_id=$1 AND trip_id=$2 AND id=$3;

-- name: PhotoIDExists :one
SELECT (EXISTS(SELECT 1 FROM photos WHERE id=sqlc.arg(id)) OR EXISTS(SELECT 1 FROM entity_tombstones t WHERE t.account_id=sqlc.arg(account_id) AND t.entity_type='photo' AND t.entity_id=sqlc.arg(id)))::boolean AS used;

-- name: InsertPhoto :one
INSERT INTO photos (id,account_id,trip_id,asset_id,taken_at_local,recorded_on,caption,sort_order,place_name,address,latitude,longitude,created_at,updated_at)
VALUES (sqlc.arg(id),sqlc.arg(account_id),sqlc.arg(trip_id),sqlc.arg(asset_id),sqlc.narg(taken_at_local),sqlc.arg(recorded_on),sqlc.arg(caption),sqlc.arg(sort_order),sqlc.arg(place_name),sqlc.arg(address),sqlc.narg(latitude),sqlc.narg(longitude),sqlc.arg(created_at),sqlc.arg(created_at)) RETURNING *;

-- name: UpdatePhoto :one
UPDATE photos SET asset_id=sqlc.arg(asset_id),taken_at_local=sqlc.narg(taken_at_local),recorded_on=sqlc.arg(recorded_on),caption=sqlc.arg(caption),sort_order=sqlc.arg(sort_order),place_name=sqlc.arg(place_name),address=sqlc.arg(address),latitude=sqlc.narg(latitude),longitude=sqlc.narg(longitude),version=version+1,updated_at=sqlc.arg(updated_at)
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND id=sqlc.arg(id) RETURNING *;

-- name: SoftDeletePhoto :one
UPDATE photos SET deleted_at=sqlc.arg(updated_at),updated_at=sqlc.arg(updated_at),version=version+1
WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND id=sqlc.arg(id) RETURNING *;

-- name: ListPhotos :many
SELECT * FROM photos WHERE account_id=sqlc.arg(account_id) AND trip_id=sqlc.arg(trip_id) AND deleted_at IS NULL
AND (sqlc.narg(date_from)::date IS NULL OR recorded_on>=sqlc.narg(date_from)::date)
AND (sqlc.narg(date_to)::date IS NULL OR recorded_on<=sqlc.narg(date_to)::date)
AND (sqlc.narg(after_day)::date IS NULL OR recorded_on<sqlc.narg(after_day)::date
 OR (recorded_on=sqlc.narg(after_day)::date AND (COALESCE(taken_at_local,TIMESTAMP '9999-12-31 23:59:59'),sort_order,id)>(sqlc.arg(after_time)::timestamp,sqlc.arg(after_sort)::integer,sqlc.arg(after_id)::uuid)))
ORDER BY recorded_on DESC,COALESCE(taken_at_local,TIMESTAMP '9999-12-31 23:59:59'),sort_order,id LIMIT sqlc.arg(page_limit);
