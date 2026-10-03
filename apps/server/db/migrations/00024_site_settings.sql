-- +goose Up
CREATE TABLE admin_site_settings (
    id integer PRIMARY KEY CHECK (id = 1),
    share_base_url text NOT NULL DEFAULT '',
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO admin_site_settings(id) VALUES (1);

-- +goose Down
DROP TABLE admin_site_settings;
