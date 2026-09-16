-- +goose Up
-- 旅行交通偏好、相邻路段路线与列表汇总。

ALTER TABLE trips
    ADD COLUMN route_short_mode varchar(16) NOT NULL DEFAULT 'walking',
    ADD COLUMN route_short_distance_meters integer NOT NULL DEFAULT 1500,
    ADD CONSTRAINT trips_route_short_mode_enum CHECK (route_short_mode IN ('walking', 'cycling')),
    ADD CONSTRAINT trips_route_short_distance_range CHECK (route_short_distance_meters BETWEEN 0 AND 50000);

CREATE TABLE itinerary_route_legs (
    id                       uuid PRIMARY KEY,
    account_id               uuid        NOT NULL,
    trip_id                  uuid        NOT NULL,
    from_item_id             uuid        NOT NULL,
    to_item_id               uuid        NOT NULL,
    version                  bigint      NOT NULL DEFAULT 1,
    mode                     varchar(16) NOT NULL,
    mode_source              varchar(16) NOT NULL,
    direct_distance_meters   bigint      NOT NULL,
    route_distance_meters    bigint,
    route_duration_seconds   bigint,
    path                     jsonb,
    status                   varchar(16) NOT NULL DEFAULT 'stale',
    error_code               varchar(64),
    calculated_at            timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT itinerary_route_legs_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT itinerary_route_legs_edge_unique UNIQUE (account_id, trip_id, from_item_id, to_item_id),
    CONSTRAINT itinerary_route_legs_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id) ON DELETE CASCADE,
    CONSTRAINT itinerary_route_legs_from_fk FOREIGN KEY (account_id, trip_id, from_item_id) REFERENCES itinerary_items (account_id, trip_id, id),
    CONSTRAINT itinerary_route_legs_to_fk FOREIGN KEY (account_id, trip_id, to_item_id) REFERENCES itinerary_items (account_id, trip_id, id),
    CONSTRAINT itinerary_route_legs_distinct_points CHECK (from_item_id <> to_item_id),
    CONSTRAINT itinerary_route_legs_version_positive CHECK (version > 0),
    CONSTRAINT itinerary_route_legs_mode_enum CHECK (mode IN ('driving', 'walking', 'cycling')),
    CONSTRAINT itinerary_route_legs_source_enum CHECK (mode_source IN ('preference', 'manual')),
    CONSTRAINT itinerary_route_legs_status_enum CHECK (status IN ('stale', 'ready', 'failed')),
    CONSTRAINT itinerary_route_legs_direct_nonnegative CHECK (direct_distance_meters >= 0),
    CONSTRAINT itinerary_route_legs_route_nonnegative CHECK (route_distance_meters IS NULL OR route_distance_meters >= 0),
    CONSTRAINT itinerary_route_legs_duration_nonnegative CHECK (route_duration_seconds IS NULL OR route_duration_seconds >= 0),
    CONSTRAINT itinerary_route_legs_path_array CHECK (path IS NULL OR jsonb_typeof(path) = 'array'),
    CONSTRAINT itinerary_route_legs_ready_complete CHECK (
        status <> 'ready' OR (route_distance_meters IS NOT NULL AND route_duration_seconds IS NOT NULL AND path IS NOT NULL AND calculated_at IS NOT NULL)
    ),
    CONSTRAINT itinerary_route_legs_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX itinerary_route_legs_trip_order_idx
    ON itinerary_route_legs (account_id, trip_id, from_item_id, to_item_id);

CREATE TABLE trip_route_summaries (
    account_id             uuid        NOT NULL,
    trip_id                uuid        NOT NULL,
    revision               bigint      NOT NULL DEFAULT 1,
    status                 varchar(16) NOT NULL DEFAULT 'stale',
    total_distance_meters  bigint,
    total_duration_seconds bigint,
    ready_leg_count        integer     NOT NULL DEFAULT 0,
    total_leg_count        integer     NOT NULL DEFAULT 0,
    missing_point_count    integer     NOT NULL DEFAULT 0,
    calculated_at          timestamptz,
    updated_at             timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT trip_route_summaries_pk PRIMARY KEY (account_id, trip_id),
    CONSTRAINT trip_route_summaries_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id) ON DELETE CASCADE,
    CONSTRAINT trip_route_summaries_revision_positive CHECK (revision > 0),
    CONSTRAINT trip_route_summaries_status_enum CHECK (status IN ('stale', 'calculating', 'ready', 'incomplete', 'empty')),
    CONSTRAINT trip_route_summaries_distance_nonnegative CHECK (total_distance_meters IS NULL OR total_distance_meters >= 0),
    CONSTRAINT trip_route_summaries_duration_nonnegative CHECK (total_duration_seconds IS NULL OR total_duration_seconds >= 0),
    CONSTRAINT trip_route_summaries_counts_nonnegative CHECK (ready_leg_count >= 0 AND total_leg_count >= 0 AND missing_point_count >= 0),
    CONSTRAINT trip_route_summaries_ready_complete CHECK (
        status <> 'ready' OR (total_distance_meters IS NOT NULL AND total_duration_seconds IS NOT NULL AND ready_leg_count = total_leg_count AND missing_point_count = 0)
    )
);

INSERT INTO trip_route_summaries (account_id, trip_id)
SELECT account_id, id FROM trips;

-- +goose Down
DROP TABLE trip_route_summaries;
DROP TABLE itinerary_route_legs;
ALTER TABLE trips
    DROP CONSTRAINT trips_route_short_distance_range,
    DROP CONSTRAINT trips_route_short_mode_enum,
    DROP COLUMN route_short_distance_meters,
    DROP COLUMN route_short_mode;
