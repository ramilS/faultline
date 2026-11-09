DELETE FROM notification_deliveries WHERE trigger = 'escalation';
ALTER TABLE notification_deliveries DROP CONSTRAINT notification_deliveries_trigger_check;
ALTER TABLE notification_deliveries
    ADD CONSTRAINT notification_deliveries_trigger_check CHECK (trigger IN ('new', 'regression'));

ALTER TABLE issues
    DROP CONSTRAINT issues_ignore_threshold_with_count_target,
    DROP CONSTRAINT issues_single_ignore_condition,
    DROP COLUMN escalated_at,
    DROP COLUMN ignore_threshold,
    DROP COLUMN ignore_until_users_seen,
    DROP COLUMN ignore_until_times_seen,
    DROP COLUMN ignored_until;
