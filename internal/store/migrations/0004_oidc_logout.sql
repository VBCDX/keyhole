-- Single sign-out (issue 9). A Zitadel session records the provider's
-- session ID (the ID token's "sid") and subject, so a back-channel logout
-- token can find every Keyhole session it ends.
ALTER TABLE sessions ADD COLUMN oidc_sid TEXT;
ALTER TABLE sessions ADD COLUMN oidc_sub TEXT;

CREATE INDEX sessions_oidc_sid ON sessions (oidc_sid);
CREATE INDEX sessions_oidc_sub ON sessions (oidc_sub);

-- The jti of every logout token accepted, kept until the token expires, so
-- a replayed token is refused.
CREATE TABLE oidc_logout_jtis (
    jti        TEXT PRIMARY KEY,
    expires_at BIGINT NOT NULL
);

CREATE INDEX oidc_logout_jtis_expires_at ON oidc_logout_jtis (expires_at);
