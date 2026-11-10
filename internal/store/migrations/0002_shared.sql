-- The suite's shared records (mocks/schema/shared on the data-schema
-- branch), translated mechanically so Dispatch can carry the same tables:
--
--   * field names are the schema's, in snake_case; _id is id;
--   * a small fixed-shape object is flattened to <field>_<subfield>
--     (createdBy {kind, id} -> created_by_kind, created_by_id; a token
--     digest -> token_alg, token_hash, token_prefix, ...);
--   * maps, arrays of objects and variant objects are JSONB (the migration
--     loader turns JSONB into TEXT on SQLite);
--   * IDs are TEXT; instants are Unix milliseconds (BIGINT), as in 0001;
--   * Mongo-only details (TTL indexes, change-stream resume tokens, Redis)
--     are left out: a *_at expiry column is swept by Keyhole, not the DB.
--
-- Postgres and SQLite each run this file unchanged.

-- ---------------------------------------------------------------------------
-- Auth model. 0001's users, identities and sessions become the schema's
-- users, identities and sessions; nothing is duplicated.

ALTER TABLE users ADD COLUMN email_lower TEXT NOT NULL DEFAULT '';
UPDATE users SET email_lower = LOWER(email);
ALTER TABLE users ADD COLUMN email_verified_at BIGINT;
ALTER TABLE users ADD COLUMN status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending'));
ALTER TABLE users ADD COLUMN lock_lock_id TEXT;
ALTER TABLE users ADD COLUMN lock_locked_at BIGINT;
ALTER TABLE users ADD COLUMN last_sign_in_at BIGINT;
ALTER TABLE users ADD COLUMN locale TEXT;
ALTER TABLE users ADD COLUMN erased_at BIGINT;
-- Not unique, unlike the schema's emailLower: Keyhole links a sign-in to a
-- user by provider, connection and subject only, never by email, so a local
-- account and a Zitadel account may share an address.
CREATE INDEX users_email_lower ON users (email_lower);

-- connection_id is the schema's connectionId: the OIDC issuer URL for a
-- Zitadel identity, '' for a password identity (never NULL, so the unique
-- key below still holds for password accounts).
ALTER TABLE identities RENAME COLUMN issuer TO connection_id;
ALTER TABLE identities RENAME COLUMN created_at TO linked_at;
UPDATE identities SET provider = 'password', connection_id = '' WHERE provider = 'local';
ALTER TABLE identities ADD COLUMN connection_org_id TEXT;
ALTER TABLE identities ADD COLUMN email_at_link TEXT;

-- Keyhole's browser sessions stay keyed by the SHA-256 of the cookie and
-- keep the sign-in starter's method, csrf_token and id_token_hint. The
-- refresh-token family (CLI sessions) is ported as nullable refresh_*
-- columns so the table matches the shared model; nothing writes them yet.
ALTER TABLE sessions ADD COLUMN client TEXT NOT NULL DEFAULT 'web:keyhole';
-- amr is the schema's authentication-methods list; method stays as the
-- sign-in path Keyhole took ('local' or 'zitadel').
ALTER TABLE sessions ADD COLUMN amr JSONB NOT NULL DEFAULT '["pwd"]';
UPDATE sessions SET amr = '["sso"]' WHERE method = 'zitadel';
ALTER TABLE sessions ADD COLUMN acr TEXT;
ALTER TABLE sessions ADD COLUMN device TEXT;
ALTER TABLE sessions ADD COLUMN user_agent TEXT;
ALTER TABLE sessions ADD COLUMN ip TEXT;
ALTER TABLE sessions ADD COLUMN place TEXT;
ALTER TABLE sessions ADD COLUMN active_org_id TEXT;
ALTER TABLE sessions ADD COLUMN last_seen_at BIGINT;
ALTER TABLE sessions ADD COLUMN idle_expires_at BIGINT;
ALTER TABLE sessions ADD COLUMN revoked_at BIGINT;
ALTER TABLE sessions ADD COLUMN revoked_by_kind TEXT;
ALTER TABLE sessions ADD COLUMN revoked_by_id TEXT;
ALTER TABLE sessions ADD COLUMN revoke_reason TEXT CHECK (revoke_reason IN ('signout', 'signout_everywhere', 'support_lock', 'refresh_reuse', 'user_erased', 'password_changed'));
ALTER TABLE sessions ADD COLUMN refresh_family_id TEXT;
ALTER TABLE sessions ADD COLUMN refresh_current_alg TEXT;
ALTER TABLE sessions ADD COLUMN refresh_current_hash TEXT;
ALTER TABLE sessions ADD COLUMN refresh_current_pepper_version INTEGER;
ALTER TABLE sessions ADD COLUMN refresh_current_prefix TEXT;
ALTER TABLE sessions ADD COLUMN refresh_current_last4 TEXT;
ALTER TABLE sessions ADD COLUMN refresh_current_issued_at BIGINT;
ALTER TABLE sessions ADD COLUMN refresh_current_expires_at BIGINT;
ALTER TABLE sessions ADD COLUMN refresh_rotated_at BIGINT;
ALTER TABLE sessions ADD COLUMN refresh_reuse_detected_at BIGINT;

CREATE INDEX sessions_user_live ON sessions (user_id, revoked_at, last_seen_at);
CREATE UNIQUE INDEX sessions_refresh_hash ON sessions (refresh_current_hash)
    WHERE refresh_current_hash IS NOT NULL;

CREATE TABLE mfa_factors (
    id                   TEXT PRIMARY KEY,
    user_id              TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    type                 TEXT NOT NULL CHECK (type IN ('totp', 'webauthn', 'recovery_codes')),
    label                TEXT,
    -- secretRef: where the TOTP seed is sealed, never the seed.
    totp_secret_ref      JSONB,
    totp_digits          INTEGER,
    totp_period_seconds  INTEGER,
    webauthn_credential_id TEXT,
    webauthn_public_key  TEXT,
    webauthn_sign_count  BIGINT,
    webauthn_aaguid      TEXT,
    webauthn_transports  JSONB,
    -- [{hash (argon2id), usedAt}]
    recovery_codes       JSONB,
    created_at           BIGINT NOT NULL,
    last_used_at         BIGINT,
    disabled_at          BIGINT
);

CREATE INDEX mfa_factors_user_type ON mfa_factors (user_id, type);
CREATE UNIQUE INDEX mfa_factors_webauthn_cred ON mfa_factors (webauthn_credential_id)
    WHERE webauthn_credential_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Suite records.

CREATE TABLE orgs (
    id                   TEXT PRIMARY KEY,
    name                 TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    audit_retention_days INTEGER,
    created_at           BIGINT NOT NULL,
    created_by_kind      TEXT NOT NULL,
    created_by_id        TEXT NOT NULL,
    updated_at           BIGINT NOT NULL,
    updated_by_kind      TEXT,
    updated_by_id        TEXT,
    deleted_at           BIGINT,
    deleted_by_kind      TEXT,
    deleted_by_id        TEXT,
    rev                  BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX orgs_status ON orgs (status, id);

CREATE TABLE org_memberships (
    id              TEXT PRIMARY KEY,
    org_id          TEXT NOT NULL REFERENCES orgs (id),
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            TEXT NOT NULL,
    status          TEXT NOT NULL,
    suspended_from  TEXT,
    suspension_id   TEXT,
    invitation_id   TEXT,
    joined_at       BIGINT,
    created_at      BIGINT NOT NULL,
    updated_at      BIGINT NOT NULL,
    updated_by_kind TEXT,
    updated_by_id   TEXT,
    rev             BIGINT NOT NULL DEFAULT 0,
    UNIQUE (org_id, user_id),
    CHECK (role IN ('Owner', 'userAdmin', 'user')),
    CHECK (status IN ('active', 'invited', 'suspended')),
    CHECK (suspended_from IS NULL OR suspended_from IN ('active', 'invited')),
    CHECK (status <> 'suspended' OR (suspended_from IS NOT NULL AND suspension_id IS NOT NULL))
);

CREATE INDEX org_memberships_user_orgs ON org_memberships (user_id, status);
CREATE INDEX org_memberships_org_role_status ON org_memberships (org_id, role, status);

CREATE TABLE suspensions (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL REFERENCES orgs (id),
    user_id          TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    membership_id    TEXT NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('active', 'ended')),
    suspended_at     BIGINT NOT NULL,
    suspended_by_kind TEXT NOT NULL,
    suspended_by_id  TEXT NOT NULL,
    reason           TEXT,
    previous_status  TEXT NOT NULL CHECK (previous_status IN ('active', 'invited')),
    resumed_at       BIGINT,
    resumed_by_kind  TEXT,
    resumed_by_id    TEXT
);

CREATE INDEX suspensions_org_user_at ON suspensions (org_id, user_id, suspended_at);

CREATE TABLE invitations (
    id                   TEXT PRIMARY KEY,
    org_id               TEXT NOT NULL REFERENCES orgs (id),
    email                TEXT NOT NULL,
    email_lower          TEXT NOT NULL,
    role                 TEXT NOT NULL,
    -- [{workspaceId, role}]
    workspace_grants     JSONB NOT NULL,
    extensions           JSONB,
    -- au_inv_… digest only; the link token is shown once.
    token_alg            TEXT NOT NULL,
    token_hash           TEXT NOT NULL UNIQUE,
    token_pepper_version INTEGER,
    token_prefix         TEXT NOT NULL,
    token_last4          TEXT NOT NULL,
    token_issued_at      BIGINT NOT NULL,
    token_expires_at     BIGINT,
    status               TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'revoked', 'expired')),
    invited_at           BIGINT NOT NULL,
    invited_by_kind      TEXT NOT NULL,
    invited_by_id        TEXT NOT NULL,
    -- [{at, by}]
    resends              JSONB,
    expires_at           BIGINT NOT NULL,
    accepted_at          BIGINT,
    accepted_user_id     TEXT,
    revoked_at           BIGINT,
    revoked_by_kind      TEXT,
    revoked_by_id        TEXT,
    CHECK (role IN ('Owner', 'userAdmin', 'user'))
);

CREATE INDEX invitations_org_status_exp ON invitations (org_id, status, expires_at);
CREATE INDEX invitations_email_status ON invitations (email_lower, status);

CREATE TABLE support_locks (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status          TEXT NOT NULL CHECK (status IN ('active', 'lifted')),
    locked_at       BIGINT NOT NULL,
    locked_by_kind  TEXT NOT NULL,
    locked_by_id    TEXT NOT NULL,
    reason          TEXT NOT NULL,
    -- [{byUserId, orgId, at}]
    unlock_requests JSONB,
    lifted_at       BIGINT,
    lifted_by_kind  TEXT,
    lifted_by_id    TEXT
);

CREATE INDEX support_locks_user_status ON support_locks (user_id, status);

CREATE TABLE workspaces (
    id              TEXT PRIMARY KEY,
    org_id          TEXT NOT NULL REFERENCES orgs (id),
    name            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'deleted')),
    created_at      BIGINT NOT NULL,
    created_by_kind TEXT NOT NULL,
    created_by_id   TEXT NOT NULL,
    updated_at      BIGINT NOT NULL,
    updated_by_kind TEXT,
    updated_by_id   TEXT,
    deleted_at      BIGINT,
    deleted_by_kind TEXT,
    deleted_by_id   TEXT,
    rev             BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX workspaces_org_status_name ON workspaces (org_id, status, name);

CREATE TABLE agents (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL REFERENCES orgs (id),
    label            TEXT NOT NULL,
    label_lower      TEXT NOT NULL,
    created_at       BIGINT NOT NULL,
    created_by_kind  TEXT NOT NULL,
    created_by_id    TEXT NOT NULL,
    created_by_label TEXT,
    updated_at       BIGINT NOT NULL,
    retired_at       BIGINT,
    retired_by_kind  TEXT,
    retired_by_id    TEXT,
    rev              BIGINT NOT NULL DEFAULT 0,
    UNIQUE (org_id, label_lower)
);

CREATE INDEX agents_org_created ON agents (org_id, created_at);

-- member_id is a user (u_…) or an agent (ag_…), so it has no foreign key.
CREATE TABLE workspace_memberships (
    id                TEXT PRIMARY KEY,
    org_id            TEXT NOT NULL REFERENCES orgs (id),
    workspace_id      TEXT NOT NULL REFERENCES workspaces (id),
    member_kind       TEXT NOT NULL,
    member_id         TEXT NOT NULL,
    role              TEXT NOT NULL,
    added_at          BIGINT NOT NULL,
    added_by_kind     TEXT NOT NULL,
    added_by_id       TEXT NOT NULL,
    delegated_by_kind TEXT,
    delegated_by_id   TEXT,
    delegated_at      BIGINT,
    updated_at        BIGINT NOT NULL,
    rev               BIGINT NOT NULL DEFAULT 0,
    UNIQUE (org_id, workspace_id, member_kind, member_id),
    CHECK (member_kind IN ('user', 'agent')),
    CHECK (role IN ('member', 'workspace_admin')),
    -- Agents are always plain members of the shared record.
    CHECK (member_kind = 'user' OR role = 'member')
);

CREATE INDEX workspace_memberships_org_member ON workspace_memberships (org_id, member_kind, member_id);
CREATE INDEX workspace_memberships_org_ws_role ON workspace_memberships (org_id, workspace_id, role);

-- One table for the suite's shared rows (shared = TRUE) and Keyhole's own
-- rows (shared = FALSE); Mongo keeps them in two databases. History, so no
-- foreign keys: rows outlive what they describe.
CREATE TABLE audit_events (
    id              TEXT PRIMARY KEY,
    org_id          TEXT NOT NULL,
    workspace_id    TEXT,
    app             TEXT NOT NULL CHECK (app IN ('keyhole', 'dispatch', 'auth', 'suite')),
    shared          BOOLEAN NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('admin', 'support', 'request', 'blocked', 'error', 'connection',
                                                  'verification', 'message', 'receipt', 'webhook', 'access', 'context')),
    severity        TEXT NOT NULL CHECK (severity IN ('ok', 'blocked', 'warn', 'info')),
    at              BIGINT NOT NULL,
    actor_kind      TEXT NOT NULL,
    actor_id        TEXT,
    actor_label     TEXT NOT NULL,
    actor_via_kind  TEXT,
    actor_via_id    TEXT,
    actor_via_label TEXT,
    subject_kind    TEXT,
    subject_id      TEXT,
    subject_label   TEXT,
    object          TEXT NOT NULL,
    result          TEXT NOT NULL,
    reason          TEXT,
    trk             TEXT NOT NULL,
    correlation_id  TEXT,
    causation_id    TEXT,
    -- [{field, before, after}]
    changes         JSONB,
    -- App-only rows: keyhole/audit_detail.schema.json.
    detail          JSONB,
    link_label      TEXT,
    link_to         TEXT,
    expire_at       BIGINT NOT NULL,
    -- Shared rows are admin or support facts and never carry app detail.
    CHECK (actor_kind IN ('user', 'agent', 'support', 'system', 'connector', 'sidecar', 'listener')),
    CHECK (NOT shared OR (type IN ('admin', 'support') AND detail IS NULL AND link_label IS NULL AND link_to IS NULL))
);

CREATE INDEX audit_events_org_at ON audit_events (org_id, at);
CREATE INDEX audit_events_org_ws_at ON audit_events (org_id, workspace_id, at);
CREATE INDEX audit_events_org_actor_at ON audit_events (org_id, actor_id, at);
CREATE INDEX audit_events_org_subject_at ON audit_events (org_id, subject_id, at);
CREATE INDEX audit_events_org_trk ON audit_events (org_id, trk);
CREATE INDEX audit_events_org_causation ON audit_events (org_id, causation_id) WHERE causation_id IS NOT NULL;
CREATE INDEX audit_events_org_via_at ON audit_events (org_id, actor_via_id, at) WHERE actor_via_id IS NOT NULL;
CREATE INDEX audit_events_org_type_at ON audit_events (org_id, type, at);
CREATE INDEX audit_events_expire_at ON audit_events (expire_at);

CREATE TABLE outbox (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    schema_version  INTEGER NOT NULL,
    org_id          TEXT NOT NULL,
    workspace_id    TEXT,
    aggregate_kind  TEXT NOT NULL,
    aggregate_id    TEXT NOT NULL,
    aggregate_rev   BIGINT NOT NULL,
    origin_app      TEXT NOT NULL CHECK (origin_app IN ('keyhole', 'dispatch', 'auth', 'suite')),
    actor_kind      TEXT NOT NULL,
    actor_id        TEXT,
    actor_label     TEXT NOT NULL,
    actor_via_kind  TEXT,
    actor_via_id    TEXT,
    actor_via_label TEXT,
    occurred_at     BIGINT NOT NULL,
    trk             TEXT NOT NULL,
    correlation_id  TEXT,
    audit_event_id  TEXT,
    refusal         BOOLEAN NOT NULL DEFAULT FALSE,
    payload         JSONB NOT NULL,
    published_at    BIGINT,
    delete_at       BIGINT
);

CREATE INDEX outbox_published_order ON outbox (published_at, id);
CREATE INDEX outbox_org_aggregate ON outbox (org_id, aggregate_id, id);
CREATE INDEX outbox_delete_at ON outbox (delete_at);

-- resume_token is Mongo's change-stream token; a SQL relay checkpoints on
-- last_event_id (outbox IDs are ULIDs, so ID order is commit order).
CREATE TABLE outbox_consumers (
    id            TEXT PRIMARY KEY,
    resume_token  JSONB,
    last_event_id TEXT,
    last_event_at BIGINT,
    updated_at    BIGINT NOT NULL
);

CREATE TABLE applied_events (
    id         TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL,
    type       TEXT NOT NULL,
    applied_at BIGINT NOT NULL,
    delete_at  BIGINT NOT NULL
);

CREATE INDEX applied_events_delete_at ON applied_events (delete_at);
