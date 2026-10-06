DROP INDEX IF EXISTS issues_merged_into_idx;
ALTER TABLE issues DROP COLUMN IF EXISTS merged_into_id;
