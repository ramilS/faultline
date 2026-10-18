-- R8 mappings of real apps reach 90MB+; the cap now matches the 200MB
-- assembled-file limit of the chunked upload, like native debug files.
ALTER TABLE proguard_mappings DROP CONSTRAINT proguard_mappings_uncompressed_bytes_check;
ALTER TABLE proguard_mappings ADD CONSTRAINT proguard_mappings_uncompressed_bytes_check
    CHECK (uncompressed_bytes > 0 AND uncompressed_bytes <= 209715200);
