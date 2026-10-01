-- +goose Up
ALTER TABLE itinerary_items
    ADD COLUMN footprint_excluded boolean NOT NULL DEFAULT false,
    ADD COLUMN poi_id varchar(128) NOT NULL DEFAULT '';

-- 只缓存坐标对应的公共行政区划，不保存账号或旅行关联。
CREATE TABLE dashboard_regions (
    latitude numeric(9,6) NOT NULL,
    longitude numeric(10,6) NOT NULL,
    province_code varchar(6) NOT NULL,
    province_name varchar(100) NOT NULL,
    city_code varchar(6) NOT NULL,
    city_name varchar(100) NOT NULL,
    resolved_at timestamptz NOT NULL,
    PRIMARY KEY (latitude, longitude)
);

-- +goose Down
DROP TABLE dashboard_regions;
ALTER TABLE itinerary_items DROP COLUMN poi_id, DROP COLUMN footprint_excluded;
