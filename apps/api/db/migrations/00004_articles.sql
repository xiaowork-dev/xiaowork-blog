-- DD §5.1–5.4, 5.7–5.9 and §7.2–7.3: article identity, the single editable draft, the single public snapshot,
-- slug registry, draft/public tag and media relations, and view metrics.
-- Draft-side tables reference article_drafts and public-side tables reference article_publications, so
-- draft edits can never write public relations by accident (DD §1 constraint 1).

-- +goose Up
CREATE TABLE articles (
    id                            uuid        PRIMARY KEY,
    author_id                     uuid        NOT NULL REFERENCES users (id),
    status                        varchar(16) NOT NULL DEFAULT 'DRAFT',
    first_published_at            timestamptz,
    public_updated_at             timestamptz,
    last_published_draft_revision bigint,
    created_at                    timestamptz NOT NULL DEFAULT now(),
    updated_at                    timestamptz NOT NULL DEFAULT now(),
    trashed_at                    timestamptz,
    withdrawn_at                  timestamptz,
    CONSTRAINT articles_status_check CHECK (status IN ('DRAFT', 'PUBLISHED', 'WITHDRAWN', 'TRASHED')),
    CONSTRAINT articles_published_has_first_publish CHECK (
        status NOT IN ('PUBLISHED', 'WITHDRAWN') OR first_published_at IS NOT NULL
    ),
    CONSTRAINT articles_trashed_has_time CHECK (status <> 'TRASHED' OR trashed_at IS NOT NULL),
    -- first_published_at and last_published_draft_revision are both set by the first successful publish.
    CONSTRAINT articles_publish_markers_together CHECK (
        (first_published_at IS NULL) = (last_published_draft_revision IS NULL)
    ),
    CONSTRAINT articles_public_updated_requires_publish CHECK (
        public_updated_at IS NULL OR first_published_at IS NOT NULL
    ),
    CONSTRAINT articles_last_revision_positive CHECK (
        last_published_draft_revision IS NULL OR last_published_draft_revision > 0
    )
);
CREATE INDEX articles_public_list_idx ON articles (status, first_published_at DESC, id DESC);
CREATE INDEX articles_author_updated_idx ON articles (author_id, updated_at DESC);

CREATE TABLE article_drafts (
    article_id       uuid         PRIMARY KEY REFERENCES articles (id),
    revision         bigint       NOT NULL DEFAULT 1,
    title            varchar(200) NOT NULL DEFAULT '',
    summary          text,
    body_markdown    text         NOT NULL DEFAULT '',
    candidate_slug   varchar(160),
    category_id      uuid         REFERENCES categories (id),
    cover_media_id   uuid         REFERENCES media_assets (id),
    seo_title        varchar(200),
    seo_description  varchar(320),
    comments_enabled boolean      NOT NULL DEFAULT TRUE,
    saved_at         timestamptz  NOT NULL DEFAULT now(),
    last_edited_by   uuid         NOT NULL REFERENCES users (id),
    CONSTRAINT article_drafts_revision_positive CHECK (revision > 0),
    -- Drafts may hold a slug another article currently uses; uniqueness is decided at publish time (API §5.2).
    CONSTRAINT article_drafts_candidate_slug_form CHECK (
        candidate_slug IS NULL OR (candidate_slug <> '' AND candidate_slug !~ '[[:space:]/?#]')
    )
);
CREATE INDEX article_drafts_category_idx ON article_drafts (category_id) WHERE category_id IS NOT NULL;
CREATE INDEX article_drafts_cover_media_idx ON article_drafts (cover_media_id) WHERE cover_media_id IS NOT NULL;

-- Global slug registry: current and historical slugs share one namespace and are never reused.
CREATE TABLE article_slugs (
    slug       varchar(160) PRIMARY KEY,
    article_id uuid         NOT NULL REFERENCES articles (id),
    is_current boolean      NOT NULL DEFAULT FALSE,
    created_at timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT article_slugs_slug_form CHECK (slug <> '' AND slug !~ '[[:space:]/?#]'),
    -- Target for article_publications' composite FK.
    CONSTRAINT article_slugs_slug_article_key UNIQUE (slug, article_id)
);
CREATE UNIQUE INDEX article_slugs_one_current_per_article ON article_slugs (article_id) WHERE is_current;

CREATE TABLE article_publications (
    article_id           uuid         PRIMARY KEY REFERENCES articles (id),
    publication_revision bigint       NOT NULL,
    source_draft_revision bigint      NOT NULL,
    title                varchar(200) NOT NULL,
    summary              text         NOT NULL,
    body_markdown        text         NOT NULL,
    body_plain_text      text         NOT NULL,
    slug                 varchar(160) NOT NULL,
    category_id          uuid         REFERENCES categories (id),
    cover_media_id       uuid         REFERENCES media_assets (id),
    seo_title            varchar(200),
    seo_description      varchar(320),
    comments_enabled     boolean      NOT NULL,
    published_at         timestamptz  NOT NULL,
    CONSTRAINT article_publications_revisions_positive CHECK (publication_revision > 0 AND source_draft_revision > 0),
    -- Publishing an empty title or body is forbidden (DD §5.2, API §5.3).
    CONSTRAINT article_publications_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT article_publications_body_not_blank CHECK (btrim(body_markdown) <> ''),
    -- The public slug must be registered in article_slugs to this same article.
    CONSTRAINT article_publications_slug_registered
        FOREIGN KEY (slug, article_id) REFERENCES article_slugs (slug, article_id)
);
CREATE UNIQUE INDEX article_publications_slug_key ON article_publications (slug);
CREATE INDEX article_publications_category_idx ON article_publications (category_id) WHERE category_id IS NOT NULL;
CREATE INDEX article_publications_cover_media_idx ON article_publications (cover_media_id) WHERE cover_media_id IS NOT NULL;

CREATE TABLE article_draft_tags (
    article_id uuid NOT NULL REFERENCES article_drafts (article_id),
    tag_id     uuid NOT NULL REFERENCES tags (id),
    PRIMARY KEY (article_id, tag_id)
);
CREATE INDEX article_draft_tags_tag_idx ON article_draft_tags (tag_id, article_id);

CREATE TABLE article_publication_tags (
    article_id uuid NOT NULL REFERENCES article_publications (article_id),
    tag_id     uuid NOT NULL REFERENCES tags (id),
    PRIMARY KEY (article_id, tag_id)
);
CREATE INDEX article_publication_tags_tag_idx ON article_publication_tags (tag_id, article_id);

CREATE TABLE article_draft_media (
    article_id uuid NOT NULL REFERENCES article_drafts (article_id),
    media_id   uuid NOT NULL REFERENCES media_assets (id),
    PRIMARY KEY (article_id, media_id)
);
CREATE INDEX article_draft_media_media_idx ON article_draft_media (media_id);

CREATE TABLE article_publication_media (
    article_id uuid NOT NULL REFERENCES article_publications (article_id),
    media_id   uuid NOT NULL REFERENCES media_assets (id),
    PRIMARY KEY (article_id, media_id)
);
CREATE INDEX article_publication_media_media_idx ON article_publication_media (media_id);

CREATE TABLE article_metrics (
    article_id uuid        PRIMARY KEY REFERENCES articles (id),
    view_count bigint      NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT article_metrics_view_count_non_negative CHECK (view_count >= 0)
);

-- +goose Down
DROP TABLE article_metrics;
DROP TABLE article_publication_media;
DROP TABLE article_draft_media;
DROP TABLE article_publication_tags;
DROP TABLE article_draft_tags;
DROP TABLE article_publications;
DROP TABLE article_slugs;
DROP TABLE article_drafts;
DROP TABLE articles;
