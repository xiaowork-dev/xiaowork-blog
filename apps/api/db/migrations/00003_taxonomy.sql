-- DD §5.5 categories and §5.6 tags. No reserved "uncategorized" row (DD §5.5).

-- +goose Up
CREATE TABLE categories (
    id          uuid         PRIMARY KEY,
    name        varchar(60)  NOT NULL,
    slug        varchar(100) NOT NULL,
    description varchar(300),
    sort_order  integer      NOT NULL DEFAULT 0,
    is_active   boolean      NOT NULL DEFAULT TRUE,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT categories_slug_key UNIQUE (slug),
    CONSTRAINT categories_name_not_blank CHECK (btrim(name) <> ''),
    -- Slug syntax beyond this (character set, case) is validated by the API.
    CONSTRAINT categories_slug_form CHECK (slug <> '' AND slug !~ '[[:space:]/?#]')
);
CREATE INDEX categories_active_sort_idx ON categories (is_active, sort_order);

CREATE TABLE tags (
    id         uuid         PRIMARY KEY,
    name       varchar(60)  NOT NULL,
    slug       varchar(100) NOT NULL,
    sort_order integer      NOT NULL DEFAULT 0,
    is_active  boolean      NOT NULL DEFAULT TRUE,
    created_at timestamptz  NOT NULL DEFAULT now(),
    updated_at timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT tags_slug_key UNIQUE (slug),
    CONSTRAINT tags_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT tags_slug_form CHECK (slug <> '' AND slug !~ '[[:space:]/?#]')
);
CREATE INDEX tags_active_sort_idx ON tags (is_active, sort_order);

-- +goose Down
DROP TABLE tags;
DROP TABLE categories;
