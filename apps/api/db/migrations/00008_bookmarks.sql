-- P8-B12: reader bookmarks ("收藏"). One row per (user, article); purely additive, so rolling the
-- application back to an older release keeps working (the old code never reads this table).

-- +goose Up
CREATE TABLE article_bookmarks (
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    article_id uuid        NOT NULL REFERENCES articles (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, article_id)
);
-- "My bookmarks" lists newest first.
CREATE INDEX article_bookmarks_user_created_idx ON article_bookmarks (user_id, created_at DESC);
-- Cascading deletes of an article look rows up by article.
CREATE INDEX article_bookmarks_article_idx ON article_bookmarks (article_id);

-- +goose Down
DROP TABLE article_bookmarks;
