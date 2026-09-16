-- +goose Up
-- 路线轨迹仅在地图打开时按需计算；数据库只保存方式、距离、时长和状态。

ALTER TABLE itinerary_route_legs
    DROP CONSTRAINT itinerary_route_legs_ready_complete,
    DROP CONSTRAINT itinerary_route_legs_path_array,
    DROP COLUMN path,
    ADD CONSTRAINT itinerary_route_legs_ready_complete CHECK (
        status <> 'ready' OR (route_distance_meters IS NOT NULL AND route_duration_seconds IS NOT NULL AND calculated_at IS NOT NULL)
    );

-- +goose Down
ALTER TABLE itinerary_route_legs
    DROP CONSTRAINT itinerary_route_legs_ready_complete,
    ADD COLUMN path jsonb;

UPDATE itinerary_route_legs
SET path = '[]'::jsonb
WHERE status = 'ready';

ALTER TABLE itinerary_route_legs
    ADD CONSTRAINT itinerary_route_legs_path_array CHECK (path IS NULL OR jsonb_typeof(path) = 'array'),
    ADD CONSTRAINT itinerary_route_legs_ready_complete CHECK (
        status <> 'ready' OR (route_distance_meters IS NOT NULL AND route_duration_seconds IS NOT NULL AND path IS NOT NULL AND calculated_at IS NOT NULL)
    );
