ALTER TABLE issue_tag_values ADD COLUMN project_id bigint;

UPDATE issue_tag_values t
SET project_id = i.project_id
FROM issues i
WHERE i.id = t.issue_id;

ALTER TABLE issue_tag_values ALTER COLUMN project_id SET NOT NULL;

-- Serves the per-project facet endpoints, which run on the autocomplete path
-- and cannot afford a join back to issues on every keystroke.
CREATE INDEX itv_project_idx ON issue_tag_values (project_id, key, value);
