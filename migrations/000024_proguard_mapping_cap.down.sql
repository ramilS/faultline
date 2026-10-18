-- Fails while a stored mapping exceeds the old 100MB cap; delete those first.
ALTER TABLE proguard_mappings DROP CONSTRAINT proguard_mappings_uncompressed_bytes_check;
ALTER TABLE proguard_mappings ADD CONSTRAINT proguard_mappings_uncompressed_bytes_check
    CHECK (uncompressed_bytes > 0 AND uncompressed_bytes <= 104857600);
