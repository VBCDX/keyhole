# Connecting vaults: backend flows

This file shows how `store.schema.json` supports every vault type and auth method in VAULT_FLOWS.md, from connecting through to rotation. Section numbers (§) refer to VAULT_FLOWS.md. Anything VAULT_FLOWS.md marks ⚠/◐ carries `x-verify: true` in the schema.

## 1. Lifecycle (every provider)

| Step | What the backend does | Data |
|---|---|---|
| 1. Draft | An org admin picks the provider, name and route. | `stores` row with `status: draft`. |
| 2. Route | `direct` (SaaS), or `connector` with a vault connector (`cn_…`). A private endpoint, a connector-only method or ambient identity forces a connector. | `route`, `networkExposure`; the connector's `routesStoreIds` gains the store, which makes it org-level. |
| 3. Auth method | Collect exactly the method's fields (§12). Keyhole fills read-only fields: `externalId`, `federation.subject`/`issuer`, `keyholePrincipalArn`, `keyholeClientId`, `keyholeServiceAccountEmail`. | `auth` |
| 4. Hand over a secret (static methods only) | Direct route: seal the secret in the Keyhole sealed-secret service → `secretRef {kind: sealed, uri: kss://…}`. Connector route: prefer `keyhole connector secret set <name>` on the host → `secretRef {kind: connector_local}`, so the secret never reaches the SaaS. AppRole wrapping: Keyhole (or the connector) calls `sys/wrapping/unwrap` once and seals the result; the wrapping token is never stored. | `auth.*` secretRef fields |
| 5. Test | Run on the route (SaaS worker or connector): reachability → TLS → auth → value-free permission check → optional read probe. Each step is recorded, and vendor errors map to `health.code`. | `store_connection_tests`, `stores.health` |
| 6. Activate | `status: active` after the first passing test. | |
| 7. Keys | Addresses per provider (`key.schema.json`), with `pinned` derived. | `keys` |
| 8. Change events | Optional event source per store; the inbound endpoint is `https://hooks.keyhole.dev/store-events/{esrc_…}`. | `store_event_sources` |
| 9. Operate | Login, then session renewal (in broker or connector memory, single-flight via `kh:vlogin:{storeId}`), then reads, cache and revalidation. | Redis for coordination only |
| 10. Rotate the store credential | The customer rotates in the vendor; the admin replaces the secretRef (new `version`), which triggers a test. Federated methods have nothing to rotate. | `secretRef.version` |
| 11. Remove | `status: removed`. Keys become `source_removed`; workspaces keep placeholders until resolved. | |

**Keyhole as OIDC issuer** covers `jwt`, `oidc`, `web_identity`, `wif`, `federated`, `oidc_identity`, and Conjur `jwt`.
- **Issuer:** `https://id.keyhole.dev`, with a discovery document and a JWKS built from `issuer_keys` (next, active and retiring keys).
- **Subject:** `keyhole:<orgId>:<storeId>`. The customer binds it exactly: Azure and Infisical recommend hardcoded values, not globs.
- **Audience:** per provider. AWS uses the value the customer's IAM OIDC provider expects; Azure uses `api://AzureADTokenExchange`; GCP uses the WIF provider resource name.
- **Lifetime:** 5 minutes. A fresh JWT is minted per login, and nothing is stored.
- **Reachability:** the vendor must reach the JWKS URL, or the customer pastes the keys (Vault `jwt_validation_pubkeys`).

## 2. Per provider

In the table:
- **Methods**: ★ = recommended, ▲ = discouraged, **(C)** = connector only.
- **"Stores"**: which fields hold a secretRef.

| Provider | Methods (`auth.method`) | Stores | Test probes | Change detection |
|---|---|---|---|---|
| `openbao` | `token`, ★`approle`, ★`approle_wrapped`, ★`jwt`, `kubernetes` (C), `cert`, `aws_iam`, `azure` (C), `gcp` (`gce` ⇒ C). The cloud methods are external plugins: probe `sys/auth` before offering them. `inlineAuth` is available on approle and jwt. | `token`, `secretId`, `clientKey` | `GET /v1/sys/health` (200/429/501/503), `auth/token/lookup-self`, `POST /v1/sys/capabilities-self`, KV v2 `subkeys` | poll KV v2 `metadata.current_version` (no events in OpenBao) |
| `hashicorp_vault` | as OpenBao, plus `aws_ec2` (C, ▲); cloud methods are built in; `edition: hcp_dedicated` requires `namespace` ⚠ | same, plus `nonce` | same (Enterprise adds 472/473 ⚠) | Enterprise: `vault_events_ws` (`kv-v2/data-*`), via SaaS or connector; otherwise poll metadata |
| `aws_secrets_manager` | ★`assume_role` (+ ExternalId), ★`web_identity` ⚠, `roles_anywhere`, `ambient` (C), ▲`access_keys` | `privateKey`, `secretAccessKey`, `sessionToken` | `sts:GetCallerIdentity`, then `GetSecretValue` (no dry run), or `DescribeSecret` for metadata only | `aws_eventbridge_api_destination` ◐; poll `DescribeSecret` (`VersionIdsToStages`) |
| `aws_ssm_parameter_store` | same AWS stack | same | `GetParameter WithDecryption` | `aws_ssm_parameter_change` ⚠; no rotation |
| `azure_key_vault` | ★`keyhole_app` ◐, ★`federated` ◐, `client_certificate`, ▲`client_secret`, `managed_identity` (C), `aks_workload_identity` (C) | `privateKey`, `clientSecret` | token acquisition (AADSTS errors are surfaced separately), then `GET {vault}/secrets/{name}` | `azure_event_grid` (handshake ⚠) on `SecretNewVersionCreated` |
| `gcp_secret_manager` | ★`keyhole_sa`, ★`impersonation`, ★`wif`, `ambient` (C), ▲`sa_key` ⚠ | `keyJson` | token, then `:testIamPermissions` (value-free), then an optional `:access`; verify CRC32C | `gcp_pubsub_push` / `gcp_pubsub_pull` (`SECRET_VERSION_ADD`) ⚠ |
| `onepassword_service_account` | `service_account` (SDK; rate limits ◐) | `token` | `Secrets().Resolve` of the key's reference | poll item `version`; aggressive caching |
| `onepassword_connect` | `connect_token` (the host is usually private, so a connector) | `token` | `GET /heartbeat`, then `/health`, then `/v1/vaults/{uuid}` | poll item `version` |
| `infisical` | `universal`, ★`oidc`, `jwt`, `aws`, `kubernetes` (C), `gcp`, `azure`, `tls_cert`, `spiffe` (C), ▲`token` | `clientSecret`, `privateKey`, `accessToken` | login (`expiresIn`), then `GET /api/v4/secrets/{name}` | `infisical_webhook` (`x-infisical-signature`), `infisical_event_subscription` (Enterprise SSE) |
| `doppler` | ★`service_token` ◐, `service_account_token` ◐, ★`oidc_identity` | `token` | `GET /v3/me` ⚠, then read one secret | `doppler_webhook` ◐, `doppler_watch_sse`, ETag polling (304) |
| `conjur` | `api_key` ◐ (8-minute tokens), ★`jwt` ⚠, `platform` ◐ | `apiKey` | authenticate, then `GET /whoami` ⚠, then read the variable | poll |
| `local` | none (`route: internal`) | values in `local_key_values` | — | — |

## 3. Health mapping (`stores.health.code`)

| Code | From |
|---|---|
| `sealed` / `not_initialized` / `standby` | Vault/OpenBao `sys/health` 503 / 501 / 429 |
| `namespace_sealed` / `namespace_locked` | OpenBao sealable or locked namespaces. Never mapped to `sealed`. The exact error code is ⚠. |
| `permission_denied` | Vault 403; AWS `AccessDeniedException`; Azure 403 `ForbiddenByRbac`/`ByPolicy` ⚠; GCP `PERMISSION_DENIED`; Infisical, Doppler, Conjur and 1Password 403 |
| `invalid_credential` / `expired_credential` | AWS `UnrecognizedClientException` / `ExpiredTokenException`; Azure `AADSTS7000215` / `AADSTS700024`; 401 elsewhere |
| `consent_missing` | Azure `AADSTS700016` (app not found in tenant) |
| `firewall_denied` / `vpcsc_denied` | Azure `ForbiddenByFirewall` ⚠; GCP 403 with a VPC-SC reason ⚠. Suggest a connector. |
| `kms_decrypt_denied` | AWS `DecryptionFailure` |
| `not_found` | 404 or `ResourceNotFoundException` / `ParameterNotFound` / `SecretNotFound` ⚠ |
| `rate_limited` | 429; `ThrottlingException`; `RESOURCE_EXHAUSTED`; 1Password hourly limits |
| `sync_not_ready` | 1Password Connect `/health` |
| `tls_error` / `dns_error` / `tcp_error` | transport |
| `connector_offline` | the route's connector has no presence |

Stale-if-error never applies to `permission_denied`, `invalid_credential`, `expired_credential` or `not_found` (§11.2 #5).

## 4. Things the backend must get right

- **AWS ExternalId.** Generate it per store (Keyhole's value, never the customer's), keep it immutable, and show it read-only. Role chaining caps sessions at 3600 s.
- **AppRole `secret_id_num_uses` > 0.** Logins go through the `kh:vlogin:{storeId}` lock so broker replicas don't burn uses. OpenBao inline auth counts every request as a use.
- **Namespaces.** Send `X-Vault-Namespace` on login **and** on reads. HCP Dedicated requires it.
- **Cross-tenant isolation with `keyhole_sa`.** Every tenant grants the same GCP principal, so Keyhole must scope reads by store, never by "whatever the SA can see".
- **JSON field selection.** Only Vault KV is a map natively. Everywhere else, `jsonKey` is parsed on Keyhole's side. For a 1Password field that holds JSON, parse it the same way.
- **Pinned versus moving addresses** decide the TTL tier (`cache.md` §3).
