ALTER TABLE notification_channels
    DROP CONSTRAINT notification_channels_proxy_telegram_only,
    DROP COLUMN proxy_url;
