-- DD §4: users and authentication (users, auth_sessions, auth_tokens, auth_rate_windows).
-- user_profiles lives in 00002 because it references media_assets.

-- +goose Up
CREATE TABLE users (
    id                  uuid         PRIMARY KEY,
    email               varchar(254) NOT NULL,
    email_normalized    varchar(254) NOT NULL,
    password_hash       text         NOT NULL,
    nickname            varchar(40)  NOT NULL,
    role                varchar(16)  NOT NULL DEFAULT 'USER',
    status              varchar(16)  NOT NULL DEFAULT 'ACTIVE',
    email_verified_at   timestamptz,
    password_changed_at timestamptz,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    updated_at          timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT users_email_normalized_key UNIQUE (email_normalized),
    CONSTRAINT users_email_not_blank CHECK (btrim(email) <> ''),
    -- Product-level case-insensitive uniqueness (DD §4.1): the normalized form must already be trimmed and lower-cased.
    CONSTRAINT users_email_normalized_form CHECK (
        email_normalized <> '' AND email_normalized = lower(btrim(email_normalized))
    ),
    CONSTRAINT users_password_hash_not_blank CHECK (password_hash <> ''),
    CONSTRAINT users_nickname_length CHECK (char_length(btrim(nickname)) BETWEEN 1 AND 40),
    CONSTRAINT users_role_check CHECK (role IN ('USER', 'ADMIN')),
    CONSTRAINT users_status_check CHECK (status IN ('ACTIVE', 'DISABLED'))
);

CREATE TABLE auth_sessions (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id),
    token_digest bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    CONSTRAINT auth_sessions_token_digest_key UNIQUE (token_digest),
    -- SHA-256 digest only; the raw session token is never stored.
    CONSTRAINT auth_sessions_token_digest_len CHECK (octet_length(token_digest) = 32),
    CONSTRAINT auth_sessions_expiry_after_create CHECK (expires_at > created_at)
);
CREATE INDEX auth_sessions_user_expires_idx ON auth_sessions (user_id, expires_at);

CREATE TABLE auth_tokens (
    id                      uuid         PRIMARY KEY,
    user_id                 uuid         NOT NULL REFERENCES users (id),
    purpose                 varchar(24)  NOT NULL,
    token_digest            bytea        NOT NULL,
    target_email            varchar(254),
    target_email_normalized varchar(254),
    created_at              timestamptz  NOT NULL DEFAULT now(),
    expires_at              timestamptz  NOT NULL,
    consumed_at             timestamptz,
    revoked_at              timestamptz,
    CONSTRAINT auth_tokens_token_digest_key UNIQUE (token_digest),
    CONSTRAINT auth_tokens_token_digest_len CHECK (octet_length(token_digest) = 32),
    CONSTRAINT auth_tokens_purpose_check CHECK (purpose IN ('VERIFY_EMAIL', 'RESET_PASSWORD', 'CHANGE_EMAIL')),
    CONSTRAINT auth_tokens_expiry_after_create CHECK (expires_at > created_at),
    CONSTRAINT auth_tokens_change_email_target CHECK (
        purpose <> 'CHANGE_EMAIL' OR (target_email IS NOT NULL AND target_email_normalized IS NOT NULL)
    ),
    CONSTRAINT auth_tokens_target_normalized_form CHECK (
        target_email_normalized IS NULL OR target_email_normalized = lower(btrim(target_email_normalized))
    )
);
CREATE INDEX auth_tokens_user_purpose_created_idx ON auth_tokens (user_id, purpose, created_at DESC);
CREATE INDEX auth_tokens_expires_idx ON auth_tokens (expires_at);

CREATE TABLE auth_rate_windows (
    action_key        varchar(32) NOT NULL,
    subject_digest    bytea       NOT NULL,
    window_started_at timestamptz NOT NULL,
    request_count     integer     NOT NULL DEFAULT 0,
    expires_at        timestamptz NOT NULL,
    PRIMARY KEY (action_key, subject_digest, window_started_at),
    CONSTRAINT auth_rate_windows_action_not_blank CHECK (btrim(action_key) <> ''),
    CONSTRAINT auth_rate_windows_count_non_negative CHECK (request_count >= 0),
    CONSTRAINT auth_rate_windows_expiry_after_start CHECK (expires_at > window_started_at)
);
CREATE INDEX auth_rate_windows_expires_idx ON auth_rate_windows (expires_at);

-- +goose Down
DROP TABLE auth_rate_windows;
DROP TABLE auth_tokens;
DROP TABLE auth_sessions;
DROP TABLE users;
