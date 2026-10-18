-- Retention keeps each project's most recently used mappings and native debug
-- files. An upload counts as a use, so existing rows start at their upload.
ALTER TABLE proguard_mappings ADD COLUMN last_used_at timestamptz;
UPDATE proguard_mappings SET last_used_at = uploaded_at;
ALTER TABLE proguard_mappings
    ALTER COLUMN last_used_at SET NOT NULL,
    ALTER COLUMN last_used_at SET DEFAULT now();
CREATE INDEX proguard_mappings_recency_idx ON proguard_mappings (project_id, last_used_at DESC, id DESC);

ALTER TABLE native_debug_file_blobs ADD COLUMN last_used_at timestamptz;
UPDATE native_debug_file_blobs SET last_used_at = created_at;
ALTER TABLE native_debug_file_blobs
    ALTER COLUMN last_used_at SET NOT NULL,
    ALTER COLUMN last_used_at SET DEFAULT now();
CREATE INDEX native_debug_file_blobs_recency_idx ON native_debug_file_blobs (project_id, last_used_at DESC, id DESC);
