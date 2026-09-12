-- +goose Up
-- 私有文件资产（含当前上传尝试）。见数据库设计 v0.3 表 14。

CREATE TABLE assets (
    id                   uuid PRIMARY KEY,
    account_id           uuid          NOT NULL REFERENCES accounts (id),
    version              bigint        NOT NULL DEFAULT 1,
    created_at           timestamptz   NOT NULL DEFAULT now(),
    updated_at           timestamptz   NOT NULL DEFAULT now(),
    deleted_at           timestamptz,
    scope                varchar(16)   NOT NULL,
    trip_id              uuid,
    original_name        varchar(255)  NOT NULL,
    status               varchar(16)   NOT NULL DEFAULT 'uploading',
    upload_attempt       integer       NOT NULL DEFAULT 1,
    staging_object_key   text,
    expected_size        bigint,
    declared_media_type  varchar(100),
    client_sha256        bytea,
    upload_expires_at    timestamptz,
    confirmed_at         timestamptz,
    media_type           varchar(100),
    byte_size            bigint,
    sha256               bytea,
    object_key           text,
    thumbnail_object_key text,
    thumbnail_status     varchar(16)   NOT NULL DEFAULT 'none',
    width                integer,
    height               integer,
    exif_taken_at_local  timestamp(0) without time zone,
    exif_latitude        numeric(9,6),
    exif_longitude       numeric(10,6),
    error_code           varchar(64),
    unreferenced_since   timestamptz,
    CONSTRAINT assets_account_id_unique UNIQUE (account_id, id),
    CONSTRAINT assets_account_trip_id_unique UNIQUE (account_id, trip_id, id),
    CONSTRAINT assets_trip_fk FOREIGN KEY (account_id, trip_id) REFERENCES trips (account_id, id),
    CONSTRAINT assets_staging_key_unique UNIQUE (staging_object_key),
    CONSTRAINT assets_object_key_unique UNIQUE (object_key),
    CONSTRAINT assets_thumbnail_key_unique UNIQUE (thumbnail_object_key),
    CONSTRAINT assets_version_positive CHECK (version > 0),
    CONSTRAINT assets_scope_enum CHECK (scope IN ('trip', 'avatar')),
    CONSTRAINT assets_scope_trip_pair CHECK ((scope = 'trip') = (trip_id IS NOT NULL)),
    CONSTRAINT assets_status_enum CHECK (status IN ('uploading', 'processing', 'ready', 'failed')),
    CONSTRAINT assets_thumbnail_status_enum CHECK (thumbnail_status IN ('none', 'processing', 'ready', 'failed')),
    CONSTRAINT assets_original_name_not_blank CHECK (btrim(original_name) <> ''),
    CONSTRAINT assets_upload_attempt_positive CHECK (upload_attempt > 0),
    CONSTRAINT assets_expected_size_positive CHECK (expected_size IS NULL OR expected_size > 0),
    CONSTRAINT assets_byte_size_nonnegative CHECK (byte_size IS NULL OR byte_size >= 0),
    CONSTRAINT assets_client_sha256_len CHECK (client_sha256 IS NULL OR octet_length(client_sha256) = 32),
    CONSTRAINT assets_sha256_len CHECK (sha256 IS NULL OR octet_length(sha256) = 32),
    CONSTRAINT assets_dimensions_positive CHECK ((width IS NULL OR width > 0) AND (height IS NULL OR height > 0)),
    CONSTRAINT assets_exif_coordinates_pair CHECK ((exif_latitude IS NULL) = (exif_longitude IS NULL)),
    CONSTRAINT assets_exif_latitude_range CHECK (exif_latitude IS NULL OR exif_latitude BETWEEN -90 AND 90),
    CONSTRAINT assets_exif_longitude_range CHECK (exif_longitude IS NULL OR exif_longitude BETWEEN -180 AND 180),
    CONSTRAINT assets_uploading_requires_staging CHECK (
        status <> 'uploading'
        OR (staging_object_key IS NOT NULL AND expected_size IS NOT NULL AND declared_media_type IS NOT NULL AND upload_expires_at IS NOT NULL)
    ),
    CONSTRAINT assets_ready_requires_object CHECK (
        status <> 'ready'
        OR (object_key IS NOT NULL AND media_type IS NOT NULL AND byte_size IS NOT NULL AND sha256 IS NOT NULL AND staging_object_key IS NULL)
    ),
    CONSTRAINT assets_thumbnail_ready_requires_key CHECK (thumbnail_status <> 'ready' OR thumbnail_object_key IS NOT NULL),
    CONSTRAINT assets_updated_not_before_created CHECK (updated_at >= created_at)
);

CREATE INDEX assets_trip_created_idx ON assets (account_id, trip_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;
CREATE INDEX assets_upload_expiry_idx ON assets (upload_expires_at) WHERE status = 'uploading';
CREATE INDEX assets_unreferenced_idx ON assets (unreferenced_since) WHERE deleted_at IS NULL AND unreferenced_since IS NOT NULL;

-- +goose Down
DROP TABLE assets;
