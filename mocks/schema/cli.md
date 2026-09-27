# CLIs: `keyhole` and `dispatch`

There are two command-line tools. Each is one binary that humans use interactively and service managers run as a daemon:

| Binary | Daemon entry points | Package names |
|---|---|---|
| `keyhole` | `keyholed connector` and `keyholed sidecar`. `keyholed` is a symlink to `keyhole daemon`, and is the name the prototype's copy uses (`npx keyholed@latest sidecar …`). | Homebrew `keyhole`, apt/yum `keyhole`, MSI, OCI image `ghcr.io/vbcdx/keyholed`, Helm chart `keyhole/connector`, npm `keyholed` |
| `dispatch` | `dispatch sidecar run` | Homebrew `dispatch`, apt/yum `dispatch`, MSI, OCI image `ghcr.io/vbcdx/dispatch-sidecar`, npm `@vbcdx/dispatch` |

## 1. Installers

- **Install script:**
  ```sh
  curl -fsSL https://get.keyhole.dev | sh
  curl -fsSL https://get.dispatch.dev | sh
  ```
  It detects the OS and architecture, downloads the release, verifies SHA-256 **and** the signature (cosign keyless, pinned to the release workflow identity) before unpacking, installs to `/usr/local/bin`, and does nothing else. It doesn't enroll, and it doesn't start a service.
- **Package managers:** Homebrew tap `vbcdx/tap`, signed apt/yum repositories, a signed MSI, and OCI images signed with cosign.
- **Kubernetes:** Helm chart `keyhole/connector` for vault connectors. Enrollment is a one-shot init Job that reads the token from a Secret, and the credential is stored in a Secret that only the connector's ServiceAccount can read. The chart can also add a sidecar container spec for app pods (`keyhole sidecar k8s-snippet`).
- **Service install:** `keyhole connector install`, `keyhole sidecar install` and `dispatch sidecar install`. Each one:
  - creates a service user (`keyholed` / `dispatch`);
  - creates the state directory (0700) and the config directory;
  - writes a systemd unit (or a launchd plist, or a Windows service) with `NoNewPrivileges`, `ProtectSystem=strict`, `PrivateTmp`, `LimitCORE=0` and `LoadCredentialEncrypted=` for held credentials;
  - prints the next step (`enroll`).

  It never enrolls on its own.

## 2. Command surface

Common flags: `--profile NAME`, `--config PATH`, `--org org_…`, `-o, --output table|json`, `-q`, `--no-color`.

### `keyhole`

| Command | What it does |
|---|---|
| `keyhole login [--no-browser]` | Human sign-in with the device flow (RFC 8628) against the auth service. Prints the verification URL and code. Stores the refresh token (`au_rt_…`) in the OS keychain; without a keychain, in `~/.config/keyhole/credentials` (0600) with a warning. |
| `keyhole login --token-file PATH` | Machine use, also `KEYHOLE_TOKEN_FILE`. The file holds a machine credential (`kh_live_…` for agent-scoped commands). It is refused unless owned by the user and mode 0600 or stricter. Admin commands still need a human login. |
| `keyhole logout`, `keyhole whoami` | whoami prints the user or agent, the orgs and roles from the claims, the active org, and token expiry. |
| `keyhole connector install [--service systemd\|launchd\|windows\|none]` | Service install (§1). |
| `keyhole connector enroll [--enroll-token-file PATH \| --enroll-token-stdin]` | Reads `kh_enr_…` from stdin (the default: a hidden prompt on a TTY), a file, or `KEYHOLED_ENROLL_TOKEN`. Generates the X25519 keypair (and an mTLS CSR if enabled), enrolls, and writes the held credential. Prints the connector ID and the org-level warning if stores will route through it. |
| `keyhole connector run` / `keyholed connector` | Foreground daemon: outbound tunnel, heartbeats, vault access, cache. |
| `keyhole connector status` | Enrollment, tunnel, last heartbeat, `configVersion`, stores routed, per-store health, cache summary, credential age, and grace in progress (`old credential valid until 14:10`). |
| `keyhole connector rotate [--grace 10m\|0] [--end-grace]` | Rotates its own credential over the tunnel (the new one never touches disk unsealed). `--grace 0` stops the old one immediately. `--end-grace` ends a running grace. |
| `keyhole connector credential set` | Reads a credential that an admin rotated in the UI, from stdin. For offline connectors. |
| `keyhole connector secret set NAME` / `secret list` / `secret rm NAME` | `connector_local` store secrets (AppRole secret_id, API keys), read from stdin and sealed locally. `list` shows names and set times only. |
| `keyhole connector uninstall [--wipe]` | Stops and removes the service. `--wipe` deletes held credentials and the cache. |
| `keyhole sidecar install` / `enroll` / `run` / `status` / `rotate` / `uninstall` | As above, for a sidecar. `enroll` accepts `--listen 127.0.0.1:8787\|unix:/run/keyholed/<name>.sock`, `--expose-on-network` (needs `--i-understand-network-exposure`, and must match the enrolled intent), `--protocols http,mcp,sse`, `--proxy-mode`. `status` also shows the bound agent, the workspace and the allowed hosts. |
| `keyhole sidecar caller-token rotate [--grace 10m\|0]` | Rotates `kh_call_…`. The new token is printed once, for the app's config. |
| `keyhole sidecar ca export` | Proxy mode: prints the local CA certificate for the app to trust. |
| `keyhole sidecar env [--mode base\|proxy]` | Prints the app-side config (no secrets): the base URL or proxy variables, and a placeholder for the caller token. |
| `keyhole sidecar mcp-stdio --socket PATH` | A stdio MCP bridge to a socket-bound sidecar, for MCP clients that spawn commands. |
| `keyhole cache status` / `cache flush [--key k_…\|--store st_…\|--all]` | See `cache.md` §7. Works against the local daemon over its admin socket (`/run/keyholed/admin.sock`, root or the service group). |
| `keyhole doctor [--component cn_…\|sc_…]` | Checks, then prints pass/warn/fail with fixes (list below). |
| `keyhole config get\|set\|list\|path` | Reads and writes the user config file (§3). |

### `dispatch`

| Command | What it does |
|---|---|
| `dispatch login [--no-browser]`, `logout`, `whoami` | Human device flow, as for `keyhole` (`aud: dispatch-cli`). |
| `dispatch login --token-file PATH` | Machine use, also `DISPATCH_TOKEN_FILE`. The file holds `agent_id`, `dsp_agent_…` and optionally `dsp_ws_…` per workspace (TOML), mode 0600. A machine should prefer running a sidecar to holding these files. |
| `dispatch send --workspace ws_… [--to ag_…,…\|--except ag_…] [--tag t] [--expires 6h\|never] [--payload @file.json] [--parent msg_…] [--fire URL --trigger send\|all-read\|all-ack --fire-user U] [--listen --listen-user U]` | Sends a message. The fire-hook password is read from stdin (`--fire-password-stdin`), never from argv. A listener password is printed once. |
| `dispatch inbox [--workspace ws_…] [--unread] [--tag t] [--since 1h]`, `dispatch get msg_…`, `dispatch read msg_…`, `dispatch ack msg_…` | Agent inbox and receipts. |
| `dispatch tail --workspace ws_…` | Live SSE stream (humans see the workspace; agents see what is addressed to them). |
| `dispatch search QUERY [--workspace ws_…] [--tag t]` | Search. |
| `dispatch webhooks retry msg_…`, `dispatch webhooks listener-rotate msg_…` | Retry a fire hook; rotate a listener password (admins; printed once). |
| `dispatch sidecar install [--service …]` | Service install (§1). |
| `dispatch sidecar enroll [--enroll-token-file PATH \| --enroll-token-stdin]` | Reads `dsp_enr_…` from stdin, a file, or `DISPATCH_ENROLL_TOKEN`. Enrolls, then receives the bound agent's `dsp_agent_` and `dsp_ws_` tokens sealed to its key. **The agent process never sees them.** |
| `dispatch sidecar run` | Foreground daemon. Serves local HTTP (`/v1/…`, the same API without the token headers), SSE (`/v1/stream`) and MCP (`/mcp`, or `dispatch sidecar mcp-stdio`) to the bound agent. Pulls the agent's inbox and, with `--pull-webhooks`, fire-webhook deliveries it may POST locally (`allowedTargets`), so targets behind firewalls need no ingress. |
| `dispatch sidecar status` | Bound agent, held tokens (last four, delivered at, grace), connections, pull cursors, cache summary. |
| `dispatch sidecar rotate [--grace 10m\|0] [--end-grace] [--what credential\|agent-token\|workspace-token --workspace ws_…]` | Rotates the sidecar credential, or the agent's tokens through the sidecar. New tokens are delivered sealed, never printed. |
| `dispatch sidecar caller-token rotate`, `dispatch sidecar env`, `dispatch sidecar uninstall [--wipe]` | As for Keyhole. |
| `dispatch cache status` / `cache flush [--all]` | See `cache.md` §7. |
| `dispatch doctor` | Checks, as below. |
| `dispatch config get\|set\|list\|path` | The config file (§3). |

### Enrollment tokens never go on argv

Both CLIs scan their own argv for `kh_enr_`, `dsp_enr_`, `kh_conn_`, `kh_sc_`, `kh_call_`, `dsp_agent_`, `dsp_ws_`, `dsp_sc_` and `dsp_call_` before parsing. On a match they exit with code 2:

> A token was passed on the command line, where process lists and shell history can see it. It was not used. Revoke it in the UI and pass the new one on stdin, with --enroll-token-file, or via KEYHOLED_ENROLL_TOKEN / DISPATCH_ENROLL_TOKEN.

Environment variables are cleared from the daemon's environment once read, so child processes don't inherit them.

### `doctor` checks

- Config parses; the effective values are printed with their source (flag, env, file, default).
- Clock skew under 60 s (JWTs and TLS).
- DNS, TCP, TLS and proxy to the API and gateway (`api.keyhole.dev`, `gw.keyhole.dev`, `api.dispatch.dev`); a corporate proxy or MITM CA is detected.
- The credential is valid, not revoked, and not near expiry; grace status.
- **Sidecar exposure:**
  - the listen address is loopback, or the enrolled intent allows network exposure;
  - the socket's mode and group are right;
  - a caller token is set for TCP;
  - the effective bind matches the configured one.
- The state directory is 0700 and the files 0600, owned by the service user; core dumps are off.
- The cache DEK unwraps (TPM present, or the KMS key usable by this identity); cache age is within `maxStalenessSeconds`.
- **Vault connector:** each routed store is reachable and authenticates (runs the connection-test steps without a read probe, unless `--read-probe`). Ambient identity is detected (IMDS, IRSA, workload identity).
- **Keyhole sidecar, proxy mode:** the app trusts the local CA (`--app-env` checks `HTTPS_PROXY` and `NO_PROXY`).
- The version is within the server's supported window.

Exit codes: 0 ok, 1 warnings, 2 usage error or a token on argv, 3 auth failure, 4 network, 5 revoked (after a wipe), 78 config error (the daemon refuses to start).

## 3. Config file: format and precedence

TOML. **Secrets never go in config files.** The file refers to a token file path, or to the keychain.

Locations:

| Scope | `keyhole` | `dispatch` |
|---|---|---|
| User (CLI) | `~/.config/keyhole/config.toml` (`%APPDATA%\keyhole\config.toml`) | `~/.config/dispatch/config.toml` |
| System (daemons) | `/etc/keyhole/keyholed.toml` | `/etc/dispatch/sidecar.toml` |
| Explicit | `--config PATH` or `KEYHOLE_CONFIG` | `--config PATH` or `DISPATCH_CONFIG` |

**Precedence**, highest first:
1. command-line flags;
2. environment variables (`KEYHOLE_*`, `KEYHOLED_*`, `DISPATCH_*`);
3. the explicit config file;
4. the user file (CLI) or system file (daemon);
5. built-in defaults.

Then the **server's enrolled intent and policy cap the result.** Local config may only tighten them:
- a lower TTL or max-staleness;
- a smaller `allowedTargets`;
- loopback when the intent allows the network.

A daemon whose effective settings widen the intent (for example `--expose-on-network` when the enrolled intent is loopback) refuses to start with exit 78 and says which source asked for it.

```toml
# ~/.config/keyhole/config.toml
active_profile = "acme"

[profile.acme]
api = "https://api.keyhole.dev"
org = "org_acme"
output = "table"
# token_file = "/etc/keyhole/agent.token"   # machines only; the file must be 0600

# /etc/keyhole/keyholed.toml — written by `keyhole sidecar enroll`
[component]
id = "sc_payapi01"
kind = "sidecar"
gateway = "wss://gw.keyhole.dev"
state_dir = "/var/lib/keyholed/sc_payapi01"

[sidecar]
listen = "127.0.0.1:8787"          # or "unix:/run/keyholed/pay-api-01.sock"
expose_on_network = false
local_auth = "caller_token"        # or "unix_socket"
app_mode = "base_url"              # or "proxy"
protocols = ["http", "mcp"]

[cache]                             # may only tighten the server's policy
mode = "memory"
key_protection = "process_memory"
default_ttl_seconds = 300
max_staleness_seconds = 3600

[log]
level = "info"                      # never logs values, tokens or bodies
```

```toml
# /etc/dispatch/sidecar.toml — written by `dispatch sidecar enroll`
[component]
id = "dsc_deployer01"
agent = "ag_deployer"
gateway = "wss://gw.dispatch.dev"
state_dir = "/var/lib/dispatch/dsc_deployer01"

[sidecar]
listen = "unix:/run/dispatch/deployer.sock"
local_auth = "unix_socket"
protocols = ["http", "sse", "mcp"]

[pull]
messages = true
webhooks = true
allowed_targets = ["ci.acme.internal:443"]

[cache]
mode = "disk"
key_protection = "tpm"
```
