ALTER TABLE issues ADD COLUMN merged_into_id bigint REFERENCES issues(id);
CREATE INDEX issues_merged_into_idx ON issues (merged_into_id) WHERE merged_into_id IS NOT NULL;
