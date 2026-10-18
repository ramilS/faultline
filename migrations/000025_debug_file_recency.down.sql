DROP INDEX native_debug_file_blobs_recency_idx;
ALTER TABLE native_debug_file_blobs DROP COLUMN last_used_at;
DROP INDEX proguard_mappings_recency_idx;
ALTER TABLE proguard_mappings DROP COLUMN last_used_at;
