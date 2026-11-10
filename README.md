# Keyhole

**Keyhole is a secrets broker.** It brokers access to secrets so applications get what they need
without the secrets ever being exposed to them.

Part of the [VBCDX](https://github.com/VBCDX) suite. **Status: in development** — this repo is a
published snapshot; canonical development happens on the VBCDX Forgejo.

---

## The idea

An application should be able to use a credential without ever holding it. Keyhole sits between the
two: it connects to the places secrets actually live, and hands out scoped, short-lived access
instead of copies.

Keyhole is **standalone first**. It ships its own web admin — UI and API — and stands up on its own
with `docker compose up --build` ([Docker Compose](#docker-compose-keyhole-plus-postgres), after
copying `.env.example` to `.env`). No centralized console, no dependency on the rest of the suite.

## Where Keyhole sits: the VBCDX architecture

These five rules are binding for every VBCDX service:

> 1. The control plane is the set of server services.
> 2. ONE common auth across them, which lives with the control plane, never on the sidecar.
> 3. A high-performance secure TUNNEL from sidecar to control plane is required, and ALL requests go via the sidecar except the web console and later REST plugins.
> 4. Service calls ALWAYS leave from the agent environment via the sidecar, NEVER from a control-plane server; no server-side tool proxying.
> 5. Control-plane services may be separate binaries; there's ONE common sidecar.

The Keyhole server is a **control-plane service**. It stores keys and tool definitions, decides
grants, and issues them. **It never calls the service a tool is for.** That is Model A:

1. An agent calls a tool on its local **sidecar** (the one VBCDX sidecar on its host; it carries every
   service's calls, with no per-service code).
2. The sidecar asks the Keyhole server for the granted slot value over the **tunnel** to the control
   plane. It authenticates with its own client certificate, and the request carries the agent's token.
3. The Keyhole server checks the grant and returns the value **sealed to that sidecar's X25519 key**.
   The value lives for a short time: the grant's TTL, 300 s by default and 24 h at most.
4. The sidecar opens it, in its one sealed-value path, and attaches it to the request. It calls the
   service **directly from the agent's host**, so the service only ever sees the agent's address.
5. A revoked grant stops working within its TTL. The control plane also pushes the revocation, so the
   sidecar drops its copy early.

Tests guard this. What they cover, exactly:

- **Keyhole's own Go source.** `internal/egress` parses every non-test `.go` file of every package
  the server binary builds, in all build-tag variants. It lists each use of the standard-library
  network and process identifiers it watches, each banned import (`os/exec`, cgo, `log/syslog`, SSH,
  HTTP/2, cloud and vault SDKs, …), each dot import, each `go:linkname`, and each import of a
  third-party package that can reach the network. The list must equal a short allow-list exactly,
  counted per entry: the healthcheck client, the OIDC sign-in client (temporary, until
  `vbcdx-control` owns sign-in), and the database drivers.
- **The dependency graph.** Every third-party package the binary links that is, or transitively
  imports, `net`, `net/http`, `crypto/tls`, `os/exec`, `log/syslog` or `golang.org/x/crypto/ssh`
  must be on a dependency allow-list that says why it may. A new network-capable dependency fails
  the test.
- **The healthcheck at runtime.** `keyhole healthcheck` uses a client that resolves the host first
  and dials only when every address is loopback (127.0.0.0/8, ::1). It ignores proxy settings and
  follows no redirects.

Not covered, exactly:

- **Calls made through an allowed import.** Inside `internal/auth` (go-oidc, oauth2, and the
  OIDC `*http.Client` field) and `internal/store` (the database handle), another call to a
  different address isn't caught. The guard pins which files may import them and how many clients
  and `sql.Open` calls exist, not each call. For OIDC this goes away when `vbcdx-control` owns
  sign-in.
- **Standard-library packages that start a process or connect indirectly** without one of the
  watched names, e.g. `go/build` (`build.Import` runs `go list`).
- **Assembly (`.s`) and C (`.c`) files**, and what an allowed dependency does inside itself.

Two parts of the design below are later work:

- **Public stores, connectors, and the "Test" step** come after the local database is ready. When
  they are built, they follow rule 4: the connection leaves from the agent environment via the
  sidecar, never from the Keyhole server.
- **The organization alert webhook** is delivered via the sidecar, the same as Dispatch's webhooks.

## Install

Keyhole runs as a **VBCDX control-plane service**. Agents never call it directly. An agent's
sidecar asks the Keyhole server for a granted value over the tunnel, and the control plane checks
who the agent is first (Model A, above).

**Install it with the VBCDX installer.** The installer verifies the signed release before anything
is written (the same trust root as the sidecar). Releases are the npm package `@vbcdx/keyhole`; the
`@vbcdx` scope's registry comes from your npm config. Only `0.1.0-dev.*` pre-releases exist today
(see Status), so `<x.y.z>` is one of those.

```sh
npm i -g @vbcdx/vbcdx      # the installer
sudo vbcdx install service keyhole-server --version <x.y.z> \
  --link-listen 127.0.0.1:8444 --control-url https://<tunnel name>:8443 \
  --control-ca <control CA file> --dry-run      # review the plan, then run it without --dry-run
```

The first install makes the KEK, the link key and its certificate request. The unit isn't started
until the control plane has signed that request (next section). The whole procedure, every flag,
`doctor`, upgrade and remove are in the installer's `docs/keyhole-server.md`. What the whole
suite protects, where each secret lives and this release's limits are in the installer's
`docs/security.md`. The `@vbcdx/keyhole`
package isn't meant to be run from `node_modules`: it has no `bin` and no install scripts. How a
release is built, signed and published: [`docs/release.md`](docs/release.md).

To run it from a checkout instead (evaluation and development), see
[Running it locally](#running-it-locally).

## Connect to the control plane

After the install above, on the control-plane host:

1. **Register Keyhole.** `vbcdx-control services add keyhole --url https://127.0.0.1:8444 --csr
   <link.csr> --cert-out keyhole.crt` signs the install's certificate request (the CSR is a
   request, not a secret). Add `--replace` if the control plane already has a `keyhole` service.
   That line is abridged: run it as the installer's `docs/keyhole-server.md` ("The link
   certificate") shows, which copies the CSR where the `vbcdx-control` user can read it and runs
   the command as the `vbcdx-control` user, with its full path, `--instance` and `--instance-dir`.
2. **Give Keyhole the certificate:** `sudo vbcdx install service keyhole-server --version <x.y.z>
   --service-cert keyhole.crt`. Install runs `keyhole check` (certificate, key, KEK, control CA,
   control URL) and starts the unit only if every check passes. `sudo vbcdx doctor --service
   keyhole-server` checks it again at any time.
3. **Load a test key under the control plane's org id** (`org_…`, not its slug: the control plane
   asserts that id, and Keyhole finds nothing under any other): the tool, key row and grant with
   `keyhole load --file`, then the value on standard input with `keyhole keys put`, never on the
   command line. [`docs/local-store.md`](docs/local-store.md) has the exact commands and how to read
   the org id.
4. **Resolve it through a sidecar.** On an agent host, sign in with `vbcdx config service sidecar
   auth login pw`, then call the tool through the sidecar's socket. The sidecar fetches the sealed
   value, opens it and calls the tool's service itself. vbcdx-core's `docs/tool-calls.md` has the
   request and a worked example.

The installer writes the settings below for you. Set by hand, they are `SERVICE_LISTEN_ADDR`,
`SERVICE_CERT_FILE`, `SERVICE_KEY_FILE`, `CONTROL_CA_FILE`, `CONTROL_URL`, `KEK_FILE` and, if the
control plane's tunnel certificate doesn't name its address, `CONTROL_SERVER_NAME`
([`docs/resolve.md`](docs/resolve.md), "Setting it up by hand").

The KEK the installer makes is a file on the Keyhole host. That's fine for test keys, and only
for them: [`docs/local-store.md`](docs/local-store.md), "What this protects against", says why.

## What works today: the Local store

This release has **one store type, the Local store**. Values live in Keyhole's own database,
sealed with the KEK. A key whose store type isn't registered is refused. You manage them with Keyhole's admin
CLI on the Keyhole host; there is no HTTP route that writes a value or a grant:

- `keyhole keys put` (a value, on standard input) and `keyhole load --file` (tools, key rows,
  grants): [`docs/local-store.md`](docs/local-store.md);
- `keyhole grants disable|enable`, `keyhole members add|remove` and `keyhole tools private-allow`:
  [`docs/resolve.md`](docs/resolve.md), which also describes the resolve request, its checks and
  the seal.

**Store types share one contract, and the Local store is the one built in.** Every store type,
the Local store included, implements the same interface, and resolve asks the type a key names
for its value; it knows no type in particular ([`docs/stores.md`](docs/stores.md)). A third party
adds a store type as a source change: implement the contract and register it where the server
starts. Loading a store type from outside the binary isn't supported in this release. External
stores (OpenBao first) are planned; the two sections below describe that design, not something
you can use yet.

## Stores and connectors (planned)

**Not built yet** (see above for what works today). In the planned design, Keyhole reads secrets
from external **stores**, the first being [OpenBao](https://openbao.org/), the open-source fork of
Vault.

A store has to be reachable, and there are two honest answers to how:

| | |
|---|---|
| **It's public** | Keyhole reaches the store directly over the network. You allow-list Keyhole's egress addresses on your side. |
| **Through a Keyhole connector** | The store is private. You enroll a connector — a small agent that runs next to the store and dials **out** to Keyhole, so nothing inbound has to be opened. |

The connector exists because the common case is a vault that is deliberately not on the public
internet. Asking someone to expose it in order to secure it is the wrong trade.

## Connecting a store (planned)

**Not built yet.** The planned wizard is six steps, and each one is allowed to fail in a way you can act on.

1. **Basics** — name the store and point at it.
2. **How to reach it** — public, or through an enrolled connector. Picking a connector (`edge-01`,
   say) routes traffic through it rather than direct.
3. **Sign in** — AppRole is the first supported method.
4. **Secrets** — choose which paths this store exposes.
5. **Scope** — decide who and what may ask for them.
6. **Test** — Keyhole actually connects and reports what it found.

### Failure is part of the flow, not an afterthought

Two states matter enough to design for explicitly:

- **Sealed.** An OpenBao vault that has been restarted is sealed, and *sealed is not broken* — it is
  the expected state waiting for unseal. Keyhole says so and tells you what to do, rather than
  reporting a generic connection failure.
- **No route to the address.** Usually means the store is private after all. The error says exactly
  that, and points at the connector as the fix — because that is almost always what the person
  actually needs.

A store that passes step 6 is reported **healthy**, and unhealthy stores say which of the above
they are.

## Design commitments

- **The secret does not pass through the application.** That is the whole point; anything that
  compromises it is a bug.
- **Errors name the cause and the next action.** "Sealed — unseal it" beats "connection failed".
  A message that does not tell you what to do next is not an error message.
- **Outbound-only for private stores.** Enrolling a connector must never require an inbound hole.
- **Standalone.** Own web admin, own backend, own deploy. Runs with or without the suite.
- **Auth vs authz split.** Identity comes from the suite auth layer over an OIDC seam and the IdP is
  swappable; **workspace and module authorization belongs to Keyhole**, not the IdP.
- **Open core.** This module is FOSS. The commercial layer depends on it, never the reverse.

## Where the work happens

Canonical development is on the VBCDX Forgejo. **This GitHub repo is a published snapshot** — it is
pushed to, not developed in. Issues and PRs opened here may be moved. The workflows under
`.forgejo/workflows/` run only there, on a self-hosted runner; GitHub runs only
`.github/workflows/pages.yml` (the mock demo), and building needs no secrets.

## Status

Early, and released as development pre-releases (`@vbcdx/keyhole` `0.1.0-dev.*`; see
[Install](#install)). In so far: the Local store with its admin CLI, and resolve to a sidecar over
the control-plane link ([What works today](#what-works-today-the-local-store)); sign-in (local
accounts, plus optional single sign-on through any OpenID Connect provider); Postgres or SQLite;
and the data model behind every screen, with read-only organization and store views and demo data.
External stores and connectors are planned.

## Licence

MIT. See [LICENSE](LICENSE).

## Analytics

Optional, minimal GA4 instrumentation records aggregate activation: `login` (single sign-on or
local) and one `page_view` per app load. It is **off by default**. To turn it on, set
`GA_MEASUREMENT_ID` to your GA4 web data stream's `G-XXXXXXXXXX` id: the server serves it to the
web app and extends the CSP to the GA4 hosts. With it unset no tag loads, no cookie is set, no
event is sent and the CSP stays strict.

`store_connected` (first healthy store) is defined in the event contract but **not emitted yet**:
connecting a store is not built yet (the Stores screens are read-only), so the change that builds
it wires that event.
Only the documented events/params are ever sent; no emails, names, account ids or secret values.
[`docs/analytics.md`](docs/analytics.md) defines the metrics and windows (activation, sign-in
completion, 7/28-day returns) so audits can cite as-of/window/source.

## Running it locally

Keyhole is one Go binary (`cmd/keyhole`) that serves the API and the web app (built into the
binary). It needs no other services: the database is SQLite by default, or Postgres. So far it
signs you in, shows who you are and signs you out; stores and connectors come next. Every setting is
an environment variable; [`.env.example`](.env.example) lists them all.

You need Go (the version in `go.mod`) and Node 22.

```sh
(cd web && npm ci && npm run build)    # build the web app into web/dist
go build -o keyhole ./cmd/keyhole       # embeds web/dist
```

### Local accounts, SQLite (no other services)

```sh
export AUTH_MODE=local                  # the default
./keyhole user create --email you@example.com --name "Your Name"   # prompts for a password (12+ characters)
./keyhole                               # serves http://localhost:8080
```

Keyhole listens on `localhost:8080` by default — loopback only, so a direct run is not
reachable from the network. To serve it to anyone else, put a TLS-terminating reverse proxy
in front and set `LISTEN_ADDR=:8080` (or `0.0.0.0:8080`) with an https `PUBLIC_URL`; binding
beyond loopback without that would serve sign-in and the session cookie in the clear. The
container image binds `:8080` inside the container, and `docker compose` publishes that port
on `127.0.0.1` only.

Data lives in `./data` (`DATA_DIR`). To create the first account in a container without a shell,
set `BOOTSTRAP_ADMIN_EMAIL`, `BOOTSTRAP_ADMIN_PASSWORD` and optionally `BOOTSTRAP_ADMIN_NAME`. They
are used only when the database has no users at all.

Whatever your umask, Keyhole creates nothing that group or others can read: the data
directory is `0700`, and `keyhole.db` with its `-wal`/`-shm` files is `0600`. Every command
sets the process umask to `077` at start. A database or data directory an older release left
readable by others is tightened the next time any command opens it (`0600` / `0700`, changed on
the opened file, never by path), with one warning per file saying since when it was readable and
that its local accounts' password hashes may have been read. A symlinked database or data
directory, or one another user owns, is refused instead. `keyhole check` reports these without
changing anything. A loose KEK is always refused, never fixed: rotate the keys it sealed. The CSR
from `keyhole service csr` is `0600` too: to pass it to `vbcdx-control services add` as another
user, copy it as root (it is public, so `0644` is fine for the copy).

### Settings from a file, and checking them

`keyhole --env-file FILE <command>` reads `KEY=VALUE` settings from `FILE` before running any
command, and they win over the environment. `--env-file` must come first, before the command. It is the same file a systemd unit names in
`EnvironmentFile=`, so an admin command sees exactly what the service runs with:

```sh
runuser -u vbcdx-keyhole -- keyhole --env-file /etc/vbcdx-keyhole/default/keyhole.env user create --email you@example.com
```

Only what systemd reads the same way is accepted: blank lines, `#` and `;` comments, and
`KEY=VALUE` with the spaces and tabs around the key and value trimmed (nothing else is trimmed).
A quoted value, a backslash, a control character other than a tab (a `\r` only as part of a
CRLF line end), a repeated key or a line with no `=` is refused. The file must be a regular
file (not a symlink), owned by root or by the user running `keyhole`, and not writable by group
or others. Its directories aren't checked, so keep it in a directory only root can write (the
installer's `/etc/vbcdx-keyhole/<instance>` is).

`keyhole check` loads the settings by the rules `serve` uses. When `SERVICE_LISTEN_ADDR` is set,
it also opens each file of the control-plane link as `serve` would: the service certificate and
its `0600` key (the pair must fit and name `vbcdx:service/keyhole`), the control CA, and the
`0600` KEK owned by this user. It dials nothing, binds nothing and opens no database, so it can
run before the server starts (systemd `ExecStartPre=`). Exit 0 means the settings and those
files are what `serve` accepts; it doesn't open `DATA_DIR` or the database, doesn't bind the
listeners, and doesn't check that the certificate is still within its validity dates (its
expiry is printed).

### Single sign-on with Zitadel (optional)

**Single sign-on is optional.** Local accounts are the default (`AUTH_MODE=local`) and need no
identity provider: leave every `OIDC_*` setting blank and Keyhole never contacts one. To add
single sign-on, use Zitadel or any other OpenID Connect provider that supports the authorization
code flow with PKCE and a client secret; Keyhole finds the provider's endpoints through standard
OpenID discovery on the issuer URL. The `AUTH_MODE` value is named `zitadel` for historical
reasons. With `AUTH_MODE=zitadel` or `both` and `OIDC_ISSUER`, `OIDC_CLIENT_ID` or
`OIDC_CLIENT_SECRET` missing, Keyhole refuses to start and names the missing setting.

Create a **Web** application in Zitadel using the authorization code flow with PKCE and a client
secret. Register the redirect URI `$PUBLIC_URL/auth/oidc/callback` and the post-logout URI
`$PUBLIC_URL/`. Then:

```sh
export AUTH_MODE=zitadel                # Zitadel only
export PUBLIC_URL=https://keyhole.example.com     # http is accepted only for localhost
export OIDC_ISSUER=https://idp.example.com      # your OpenID Connect provider
export OIDC_CLIENT_ID=...
export OIDC_CLIENT_SECRET=...
export SESSION_SECRET="$(openssl rand -base64 48)"
./keyhole
```

The first Zitadel sign-in creates a Keyhole user, linked by issuer and subject. A Zitadel account
is never linked to a local account just because the emails match. Signing out ends the Keyhole
session and then the Zitadel session.

With Zitadel on, a visitor with no Keyhole session is first sent through a silent sign-in
(`prompt=none`), so a Zitadel session from another suite app signs them straight in. After an
explicit sign-out Keyhole skips the silent sign-in until the next sign-in or until the browser
closes, whichever comes first, the same as Dispatch. The `keyhole_signed_out` marker cookie behind
this is a session cookie (`__Host-` prefixed when cookies are Secure): Path `/`, HttpOnly,
SameSite=Lax, with no Max-Age or Expires.

### Both

`AUTH_MODE=both` offers Zitadel and local accounts on the same sign-in page. Set the Zitadel
variables above, and create local accounts with `keyhole user create`.

### Demo data

`./keyhole seed` loads the mock's demo organization, Acme Corp, with its stores, keys, tools,
workspaces, agents, cabinets, connectors and audit log. Running it again changes nothing. Set
`SEED_DEMO_DATA=true` to load it automatically when the database has no organizations. With the
setting on, anyone who signs in without an organization becomes an Owner of Acme Corp, so Keyhole
refuses to start unless `PUBLIC_URL` is loopback (`localhost`, `127.0.0.1` or `::1`) — the grant
is for demos on your own machine. The demo data holds no real secrets, and nobody can sign in as
one of the demo people. The tables behind each screen are described in
[docs/data-model.md](docs/data-model.md).

### Docker Compose (Keyhole plus Postgres)

```sh
cp .env.example .env                    # set POSTGRES_PASSWORD, plus BOOTSTRAP_ADMIN_* for a first account
docker compose up --build               # http://localhost:8080
```

That is all a fresh clone needs: no identity provider, no other services. Without
`BOOTSTRAP_ADMIN_EMAIL` and `BOOTSTRAP_ADMIN_PASSWORD` there is no account to sign in with.
`scripts/docker-smoke.sh compose` checks a running stack (health, the sign-in page, a bootstrap
admin sign-in, no OpenID Connect activity); the Forgejo CI runs it on every pull request.

The image is distroless and runs as a non-root user. Compose publishes Keyhole on 127.0.0.1 only.

### Health checks (there is no shell in the image)

The runtime image deliberately ships no shell and no network client. Deploy platforms must not
run a check inside the container as `/bin/sh -c "curl ... || wget ..."`: neither exists. Point
the platform's health check at one of these instead:

- Docker's own health check, which the image defines
  (`HEALTHCHECK ... CMD ["/usr/local/bin/keyhole", "healthcheck"]`). Docker executes it
  directly, with no shell, and `docker compose` uses it automatically.
- An external HTTP probe: `GET /healthz` on port 8080, expecting `200`.

`keyhole healthcheck` only ever connects to loopback. A server bound to every interface (`:8080`,
`0.0.0.0`, `[::]`) is probed on loopback. A server bound to one explicit non-loopback address
can't be probed from inside this way; use the external HTTP probe for it.

### Commands and exit codes

`keyhole help` lists every command, and `keyhole <command> --help` prints one command's usage
(it never runs the command: `healthcheck --help` dials nothing). Every command exits with:

| Code | Meaning |
|---|---|
| 0 | success (`healthcheck`: `/healthz` answered) |
| 1 | the command failed; the message says why |
| 2 | usage: an unknown command or flag, or a missing or bad argument |

2 is the usage code of the other VBCDX command-line tools too, so a script reads them alike.

### Developing the web app

Run the server on :8080 as above, then `cd web && npm run dev`. Vite proxies `/api` and `/auth`
to the server, so cookies work as they do in production.

### Tests

```sh
go test ./...                                                    # SQLite only
KEYHOLE_TEST_POSTGRES_URL='postgres://USER:PASSWORD@localhost:5432/DB?sslmode=disable' go test ./...
```

With a Postgres URL, every database test also runs against Postgres, each test in its own schema.
CI runs both, plus the web build and the Docker build.
