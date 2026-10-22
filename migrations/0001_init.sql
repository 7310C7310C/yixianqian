-- Core schema for the yixianqian P0 slice.
-- All identifiers are generated in Go or by PostgreSQL identity columns; no extensions required.

CREATE TABLE users (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    legacy_user_id     text UNIQUE,
    phone              text NOT NULL UNIQUE,
    name               text NOT NULL,
    status             text NOT NULL DEFAULT 'active',
    failed_login_count integer NOT NULL DEFAULT 0,
    last_failed_at     timestamptz,
    locked_until       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_status_check CHECK (status IN ('active', 'disabled')),
    CONSTRAINT users_phone_not_empty CHECK (phone <> ''),
    CONSTRAINT users_name_not_empty CHECK (name <> '')
);

CREATE TABLE auth_credentials (
    user_id       bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    password_hash text NOT NULL,
    algorithm     text NOT NULL DEFAULT 'argon2id',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   text NOT NULL UNIQUE,
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    ip           inet,
    user_agent   text
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE activation_tokens (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint REFERENCES users (id) ON DELETE CASCADE,
    phone       text,
    name        text,
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT activation_tokens_target_check CHECK (
        user_id IS NOT NULL OR (phone IS NOT NULL AND name IS NOT NULL)
    )
);

CREATE INDEX activation_tokens_user_id_idx ON activation_tokens (user_id);
CREATE INDEX activation_tokens_phone_idx ON activation_tokens (phone);

CREATE TABLE media (
    id            text PRIMARY KEY,
    user_id       bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    original_name text NOT NULL DEFAULT '',
    object_key    text NOT NULL UNIQUE,
    thumb_key     text NOT NULL,
    content_type  text NOT NULL,
    byte_size     bigint NOT NULL,
    thumb_size    bigint NOT NULL,
    width         integer NOT NULL,
    height        integer NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT media_byte_size_check CHECK (byte_size > 0),
    CONSTRAINT media_thumb_size_check CHECK (thumb_size > 0),
    CONSTRAINT media_dimensions_check CHECK (width > 0 AND height > 0),
    CONSTRAINT media_content_type_check CHECK (content_type IN ('image/jpeg', 'image/png'))
);

CREATE INDEX media_user_created_idx ON media (user_id, created_at DESC, id DESC);

CREATE TABLE notifications (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       text NOT NULL,
    title      text NOT NULL,
    body       text NOT NULL DEFAULT '',
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notifications_user_created_idx ON notifications (user_id, created_at DESC);

CREATE TABLE audit_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id bigint REFERENCES users (id) ON DELETE SET NULL,
    action        text NOT NULL,
    target_type   text NOT NULL DEFAULT '',
    target_id     text NOT NULL DEFAULT '',
    ip            inet,
    user_agent    text,
    metadata      jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_user_id, created_at DESC);
