-- The event that caused a delivery, so the notification links to it rather
-- than to whatever the issue's newest event is by the time it is opened.
-- Nullable: deliveries queued before this column existed carry none, and
-- their link falls back to the issue alone.
ALTER TABLE notification_deliveries ADD COLUMN event_id uuid;
