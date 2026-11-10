ALTER TABLE notification_channels
    ADD COLUMN proxy_url text NOT NULL DEFAULT '',
    ADD CONSTRAINT notification_channels_proxy_telegram_only CHECK (proxy_url = '' OR kind = 'telegram');
