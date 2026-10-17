-- The most recent time a resolved issue came back, and the release the event
-- that brought it back carried. Set by ingest when it reopens an issue and
-- cleared when someone resolves or ignores it again, so a non-null value
-- means "this issue is open because it regressed", which the issue page
-- shows first.
ALTER TABLE issues
    ADD COLUMN regressed_at timestamptz,
    ADD COLUMN regressed_in_release text;
