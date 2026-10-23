-- DD §9.1: search only the public snapshot, using pg_trgm GIN indexes for ILIKE.
-- pg_trgm is a trusted extension, so the database owner can create it without superuser.
-- Index choice must still be validated against real Chinese/English samples (DB-16, P5).

-- +goose Up
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX article_publications_title_trgm_idx ON article_publications USING gin (title gin_trgm_ops);
CREATE INDEX article_publications_summary_trgm_idx ON article_publications USING gin (summary gin_trgm_ops);
CREATE INDEX article_publications_body_trgm_idx ON article_publications USING gin (body_plain_text gin_trgm_ops);

-- +goose Down
DROP INDEX article_publications_body_trgm_idx;
DROP INDEX article_publications_summary_trgm_idx;
DROP INDEX article_publications_title_trgm_idx;
DROP EXTENSION IF EXISTS pg_trgm;
