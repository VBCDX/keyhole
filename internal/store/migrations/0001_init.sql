-- Portable DDL: this file runs unchanged on Postgres and SQLite.
-- Timestamps are Unix milliseconds (BIGINT) so both databases store and
-- compare them the same way.

CREATE TABLE users (
    id         TEXT PRIMARY KEY,
    email      TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

-- One row per way a user can sign in. A local account is provider 'local',
-- issuer 'local' and subject = the lower-cased email; a Zitadel account is
-- provider 'oidc', issuer = the issuer URL and subject = the token's "sub".
CREATE TABLE identities (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    provider      TEXT NOT NULL,
    issuer        TEXT NOT NULL,
    subject       TEXT NOT NULL,
    password_hash TEXT,
    created_at    BIGINT NOT NULL,
    last_used_at  BIGINT,
    UNIQUE (provider, issuer, subject)
);

CREATE INDEX identities_user_id ON identities (user_id);

-- The primary key is the SHA-256 of the cookie value, never the value itself.
CREATE TABLE sessions (
    id_hash       TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    method        TEXT NOT NULL,
    csrf_token    TEXT NOT NULL,
    id_token_hint TEXT,
    created_at    BIGINT NOT NULL,
    expires_at    BIGINT NOT NULL
);

CREATE INDEX sessions_user_id ON sessions (user_id);
CREATE INDEX sessions_expires_at ON sessions (expires_at);
