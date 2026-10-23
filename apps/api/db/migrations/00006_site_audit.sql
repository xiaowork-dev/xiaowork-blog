-- DD §7.4 static_page_media and DD §8: site settings, social/navigation links, about page, admin audit log.

-- +goose Up
CREATE TABLE site_settings (
    id                      smallint     PRIMARY KEY DEFAULT 1,
    site_title              varchar(120) NOT NULL,
    site_description        varchar(320) NOT NULL DEFAULT '',
    profile_name            varchar(80)  NOT NULL,
    profile_bio             varchar(240) NOT NULL DEFAULT '',
    profile_avatar_media_id uuid         REFERENCES media_assets (id),
    logo_media_id           uuid         REFERENCES media_assets (id),
    favicon_media_id        uuid         REFERENCES media_assets (id),
    contact_email           varchar(254),
    default_seo_title       varchar(200),
    default_seo_description varchar(320),
    updated_at              timestamptz  NOT NULL DEFAULT now(),
    -- Singleton row (DD §8.1). Secrets (SMTP, session keys, DB URL) never live here.
    CONSTRAINT site_settings_singleton CHECK (id = 1),
    CONSTRAINT site_settings_title_not_blank CHECK (btrim(site_title) <> '')
);
CREATE INDEX site_settings_profile_avatar_idx ON site_settings (profile_avatar_media_id) WHERE profile_avatar_media_id IS NOT NULL;
CREATE INDEX site_settings_logo_idx ON site_settings (logo_media_id) WHERE logo_media_id IS NOT NULL;
CREATE INDEX site_settings_favicon_idx ON site_settings (favicon_media_id) WHERE favicon_media_id IS NOT NULL;

-- Deployment initial data (DD §8.1): placeholder values, edited later in the admin UI.
INSERT INTO site_settings (id, site_title, profile_name) VALUES (1, 'XiaoWork Blog', 'XiaoWork');

CREATE TABLE site_social_links (
    id         uuid        PRIMARY KEY,
    icon_key   varchar(40) NOT NULL,
    label      varchar(80) NOT NULL,
    url        text        NOT NULL,
    sort_order integer     NOT NULL DEFAULT 0,
    is_enabled boolean     NOT NULL DEFAULT TRUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT site_social_links_icon_not_blank CHECK (btrim(icon_key) <> ''),
    CONSTRAINT site_social_links_label_not_blank CHECK (btrim(label) <> ''),
    -- Only https: and mailto: (API §5.8); javascript:/data: are rejected.
    CONSTRAINT site_social_links_url_scheme CHECK (url ~* '^(https://[^[:space:]]+|mailto:[^[:space:]]+)$')
);
CREATE INDEX site_social_links_enabled_sort_idx ON site_social_links (is_enabled, sort_order, id);

CREATE TABLE site_navigation_links (
    id         uuid        PRIMARY KEY,
    label      varchar(60) NOT NULL,
    link_type  varchar(12) NOT NULL,
    url        text        NOT NULL,
    sort_order integer     NOT NULL DEFAULT 0,
    is_enabled boolean     NOT NULL DEFAULT TRUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT site_navigation_links_label_not_blank CHECK (btrim(label) <> ''),
    CONSTRAINT site_navigation_links_type_check CHECK (link_type IN ('INTERNAL', 'EXTERNAL')),
    -- INTERNAL: site-relative path (not protocol-relative //); EXTERNAL: https only.
    CONSTRAINT site_navigation_links_url_form CHECK (
        (link_type = 'INTERNAL' AND url ~ '^/([^/[:space:]][^[:space:]]*)?$')
        OR (link_type = 'EXTERNAL' AND url ~* '^https://[^[:space:]]+$')
    )
);
CREATE INDEX site_navigation_links_enabled_sort_idx ON site_navigation_links (is_enabled, sort_order, id);

CREATE TABLE static_pages (
    page_key        varchar(32)  PRIMARY KEY,
    title           varchar(160) NOT NULL,
    body_markdown   text         NOT NULL,
    body_plain_text text         NOT NULL,
    updated_by      uuid         NOT NULL REFERENCES users (id),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    -- V1.0 only has the about page (DD §8.4); more pages require a design review.
    CONSTRAINT static_pages_key_check CHECK (page_key IN ('about'))
);

CREATE TABLE static_page_media (
    page_key varchar(32) NOT NULL REFERENCES static_pages (page_key),
    media_id uuid        NOT NULL REFERENCES media_assets (id),
    PRIMARY KEY (page_key, media_id)
);
CREATE INDEX static_page_media_media_idx ON static_page_media (media_id);

CREATE TABLE admin_audit_logs (
    id            uuid         PRIMARY KEY,
    actor_user_id uuid         NOT NULL REFERENCES users (id),
    action        varchar(64)  NOT NULL,
    object_type   varchar(40)  NOT NULL,
    object_key    varchar(256) NOT NULL,
    request_id    varchar(64),
    details       jsonb,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT admin_audit_logs_action_not_blank CHECK (btrim(action) <> ''),
    CONSTRAINT admin_audit_logs_details_object CHECK (details IS NULL OR jsonb_typeof(details) = 'object')
);
CREATE INDEX admin_audit_logs_created_idx ON admin_audit_logs (created_at DESC, id DESC);
CREATE INDEX admin_audit_logs_actor_created_idx ON admin_audit_logs (actor_user_id, created_at DESC);

-- +goose Down
DROP TABLE admin_audit_logs;
DROP TABLE static_page_media;
DROP TABLE static_pages;
DROP TABLE site_navigation_links;
DROP TABLE site_social_links;
DROP TABLE site_settings;
