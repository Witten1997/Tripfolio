-- +goose Up
-- 旅行内的行程、行李、待办、预订。见数据库设计 v0.3 表 5、6、7、11。

CREATE TABLE itinerary_items (
    id                       uuid PRIMARY KEY,
    account_id               uuid          NOT NULL REFERENCES accounts (id),
    version                  bigint        NOT NULL DEFAULT 1,
    created_at               timestamptz   NOT NULL DEFAULT now(),
    updated_at               timestamptz   NOT NULL DEFAULT now(),
    deleted_at               timestamptz,
    trip_id                  uuid          NOT NULL,
    title                    varchar(200)  NOT NULL,
    kind                     varchar(16)   NOT NULL,
    scheduled_on             date          NOT NULL,
    sort_order               integer       NOT NULL DEFAULT 0,
    planned_start_local      timestamp(0) without time zone,
    planned_end_local        timestamp(0) without time zone,
    planned_duration_minutes integer,
    place_name               varchar(200)  NOT NULL DEFAULT '',
    address                  varchar(500)  NOT NULL DEFAULT '',
    latitude                 numeric(9,6),
    longitude                numeric(10,6),
    estimated_amount         numeric(18,4),
    notes                    text          NOT NULL DEFAULT '',
    status                   varchar(16)   NOT NULL DEFAULT 'pending',
    actual_start_local       timestamp(0) without time zone,
    actual_end_local         timestamp(0) without time zone,
    actual_notes             text          NOT NULL DEFAULT '',
    CONSTRAINT itinerary_items_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT itinerary_items_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT itinerary_items_version_positive CHECK (version > 0),
    CONSTRAINT itinerary_items_title_length CHECK (char_length(title) BETWEEN 1 AND 200 AND btrim(title) <> ''),
    CONSTRAINT itinerary_items_kind_enum CHECK (kind IN ('attraction', 'transport', 'lodging', 'dining', 'other')),
    CONSTRAINT itinerary_items_status_enum CHECK (status IN ('pending', 'completed', 'skipped')),
    CONSTRAINT itinerary_items_sort_nonnegative CHECK (sort_order >= 0),
    CONSTRAINT itinerary_items_duration_positive CHECK (planned_duration_minutes IS NULL OR planned_duration_minutes > 0),
    CONSTRAINT itinerary_items_end_or_duration CHECK (planned_end_local IS NULL OR planned_duration_minutes IS NULL),
    CONSTRAINT itinerary_items_planned_order CHECK (planned_start_local IS NULL OR planned_end_local IS NULL OR planned_end_local >= planned_start_local),
    CONSTRAINT itinerary_items_actual_order CHECK (actual_start_local IS NULL OR actual_end_local IS NULL OR actual_end_local >= actual_start_local),
    CONSTRAINT itinerary_items_coordinates_pair CHECK ((latitude IS NULL) = (longitude IS NULL)),
    CONSTRAINT itinerary_items_latitude_range CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    CONSTRAINT itinerary_items_longitude_range CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),
    CONSTRAINT itinerary_items_estimated_nonnegative CHECK (estimated_amount IS NULL OR estimated_amount >= 0),
    CONSTRAINT itinerary_items_notes_length CHECK (char_length(notes) <= 10000 AND char_length(actual_notes) <= 10000),
    CONSTRAINT itinerary_items_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX itinerary_items_day_order_idx
    ON itinerary_items (account_id, trip_id, scheduled_on, sort_order, id) WHERE deleted_at IS NULL;

CREATE TABLE packing_items (
    id         uuid PRIMARY KEY,
    account_id uuid          NOT NULL REFERENCES accounts (id),
    version    bigint        NOT NULL DEFAULT 1,
    created_at timestamptz   NOT NULL DEFAULT now(),
    updated_at timestamptz   NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    trip_id    uuid          NOT NULL,
    name       varchar(120)  NOT NULL,
    category   varchar(24)   NOT NULL,
    quantity   integer       NOT NULL DEFAULT 1,
    notes      varchar(2000) NOT NULL DEFAULT '',
    status     varchar(16)   NOT NULL DEFAULT 'pending',
    CONSTRAINT packing_items_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT packing_items_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT packing_items_version_positive CHECK (version > 0),
    CONSTRAINT packing_items_name_length CHECK (char_length(name) BETWEEN 1 AND 120 AND btrim(name) <> ''),
    CONSTRAINT packing_items_category_enum CHECK (category IN ('documents', 'electronics', 'clothing', 'daily', 'food', 'medicine', 'other')),
    CONSTRAINT packing_items_quantity_range CHECK (quantity BETWEEN 1 AND 9999),
    CONSTRAINT packing_items_status_enum CHECK (status IN ('pending', 'ready', 'packed')),
    CONSTRAINT packing_items_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX packing_items_trip_category_idx
    ON packing_items (account_id, trip_id, category, created_at, id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX packing_items_trip_category_name_unique
    ON packing_items (account_id, trip_id, category, lower(btrim(name))) WHERE deleted_at IS NULL;

CREATE TABLE todo_items (
    id           uuid PRIMARY KEY,
    account_id   uuid          NOT NULL REFERENCES accounts (id),
    version      bigint        NOT NULL DEFAULT 1,
    created_at   timestamptz   NOT NULL DEFAULT now(),
    updated_at   timestamptz   NOT NULL DEFAULT now(),
    deleted_at   timestamptz,
    trip_id      uuid          NOT NULL,
    title        varchar(200)  NOT NULL,
    due_on       date,
    notes        varchar(4000) NOT NULL DEFAULT '',
    completed_at timestamptz,
    CONSTRAINT todo_items_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT todo_items_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT todo_items_version_positive CHECK (version > 0),
    CONSTRAINT todo_items_title_length CHECK (char_length(title) BETWEEN 1 AND 200 AND btrim(title) <> ''),
    CONSTRAINT todo_items_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX todo_items_trip_due_idx
    ON todo_items (account_id, trip_id, (COALESCE(due_on, DATE '9999-12-31')), id) WHERE deleted_at IS NULL;

CREATE TABLE reservations (
    id                uuid PRIMARY KEY,
    account_id        uuid          NOT NULL REFERENCES accounts (id),
    version           bigint        NOT NULL DEFAULT 1,
    created_at        timestamptz   NOT NULL DEFAULT now(),
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    deleted_at        timestamptz,
    trip_id           uuid          NOT NULL,
    kind              varchar(16)   NOT NULL,
    title             varchar(200)  NOT NULL,
    booking_reference varchar(120)  NOT NULL DEFAULT '',
    transport_number  varchar(80),
    provider_name     varchar(200),
    start_local       timestamp(0) without time zone,
    end_local         timestamp(0) without time zone,
    origin            varchar(300),
    destination       varchar(300),
    address           varchar(500)  NOT NULL DEFAULT '',
    contact_name      varchar(120),
    contact_phone     varchar(64),
    notes             text          NOT NULL DEFAULT '',
    CONSTRAINT reservations_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT reservations_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT reservations_version_positive CHECK (version > 0),
    CONSTRAINT reservations_kind_enum CHECK (kind IN ('transport', 'lodging', 'attraction', 'other')),
    CONSTRAINT reservations_title_length CHECK (char_length(title) BETWEEN 1 AND 200 AND btrim(title) <> ''),
    CONSTRAINT reservations_transport_fields CHECK (kind = 'transport' OR (transport_number IS NULL AND origin IS NULL AND destination IS NULL)),
    CONSTRAINT reservations_time_order CHECK (start_local IS NULL OR end_local IS NULL OR end_local >= start_local),
    CONSTRAINT reservations_notes_length CHECK (char_length(notes) <= 10000),
    CONSTRAINT reservations_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX reservations_trip_start_idx
    ON reservations (account_id, trip_id, (COALESCE(start_local, TIMESTAMP '9999-12-31 23:59:59')), id) WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE reservations;
DROP TABLE todo_items;
DROP TABLE packing_items;
DROP TABLE itinerary_items;
