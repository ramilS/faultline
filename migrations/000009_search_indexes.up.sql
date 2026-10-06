CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- issues_list_idx orders (project_id, status, last_seen), so with status absent
-- or multi-valued it no longer supplies the ORDER BY and Postgres sorts. Every
-- list query also filters merged_into_id IS NULL, so the partial index is both
-- smaller and exactly matched to the predicate.
CREATE INDEX issues_stream_idx ON issues (project_id, last_seen DESC)
    WHERE merged_into_id IS NULL;

CREATE INDEX issues_title_trgm_idx ON issues USING gin (title gin_trgm_ops);
CREATE INDEX issues_culprit_trgm_idx ON issues USING gin (culprit gin_trgm_ops);
