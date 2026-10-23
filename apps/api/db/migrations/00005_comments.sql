-- DD §6.1 comments: bound to the stable article id, published immediately (no review state), tombstones on delete.

-- +goose Up
CREATE TABLE comments (
    id         uuid        PRIMARY KEY,
    article_id uuid        NOT NULL REFERENCES articles (id),
    author_id  uuid        NOT NULL REFERENCES users (id),
    parent_id  uuid,
    root_id    uuid,
    body_text  text,
    status     varchar(20) NOT NULL DEFAULT 'VISIBLE',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    hidden_at  timestamptz,
    CONSTRAINT comments_article_id_id_key UNIQUE (article_id, id),
    -- Composite FKs keep parent and root inside the same article (DD §6.1, DB-11).
    CONSTRAINT comments_parent_same_article
        FOREIGN KEY (article_id, parent_id) REFERENCES comments (article_id, id),
    CONSTRAINT comments_root_same_article
        FOREIGN KEY (article_id, root_id) REFERENCES comments (article_id, id),
    CONSTRAINT comments_status_check CHECK (status IN ('VISIBLE', 'HIDDEN', 'DELETED_AUTHOR', 'DELETED_ADMIN')),
    -- Root comments have neither parent nor root; replies have both.
    CONSTRAINT comments_parent_root_pairing CHECK ((parent_id IS NULL) = (root_id IS NULL)),
    CONSTRAINT comments_not_self_parent CHECK (parent_id IS NULL OR (parent_id <> id AND root_id <> id)),
    -- Visible/hidden comments keep text (hidden is recoverable); limit follows API §5.5 default of 2000 chars.
    CONSTRAINT comments_body_when_present CHECK (
        status NOT IN ('VISIBLE', 'HIDDEN')
        OR (body_text IS NOT NULL AND btrim(body_text) <> '' AND char_length(body_text) <= 2000)
    ),
    -- Deleted comments are tombstones: text cleared, deletion time recorded.
    CONSTRAINT comments_tombstone_cleared CHECK (
        status NOT IN ('DELETED_AUTHOR', 'DELETED_ADMIN') OR (body_text IS NULL AND deleted_at IS NOT NULL)
    ),
    CONSTRAINT comments_hidden_has_time CHECK (status <> 'HIDDEN' OR hidden_at IS NOT NULL)
);
CREATE INDEX comments_article_parent_created_idx ON comments (article_id, parent_id, created_at DESC, id DESC);
CREATE INDEX comments_article_root_created_idx ON comments (article_id, root_id, created_at ASC, id ASC);
CREATE INDEX comments_author_created_idx ON comments (author_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE comments;
