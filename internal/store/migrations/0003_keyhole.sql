-- Keyhole's own records (mocks/schema/keyhole on the data-schema branch),
-- with the same conventions as 0002:
--
--   * field names are the schema's, in snake_case; _id is id, or
--     <record>_id where a row extends a shared record (workspace_settings,
--     agent_profiles, org_settings);
--   * a small fixed-shape object is flattened to <field>_<subfield>; a
--     credentialSet {current, previous{digest, graceUntil}, ...} becomes
--     <field>_alg/_hash/_prefix/_last4/... for the current digest and
--     <field>_prev_... for the one still in grace;
--   * an array of IDs that the schema indexes (availableKeyIds,
--     exposedKeyIds, availableToolIds, a grant's slotMap/keyIds, a cabinet's
--     keyIds and access list) is a child table, so "which workspaces can use
--     this key" is an indexed join rather than a scan;
--   * provider-specific and free-form objects stay JSONB;
--   * no column holds a secret value. Store credentials are secretRefs
--     (kss://… pointers into the sealed-secret service); tokens are digests
--     (hash, prefix, last four); Local-store values are envelope-encrypted.
--
-- Postgres and SQLite each run this file unchanged.

-- ---------------------------------------------------------------------------
-- Extensions of the shared records.

CREATE TABLE org_settings (
    org_id                        TEXT PRIMARY KEY REFERENCES orgs (id),
    config_version                BIGINT NOT NULL DEFAULT 0,
    notify_connector_offline      BOOLEAN NOT NULL DEFAULT TRUE,
    notify_store_unhealthy        BOOLEAN NOT NULL DEFAULT TRUE,
    notify_verification_failed    BOOLEAN NOT NULL DEFAULT TRUE,
    notify_token_expiring         BOOLEAN NOT NULL DEFAULT TRUE,
    notify_blocked_spike          BOOLEAN NOT NULL DEFAULT FALSE,
    notify_member_joined          BOOLEAN NOT NULL DEFAULT FALSE,
    alert_webhook_url             TEXT,
    -- secretRef only: the signing secret itself lives in the sealed-secret service.
    alert_webhook_signing_secret  JSONB,
    default_grace_seconds         INTEGER,
    -- Getting-started checklist on Home (docs/data-model.md, "home").
    checklist_dismissed_at        BIGINT,
    config_downloaded_at          BIGINT,
    updated_at                    BIGINT NOT NULL,
    updated_by_kind               TEXT,
    updated_by_id                 TEXT,
    CHECK (alert_webhook_url IS NULL OR alert_webhook_url LIKE 'https://%')
);

CREATE TABLE workspace_settings (
    workspace_id    TEXT PRIMARY KEY REFERENCES workspaces (id),
    org_id          TEXT NOT NULL REFERENCES orgs (id),
    slug            TEXT NOT NULL UNIQUE,
    https           BOOLEAN NOT NULL DEFAULT FALSE,
    mcp             BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at      BIGINT NOT NULL,
    updated_by_kind TEXT,
    updated_by_id   TEXT,
    rev             BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX workspace_settings_org ON workspace_settings (org_id);

-- Keyhole's side of a shared agent: status and the kh_live_ token digest.
CREATE TABLE agent_profiles (
    agent_id                TEXT PRIMARY KEY REFERENCES agents (id),
    org_id                  TEXT NOT NULL REFERENCES orgs (id),
    status                  TEXT NOT NULL,
    token_alg               TEXT NOT NULL,
    token_hash              TEXT NOT NULL,
    token_pepper_version    INTEGER,
    token_prefix            TEXT NOT NULL,
    token_last4             TEXT NOT NULL,
    token_issued_at         BIGINT NOT NULL,
    token_expires_at        BIGINT,
    token_prev_alg          TEXT,
    token_prev_hash         TEXT,
    token_prev_pepper_version INTEGER,
    token_prev_prefix       TEXT,
    token_prev_last4        TEXT,
    token_prev_issued_at    BIGINT,
    token_grace_until       BIGINT,
    token_rotated_at        BIGINT,
    token_rotated_by_kind   TEXT,
    token_rotated_by_id     TEXT,
    token_grace_seconds     INTEGER,
    token_grace_ended_early_at BIGINT,
    token_rotation_count    INTEGER NOT NULL DEFAULT 0,
    expires_at              BIGINT,
    rate_limit_per_minute   INTEGER,
    last_used_at            BIGINT,
    suspended_at            BIGINT,
    suspended_by_kind       TEXT,
    suspended_by_id         TEXT,
    revoked_at              BIGINT,
    revoked_by_kind         TEXT,
    revoked_by_id           TEXT,
    created_at              BIGINT NOT NULL,
    updated_at              BIGINT NOT NULL,
    rev                     BIGINT NOT NULL DEFAULT 0,
    CHECK (status IN ('active', 'suspended', 'revoked')),
    CHECK (rate_limit_per_minute IS NULL OR rate_limit_per_minute >= 1),
    CHECK ((token_prev_hash IS NULL) = (token_grace_until IS NULL))
);

-- Auth-path lookups: the digest is all a request has before the org is known.
CREATE UNIQUE INDEX agent_profiles_token_hash ON agent_profiles (token_hash);
CREATE UNIQUE INDEX agent_profiles_token_hash_prev ON agent_profiles (token_prev_hash)
    WHERE token_prev_hash IS NOT NULL;
CREATE INDEX agent_profiles_org_status ON agent_profiles (org_id, status);

-- ---------------------------------------------------------------------------
-- Vault connectors (kind 'vault') and sidecars (kind 'sidecar').
-- routesStoreIds and scope are derived, not stored: a connector routes the
-- stores whose route_connector_id names it, and is org-scoped when it routes
-- any. Presence lives in memory/Redis; health_* is written on transitions.

CREATE TABLE connectors (
    id                          TEXT PRIMARY KEY,
    org_id                      TEXT NOT NULL REFERENCES orgs (id),
    kind                        TEXT NOT NULL,
    name                        TEXT NOT NULL,
    status                      TEXT NOT NULL,
    workspace_id                TEXT REFERENCES workspaces (id),
    -- Sidecars only: the agent the sidecar acts as.
    agent_id                    TEXT REFERENCES agents (id),
    enrollment_token_id         TEXT NOT NULL,
    enrolled_at                 BIGINT NOT NULL,
    enrolled_by_kind            TEXT NOT NULL,
    enrolled_by_id              TEXT NOT NULL,
    enrolled_from_ip            TEXT,
    public_key                  TEXT,
    client_cert_fingerprint     TEXT,
    -- kh_conn_… / kh_sc_… digest; cleared when the component is revoked.
    credential_alg              TEXT,
    credential_hash             TEXT,
    credential_pepper_version   INTEGER,
    credential_prefix           TEXT,
    credential_last4            TEXT,
    credential_issued_at        BIGINT,
    credential_expires_at       BIGINT,
    credential_prev_alg         TEXT,
    credential_prev_hash        TEXT,
    credential_prev_pepper_version INTEGER,
    credential_prev_prefix      TEXT,
    credential_prev_last4       TEXT,
    credential_prev_issued_at   BIGINT,
    credential_grace_until      BIGINT,
    credential_rotated_at       BIGINT,
    credential_rotated_by_kind  TEXT,
    credential_rotated_by_id    TEXT,
    credential_grace_seconds    INTEGER,
    credential_grace_ended_early_at BIGINT,
    credential_rotation_count   INTEGER NOT NULL DEFAULT 0,
    -- installInfo {method, os, arch, host, serviceManager, version}
    install                     JSONB,
    health_status               TEXT NOT NULL DEFAULT 'offline',
    health_last_seen_at         BIGINT,
    health_ip                   TEXT,
    health_missed_heartbeats    INTEGER NOT NULL DEFAULT 0,
    health_reported_version     TEXT,
    health_config_version       BIGINT,
    -- Counts only, never keys or values.
    health_cache                JSONB,
    cache_policy                JSONB NOT NULL,
    -- Vault connectors.
    egress_tunnel               TEXT,
    egress_region               TEXT,
    -- Sidecars: localEndpoint (listen, exposure, localAuth with the caller
    -- token's digest only), appMode and proxy settings.
    local                       JSONB,
    app_mode                    TEXT,
    proxy                       JSONB,
    created_at                  BIGINT NOT NULL,
    created_by_kind             TEXT NOT NULL,
    created_by_id               TEXT NOT NULL,
    updated_at                  BIGINT NOT NULL,
    updated_by_kind             TEXT,
    updated_by_id               TEXT,
    revoked_at                  BIGINT,
    revoked_by_kind             TEXT,
    revoked_by_id               TEXT,
    rev                         BIGINT NOT NULL DEFAULT 0,
    CHECK (kind IN ('vault', 'sidecar')),
    CHECK (status IN ('active', 'revoked')),
    CHECK (health_status IN ('healthy', 'degraded', 'offline')),
    CHECK (status = 'revoked' OR credential_hash IS NOT NULL),
    CHECK (kind = 'vault' OR (workspace_id IS NOT NULL AND agent_id IS NOT NULL
                              AND local IS NOT NULL AND app_mode IN ('base_url', 'proxy'))),
    CHECK (kind = 'sidecar' OR (agent_id IS NULL AND local IS NULL AND app_mode IS NULL))
);

CREATE UNIQUE INDEX connectors_credential_hash ON connectors (credential_hash)
    WHERE credential_hash IS NOT NULL;
CREATE UNIQUE INDEX connectors_credential_hash_prev ON connectors (credential_prev_hash)
    WHERE credential_prev_hash IS NOT NULL;
CREATE INDEX connectors_org_kind_ws ON connectors (org_id, kind, workspace_id);
CREATE INDEX connectors_org_agent ON connectors (org_id, agent_id) WHERE kind = 'sidecar';

CREATE TABLE enrollment_tokens (
    id                        TEXT PRIMARY KEY,
    org_id                    TEXT NOT NULL REFERENCES orgs (id),
    app                       TEXT NOT NULL DEFAULT 'keyhole',
    component_kind            TEXT NOT NULL,
    -- kh_enr_… digest; the token is shown once.
    token_alg                 TEXT NOT NULL,
    token_hash                TEXT NOT NULL UNIQUE,
    token_pepper_version      INTEGER,
    token_prefix              TEXT NOT NULL,
    token_last4               TEXT NOT NULL,
    token_issued_at           BIGINT NOT NULL,
    status                    TEXT NOT NULL,
    -- What the admin chose: name, workspace, agent, local endpoint, mode, cache.
    intent                    JSONB NOT NULL,
    created_at                BIGINT NOT NULL,
    created_by_kind           TEXT NOT NULL,
    created_by_id             TEXT NOT NULL,
    expires_at                BIGINT NOT NULL,
    consumed_at               BIGINT,
    consumed_from_ip          TEXT,
    consumed_by_component_id  TEXT,
    revoked_at                BIGINT,
    revoked_by_kind           TEXT,
    revoked_by_id             TEXT,
    delete_at                 BIGINT,
    CHECK (component_kind IN ('vault_connector', 'keyhole_sidecar')),
    CHECK (status IN ('pending', 'consumed', 'expired', 'revoked'))
);

CREATE INDEX enrollment_tokens_org_status_exp ON enrollment_tokens (org_id, status, expires_at);
CREATE INDEX enrollment_tokens_delete_at ON enrollment_tokens (delete_at);

-- ---------------------------------------------------------------------------
-- Secret stores. Columns every store has, plus what an OpenBao store needs,
-- are real columns; the rest of each provider's connection and auth fields
-- are in settings ({"connection": {...}, "auth": {...}}). Credentials in
-- settings are secretRefs, never values.

CREATE TABLE stores (
    id                          TEXT PRIMARY KEY,
    org_id                      TEXT NOT NULL REFERENCES orgs (id),
    name                        TEXT NOT NULL,
    provider                    TEXT NOT NULL,
    status                      TEXT NOT NULL,
    route_mode                  TEXT NOT NULL,
    route_connector_id          TEXT REFERENCES connectors (id),
    network_exposure            TEXT NOT NULL DEFAULT 'unknown',
    -- OpenBao / Vault connection.
    address                     TEXT,
    namespace                   TEXT,
    ca_bundle_pem               TEXT,
    ca_bundle_name              TEXT,
    tls_server_name             TEXT,
    skip_tls_verify             BOOLEAN NOT NULL DEFAULT FALSE,
    -- Where the OpenBao wizard browses for keys (KV mount and path prefix).
    kv_mount                    TEXT,
    kv_path_prefix              TEXT,
    -- Auth method and its non-secret identifiers.
    auth_method                 TEXT,
    auth_mount                  TEXT,
    auth_role_id                TEXT,
    auth_role                   TEXT,
    identity_source             TEXT,
    settings                    JSONB,
    health_status               TEXT NOT NULL DEFAULT 'unknown',
    health_code                 TEXT,
    health_message              TEXT,
    health_http_status          INTEGER,
    health_vendor_code          TEXT,
    health_checked_at           BIGINT NOT NULL,
    health_last_test_id         TEXT,
    cache_default_ttl_seconds   INTEGER NOT NULL DEFAULT 300,
    -- {eventSourceIds, revalidation, ...}
    invalidation                JSONB,
    created_at                  BIGINT NOT NULL,
    created_by_kind             TEXT NOT NULL,
    created_by_id               TEXT NOT NULL,
    updated_at                  BIGINT NOT NULL,
    updated_by_kind             TEXT,
    updated_by_id               TEXT,
    removed_at                  BIGINT,
    removed_by_kind             TEXT,
    removed_by_id               TEXT,
    rev                         BIGINT NOT NULL DEFAULT 0,
    CHECK (provider IN ('openbao', 'hashicorp_vault', 'aws_secrets_manager', 'aws_ssm_parameter_store',
                        'azure_key_vault', 'gcp_secret_manager', 'onepassword_connect',
                        'onepassword_service_account', 'infisical', 'doppler', 'conjur', 'local')),
    CHECK (status IN ('draft', 'verifying', 'active', 'disabled', 'removed')),
    CHECK (route_mode IN ('direct', 'connector', 'internal')),
    CHECK ((route_mode = 'connector') = (route_connector_id IS NOT NULL)),
    CHECK ((route_mode = 'internal') = (provider = 'local')),
    CHECK (network_exposure IN ('public', 'private', 'unknown')),
    CHECK (network_exposure <> 'private' OR route_mode = 'connector'),
    CHECK (identity_source IS NULL OR identity_source <> 'connector_ambient' OR route_mode = 'connector'),
    CHECK (health_status IN ('healthy', 'degraded', 'sealed', 'denied', 'unreachable', 'unknown')),
    CHECK (cache_default_ttl_seconds BETWEEN 0 AND 3600)
);

-- A name is unique among an org's stores that are not removed.
CREATE UNIQUE INDEX stores_org_name ON stores (org_id, name) WHERE status <> 'removed';
CREATE INDEX stores_org_connector ON stores (org_id, route_connector_id) WHERE route_connector_id IS NOT NULL;
CREATE INDEX stores_org_provider ON stores (org_id, provider);

CREATE TABLE store_connection_tests (
    id                    TEXT PRIMARY KEY,
    org_id                TEXT NOT NULL REFERENCES orgs (id),
    store_id              TEXT NOT NULL REFERENCES stores (id),
    trigger               TEXT NOT NULL,
    ran_via_mode          TEXT NOT NULL,
    ran_via_connector_id  TEXT,
    started_at            BIGINT NOT NULL,
    finished_at           BIGINT,
    ok                    BOOLEAN NOT NULL,
    health_code           TEXT,
    -- [{step, ok, code, message, ms}] — never values.
    steps                 JSONB NOT NULL,
    requested_by_kind     TEXT,
    requested_by_id       TEXT,
    delete_at             BIGINT,
    CHECK (trigger IN ('create', 'edit', 'manual', 'scheduled', 'credential_rotated')),
    CHECK (ran_via_mode IN ('direct', 'connector'))
);

CREATE INDEX store_connection_tests_org_store_at ON store_connection_tests (org_id, store_id, started_at);
CREATE INDEX store_connection_tests_delete_at ON store_connection_tests (delete_at);

CREATE TABLE store_event_sources (
    id                TEXT PRIMARY KEY,
    org_id            TEXT NOT NULL REFERENCES orgs (id),
    store_id          TEXT NOT NULL REFERENCES stores (id),
    kind              TEXT NOT NULL,
    direction         TEXT,
    endpoint_path     TEXT,
    -- {method, secret (secretRef), headerName, ...}
    verification      JSONB,
    -- {eventTypes, paths, configs}
    filter            JSONB,
    via_connector_id  TEXT REFERENCES connectors (id),
    status            TEXT NOT NULL,
    last_event_at     BIGINT,
    created_at        BIGINT NOT NULL,
    created_by_kind   TEXT NOT NULL,
    created_by_id     TEXT NOT NULL,
    updated_at        BIGINT NOT NULL,
    CHECK (status IN ('pending_setup', 'active', 'failing', 'disabled'))
);

CREATE INDEX store_event_sources_org_store ON store_event_sources (org_id, store_id);

-- Keyhole-as-OIDC-issuer signing keys. Platform rows: no org. The private
-- key never leaves the KMS; private_key_ref names it.
CREATE TABLE issuer_keys (
    id               TEXT PRIMARY KEY,
    kid              TEXT NOT NULL UNIQUE,
    alg              TEXT NOT NULL,
    public_jwk       JSONB NOT NULL,
    private_key_ref  TEXT NOT NULL,
    status           TEXT NOT NULL,
    created_at       BIGINT NOT NULL,
    activated_at     BIGINT,
    retiring_at      BIGINT,
    retired_at       BIGINT,
    CHECK (alg IN ('RS256', 'ES256')),
    CHECK (status IN ('next', 'active', 'retiring', 'retired'))
);

CREATE INDEX issuer_keys_status ON issuer_keys (status);

-- ---------------------------------------------------------------------------
-- Cabinets come before keys: a cabinet-owned Local key names its cabinet.

CREATE TABLE cabinets (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL REFERENCES orgs (id),
    workspace_id     TEXT NOT NULL REFERENCES workspaces (id),
    name             TEXT NOT NULL,
    created_by_kind  TEXT,
    created_by_id    TEXT,
    -- NULL: the workspace's admins manage it.
    managed_by       TEXT REFERENCES users (id),
    -- access: "everyone" in the workspace, or the players in cabinet_access.
    access_everyone  BOOLEAN NOT NULL,
    created_at       BIGINT NOT NULL,
    updated_at       BIGINT NOT NULL,
    rev              BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX cabinets_org_ws ON cabinets (org_id, workspace_id);

-- Keys say where a value lives, never the value.
CREATE TABLE keys (
    id                      TEXT PRIMARY KEY,
    org_id                  TEXT NOT NULL REFERENCES orgs (id),
    name                    TEXT NOT NULL,
    store_id                TEXT NOT NULL REFERENCES stores (id),
    provider                TEXT NOT NULL,
    -- Provider-specific address: {engine, mount, path, field, version} for
    -- OpenBao, {localValueId, version} for Local. NULL for a Local key whose
    -- value has not been written yet.
    address                 JSONB,
    pinned                  BOOLEAN NOT NULL DEFAULT FALSE,
    cache_ttl_seconds       INTEGER,
    length                  INTEGER,
    last_seen_version       TEXT,
    rotated_at              BIGINT,
    expires_at              BIGINT,
    rotation_reminder_days  INTEGER,
    notes                   TEXT NOT NULL DEFAULT '',
    status                  TEXT NOT NULL DEFAULT 'active',
    cabinet_id              TEXT REFERENCES cabinets (id),
    created_at              BIGINT NOT NULL,
    created_by_kind         TEXT NOT NULL,
    created_by_id           TEXT NOT NULL,
    updated_at              BIGINT NOT NULL,
    updated_by_kind         TEXT,
    updated_by_id           TEXT,
    rev                     BIGINT NOT NULL DEFAULT 0,
    UNIQUE (org_id, store_id, name),
    CHECK (status IN ('active', 'source_removed')),
    CHECK (cache_ttl_seconds IS NULL OR cache_ttl_seconds BETWEEN 0 AND 3600),
    CHECK (rotation_reminder_days IS NULL OR rotation_reminder_days >= 1),
    CHECK (address IS NOT NULL OR provider = 'local')
);

CREATE INDEX keys_org_name ON keys (org_id, name);
CREATE INDEX keys_org_cabinet ON keys (org_id, cabinet_id) WHERE cabinet_id IS NOT NULL;

-- The only secret values in the database: one row per version, AES-256-GCM
-- under a per-row data key that a KMS key wraps. Only the key-resolution
-- path reads this table; no admin query returns it.
CREATE TABLE local_key_values (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL REFERENCES orgs (id),
    key_id           TEXT NOT NULL REFERENCES keys (id) ON DELETE CASCADE,
    version          INTEGER NOT NULL,
    -- {alg, nonce, ciphertext, aad, dek {wrapped, kmsKeyRef}}
    envelope         JSONB NOT NULL,
    length           INTEGER NOT NULL,
    created_at       BIGINT NOT NULL,
    created_by_kind  TEXT NOT NULL,
    created_by_id    TEXT NOT NULL,
    superseded_at    BIGINT,
    destroy_at       BIGINT,
    UNIQUE (org_id, key_id, version)
);

CREATE INDEX local_key_values_destroy_at ON local_key_values (destroy_at);

-- ---------------------------------------------------------------------------
-- Tools: the editable draft on the row, published versions as immutable
-- snapshots.

CREATE TABLE tools (
    id                   TEXT PRIMARY KEY,
    org_id               TEXT NOT NULL REFERENCES orgs (id),
    status               TEXT NOT NULL,
    draft_internal_name  TEXT NOT NULL,
    draft_display_name   TEXT NOT NULL,
    draft_description    TEXT NOT NULL,
    draft_base_url       TEXT NOT NULL,
    -- [{id, method, path, summary, inputs: [...]}]
    draft_actions        JSONB NOT NULL,
    -- [{id, name, location, field, scheme}]
    draft_slots          JSONB NOT NULL,
    draft_per_minute     INTEGER NOT NULL,
    draft_timeout_ms     INTEGER NOT NULL,
    published_version    INTEGER,
    catalog              TEXT,
    created_at           BIGINT NOT NULL,
    created_by_kind      TEXT NOT NULL,
    created_by_id        TEXT NOT NULL,
    updated_at           BIGINT NOT NULL,
    updated_by_kind      TEXT,
    updated_by_id        TEXT,
    deleted_at           BIGINT,
    rev                  BIGINT NOT NULL DEFAULT 0,
    CHECK (status IN ('draft', 'published')),
    CHECK (status = 'draft' OR published_version IS NOT NULL),
    CHECK (catalog IS NULL OR catalog IN ('stripe', 'github', 'slack')),
    CHECK (draft_per_minute >= 1),
    CHECK (draft_timeout_ms BETWEEN 100 AND 120000)
);

CREATE INDEX tools_org_internal_name ON tools (org_id, draft_internal_name);

CREATE TABLE tool_versions (
    id                 TEXT PRIMARY KEY,
    org_id             TEXT NOT NULL REFERENCES orgs (id),
    tool_id            TEXT NOT NULL REFERENCES tools (id),
    version            INTEGER NOT NULL,
    -- The full tool snapshot, as tool.schema.json $defs/snapshot.
    snapshot           JSONB NOT NULL,
    changes            JSONB,
    published_at       BIGINT NOT NULL,
    published_by_kind  TEXT NOT NULL,
    published_by_id    TEXT NOT NULL,
    UNIQUE (org_id, tool_id, version),
    CHECK (version >= 1)
);

-- ---------------------------------------------------------------------------
-- What a workspace can use. Org admins make keys and tools available; a
-- workspace admin exposes available keys (exposed is a flag on the available
-- row, so exposed ⊆ available holds by construction).

CREATE TABLE workspace_availability (
    workspace_id              TEXT PRIMARY KEY REFERENCES workspaces (id),
    org_id                    TEXT NOT NULL REFERENCES orgs (id),
    availability_set_by_kind  TEXT,
    availability_set_by_id    TEXT,
    availability_set_at       BIGINT,
    exposure_set_by_kind      TEXT,
    exposure_set_by_id        TEXT,
    exposure_set_at           BIGINT,
    updated_at                BIGINT NOT NULL,
    rev                       BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE workspace_available_keys (
    workspace_id  TEXT NOT NULL REFERENCES workspace_availability (workspace_id) ON DELETE CASCADE,
    org_id        TEXT NOT NULL REFERENCES orgs (id),
    key_id        TEXT NOT NULL REFERENCES keys (id),
    exposed       BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (workspace_id, key_id)
);

CREATE INDEX workspace_available_keys_org_key ON workspace_available_keys (org_id, key_id, exposed);

CREATE TABLE workspace_available_tools (
    workspace_id  TEXT NOT NULL REFERENCES workspace_availability (workspace_id) ON DELETE CASCADE,
    org_id        TEXT NOT NULL REFERENCES orgs (id),
    tool_id       TEXT NOT NULL REFERENCES tools (id),
    PRIMARY KEY (workspace_id, tool_id)
);

CREATE INDEX workspace_available_tools_org_tool ON workspace_available_tools (org_id, tool_id);

-- A tool granted to a workspace. slotMap is grant_slots: one row per slot,
-- key_id NULL while the slot is unfilled.
CREATE TABLE grants (
    id               TEXT PRIMARY KEY,
    org_id           TEXT NOT NULL REFERENCES orgs (id),
    workspace_id     TEXT NOT NULL REFERENCES workspaces (id),
    tool_id          TEXT NOT NULL REFERENCES tools (id),
    -- NULL: follow the latest published version.
    pinned_version   INTEGER,
    enabled          BOOLEAN NOT NULL,
    per_minute       INTEGER NOT NULL,
    granted_at       BIGINT NOT NULL,
    granted_by_kind  TEXT NOT NULL,
    granted_by_id    TEXT NOT NULL,
    updated_at       BIGINT NOT NULL,
    updated_by_kind  TEXT,
    updated_by_id    TEXT,
    rev              BIGINT NOT NULL DEFAULT 0,
    UNIQUE (org_id, workspace_id, tool_id),
    CHECK (per_minute >= 1)
);

CREATE INDEX grants_org_tool ON grants (org_id, tool_id);

CREATE TABLE grant_slots (
    grant_id  TEXT NOT NULL REFERENCES grants (id) ON DELETE CASCADE,
    org_id    TEXT NOT NULL REFERENCES orgs (id),
    slot      TEXT NOT NULL,
    key_id    TEXT REFERENCES keys (id),
    PRIMARY KEY (grant_id, slot)
);

CREATE INDEX grant_slots_org_key ON grant_slots (org_id, key_id);

CREATE TABLE cabinet_keys (
    cabinet_id  TEXT NOT NULL REFERENCES cabinets (id) ON DELETE CASCADE,
    org_id      TEXT NOT NULL REFERENCES orgs (id),
    key_id      TEXT NOT NULL REFERENCES keys (id),
    PRIMARY KEY (cabinet_id, key_id)
);

CREATE INDEX cabinet_keys_org_key ON cabinet_keys (org_id, key_id);

CREATE TABLE cabinet_tools (
    cabinet_id  TEXT NOT NULL REFERENCES cabinets (id) ON DELETE CASCADE,
    org_id      TEXT NOT NULL REFERENCES orgs (id),
    tool_id     TEXT NOT NULL REFERENCES tools (id),
    -- slot name → keyId, or null while unfilled.
    slot_map    JSONB NOT NULL,
    PRIMARY KEY (cabinet_id, tool_id)
);

-- Players a cabinet is shared with when access_everyone is FALSE.
CREATE TABLE cabinet_access (
    cabinet_id   TEXT NOT NULL REFERENCES cabinets (id) ON DELETE CASCADE,
    org_id       TEXT NOT NULL REFERENCES orgs (id),
    member_kind  TEXT NOT NULL,
    member_id    TEXT NOT NULL,
    PRIMARY KEY (cabinet_id, member_kind, member_id),
    CHECK (member_kind IN ('user', 'agent'))
);

CREATE INDEX cabinet_access_org_member ON cabinet_access (org_id, member_kind, member_id);

-- Not in mocks/schema: a person's own alert choices ("My settings"). The
-- org_settings notify_* columns stay the org's defaults; a person's row,
-- when present, wins. Dispatch's schema does the same.
CREATE TABLE notification_preferences (
    org_id               TEXT NOT NULL REFERENCES orgs (id),
    user_id              TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    connector_offline    BOOLEAN NOT NULL,
    store_unhealthy      BOOLEAN NOT NULL,
    verification_failed  BOOLEAN NOT NULL,
    token_expiring       BOOLEAN NOT NULL,
    blocked_spike        BOOLEAN NOT NULL,
    member_joined        BOOLEAN NOT NULL,
    updated_at           BIGINT NOT NULL,
    PRIMARY KEY (org_id, user_id)
);
