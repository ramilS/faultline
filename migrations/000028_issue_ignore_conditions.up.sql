ALTER TABLE issues
    ADD COLUMN ignored_until timestamptz,
    ADD COLUMN ignore_until_times_seen bigint,
    ADD COLUMN ignore_until_users_seen bigint,
    ADD COLUMN ignore_threshold bigint,
    ADD COLUMN escalated_at timestamptz,
    ADD CONSTRAINT issues_single_ignore_condition
        CHECK (num_nonnulls(ignored_until, ignore_until_times_seen, ignore_until_users_seen) <= 1),
    ADD CONSTRAINT issues_ignore_threshold_with_count_target
        CHECK ((ignore_threshold IS NULL) = (ignore_until_times_seen IS NULL AND ignore_until_users_seen IS NULL));

ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_trigger_check;
ALTER TABLE notification_deliveries
    ADD CONSTRAINT notification_deliveries_trigger_check CHECK (trigger IN ('new', 'regression', 'escalation'));
