-- An SSO-provisioned account has no password. Reads COALESCE the column to the
-- empty string, which internal/auth.Service.Login treats as "cannot sign in
-- with a password" on the same code path as an unknown email.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

CREATE TABLE user_identities (
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider   text   NOT NULL,
    subject    text   NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, subject)
);

CREATE INDEX user_identities_user_idx ON user_identities (user_id);
