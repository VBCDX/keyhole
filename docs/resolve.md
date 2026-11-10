# Resolve: how a sidecar gets a Keyhole-held value

Model A, rules 2–4 (the README, "Where Keyhole sits").
This page is the specification the sidecar side (the vbcdx sidecar's sealed-value path) implements.
It rides the control-plane link: vbcdx-control forwards the request to Keyhole over mutual TLS.

## The path

```
agent ─▶ sidecar ──tunnel (mTLS)──▶ vbcdx-control ──service link (h2, mTLS)──▶ Keyhole
         POST /control/v1/keyhole/orgs/{org}/resolve      POST /orgs/{org}/resolve
```

1. The sidecar sends `POST /control/v1/keyhole/orgs/{org}/resolve` over its tunnel, with its session. The body is below.
2. vbcdx-control ends the tunnel, checks the sidecar's certificate and the session bound to it, and scopes `{org}`: a caller who isn't a member is refused there and nothing is sent. Then it forwards to Keyhole's enrolled URL, attaching `Vbcdx-Assertion` (aud `keyhole`, jti = the request id). Only `Content-Type`, `Vbcdx-Assertion` and `Vbcdx-Request-Id` are forwarded.
3. Keyhole verifies the assertion, finds the grant, opens the Local value, and returns it **sealed to the key the sidecar sent**. vbcdx-control relays ciphertext.
4. The sidecar opens it, attaches it to the agent's request, and calls the service **from the agent's host** (rule 4). The value never reaches the agent process.

## Request

`POST /orgs/{org}/resolve`, `Content-Type: application/json`, at most 8 KiB, no unknown fields:

```json
{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": "<base64url, 32 bytes>"}
```

`eph_pub` is a **fresh X25519 public key for this one request**. The sidecar keeps its private half in memory for this request only.

## What Keyhole checks, in order

| Check | Refusal |
|---|---|
| `Vbcdx-Assertion` present and valid: EdDSA, typ `vbcdx-assertion+jwt`, iss `vbcdx-control`, aud `keyhole`, lifetime ≤ 60 s (± 5 s skew), signature against control's keys, jti not seen before | 401 `unauthorized` (no audit row: the caller isn't known yet) |
| The body is `{tool, slot, eph_pub}`, all non-empty | 400 `bad_request` |
| `claims.sidecar` present (rule 3) | 403 `forbidden` |
| `claims.org.id` equals `{org}` | 403 `forbidden` |
| An enabled grant of the **published** tool, in an active workspace of an active org, to a workspace where (`claims.kind`, `claims.sub`) is a member; its `slot` filled with an active Local key | 404 `not_found` |
| The grants that match all name the same key, at the same tool version | 409 `conflict` |
| The tool version the grant uses (its pinned one, else the latest published) defines the requested slot | 404 `not_found` |
| The key has a value | 404 `not_found` |
| `eph_pub` is a usable X25519 key (not a low-order point) | 400 `bad_request` |

Every refusal body says only "the value can't be resolved". After the assertion is verified, each refusal and each success writes an audit row: who, through which sidecar, the tool and slot, and the outcome class. The row never holds the value, its length or the key's address.

The signing keys come from `GET /control/v1/keys`, fetched with Keyhole's service certificate over the tunnel listener (`CONTROL_URL`; where an nft rule lets the keyhole user reach only `lo`, that's `https://127.0.0.1:8443`). An unknown kid refreshes them at most once every 30 s. A dropped key still verifies for 70 s, and a failed refresh keeps the old keys.

## Reply

`200`:

```json
{"sealed": {"alg": "X25519-HKDF-SHA256-A256GCM", "epk": "…", "nonce": "…", "ct": "…"},
 "jti": "req_…",
 "ttl": 300,
 "tool": {"version": 1, "base_url": "https://llm.example.com",
          "actions": [{"method": "GET", "path": "/v1/models"}, {"method": "POST", "path": "/v1/chat/completions"}],
          "slot": {"location": "header", "field": "Authorization", "scheme": "Bearer"},
          "timeout_ms": 60000,
          "private_allow": ["10.0.0.20/32"]}}
```

`jti` is the assertion's jti, which the seal's AAD binds (step 4 below). vbcdx-control mints it for each forward and doesn't return it to the sidecar, so the reply carries it. It isn't secret. A reply still opens only with this request's private key.

`ttl` is how many seconds the sidecar may keep the value: the key's `cache_ttl_seconds`, else the store's `cache_default_ttl_seconds`, else 300. A `0` means use it for this request only.

`tool` is the definition of the tool version the grant uses (its `pinned_version`, else the tool's `published_version`). The sidecar makes the call from it:
- only to `base_url`;
- only for a method + path in `actions`;
- with the value placed per `slot` (for LiteLLM: header `Authorization: Bearer <value>`);
- within `timeout_ms`;
- to a **private** address (loopback, RFC 1918, ULA, CGNAT) only when `private_allow` names it. Link-local (cloud metadata included), unspecified, multicast and broadcast addresses are never reached, whatever the list says. `private_allow` is always present, `[]` when the tool has none.

It holds no secret.

### Letting a tool reach a private address

A tool whose service lives on a private network (say an LLM gateway, `llm.example.com` → `10.0.0.20`) needs its address allowed:

```sh
keyhole tools private-allow --org ORG --tool t_litellm --cidr 10.0.0.20
# tool t_litellm version 2 published: private addresses allowed: 10.0.0.20/32
```

- **Entries are IP addresses or CIDRs**, repeat `--cidr` for more. An address is stored as its `/32` or `/128`. Refused:
  - a host name (a name doesn't pin the address it resolves to);
  - anything in 6to4 (`2002::/16`), NAT64 (`64:ff9b::/96`, `64:ff9b:1::/48`) or IPv4-mapped space as a CIDR: the sidecar reads those as their IPv4 address, so write the IPv4 address or CIDR. A bare IPv4-mapped address is taken as its IPv4 address;
  - a range wider than `/8` (IPv4) or `/16` (IPv6), so no pair of halves adds up to everything;
  - host bits set, or a zone id;
  - a range overlapping link-local (cloud metadata), unspecified, multicast or reserved space, which a sidecar never reaches anyway.
- **`--none` clears the list.**
- **A tool version never changes,** so this publishes a new version (the current one with the list replaced) and audits it (`tool version published`). Grants that follow the published version get it at their next resolve; a grant pinned to a version keeps that version's list (the command says how many).
- **In a fixture** (`keyhole load`), a tool version's snapshot may carry `"privateAllow": [...]` in the same canonical form (sorted, no duplicates, `/32` written out); anything else fails the load.

**Resolve is sidecar-internal.** No op an agent can call exposes it, and the sidecar never forwards it on an agent's behalf: an agent holding the reply could open the value with its own key. The only caller is the sidecar's own egress path.

## The seal

All byte fields are base64url without padding.

1. Keyhole makes a fresh X25519 key pair (`esk`, `epk`) per reply.
2. `shared = X25519(esk, eph_pub)`. A low-order `eph_pub` gives an all-zero secret, and is refused.
3. `key = HKDF-SHA256(ikm = shared, salt = eph_pub ‖ epk, info = "vbcdx-keyhole-seal-v2", L = 32)`. The salt is the recipient's 32 bytes, then the ephemeral key's 32 bytes.
4. `aad = "vbcdx-keyhole-seal-v2"`, then each of these as a 4-byte big-endian length and its bytes: the jti, the tool, the slot, the tool version (decimal) and `tooldef`, the 32-byte digest below. A number (the tool version here, `timeout_ms` below) is its plain decimal form: no sign, padding or leading zeros (Go's `strconv.Itoa`), so `0` is `"0"`. The jti is the assertion's: vbcdx-control's request id for this forward, also returned in the reply's `jti`. A reply can't be opened as another request's, tool's, slot's, tool version's or **tool definition's**. The lengths mean no text in any field can make two different inputs share an AAD.

   `tooldef = SHA-256("vbcdx-keyhole-tooldef-v1" ‖ …)` over **every field of the reply's `tool` the sidecar uses for its call**, in this order: `base_url`, `timeout_ms` (decimal), `slot.location`, `slot.field`, `slot.scheme`, then the number of `private_allow` entries (4-byte big-endian) and each entry, then the number of `actions` (4-byte big-endian) and each action's `method` and `path`. Each string is a 4-byte big-endian length and its bytes; arrays keep the reply's order. It is length-prefixed fields, not canonical JSON, so every language computes the same bytes.

   Every string is hashed **exactly as the reply sends it**: nothing is normalized first (no case folding, no trailing-slash or default-port rewriting of `base_url`). The sidecar hashes the exact string it will use for the request, and uses the string it hashed.

   **The sidecar rebuilds `tooldef` from the exact definition it is about to send the request with, and opens the value only with that AAD.** A reply whose definition was changed on the way (a different `base_url`, an extra action, another private address) doesn't open: one error, no request.
5. `ct = AES-256-GCM(key, nonce = 12 random bytes, plaintext = the value, aad)`, the tag appended.

The sidecar opens it with `X25519(eph_priv, epk)` and the same HKDF and AAD. Any failure is one error, and the sidecar drops the request.

### Test vector

Every byte is fixed. Keyhole's Go (`internal/seal/vector_test.go`) and an independent Python implementation (the `cryptography` package) give the same digest, AAD and ciphertext; the sidecar checks its AAD and its open against them.

| | |
|---|---|
| recipient private key | 32 × `0x11` |
| recipient public (`eph_pub`) | `e06Qm75__kTEZaIgA31gjuNYl9Me-XLwf3SJLLD3PxM` |
| ephemeral private key | 32 × `0x22` |
| `epk` | `D6poTtKIZ7l_Smot7l34zpdOdrcBjj8iocTPJnhXDyA` |
| `nonce` | 12 × `0x33` = `MzMzMzMzMzMzMzMz` |
| jti, tool, slot | `req_vector`, `t_litellm`, `litellm_key` |
| tool definition | version `1`, `base_url` `https://litellm.example.invalid`, `timeout_ms` `60000`, slot `header` / `Authorization` / `Bearer`, `private_allow` `["10.0.0.20/32"]`, actions `GET /v1/models`, `POST /v1/chat/completions` |
| `tooldef` (hex) | `36f6359e8bc01be18445221a09fcbb24c9afd94229e98a4d1eb9001bb109084c` |
| `aad` (hex) | `76626364782d6b6579686f6c652d7365616c2d76320000000a7265715f766563746f7200000009745f6c6974656c6c6d0000000b6c6974656c6c6d5f6b657900000001310000002036f6359e8bc01be18445221a09fcbb24c9afd94229e98a4d1eb9001bb109084c` |
| plaintext | `sk-test-vector-not-a-key` |
| `ct` | `SYi5wggpMgN6f1seVz3zdQYiw1corvpc6S_7RZoXRWp2m_kMW-y4Bw` |

Seal v1 (which bound only jti, tool and slot) is gone: Keyhole seals v2 only, and a sidecar that builds the v1 AAD can't open a v2 reply.

## What this protects, and what it doesn't

- The control plane relays ciphertext, so the value is not in its memory, logs or traces.
- Each reply is bound to one request, tool and slot (the AAD), and can't be opened without that request's private key.
- **It does not protect against a malicious control plane.** vbcdx-control mints the assertion naming the sidecar, and rule 2 trusts it, so a control plane that lies can ask with its own ephemeral key. A registered per-sidecar key that the control plane can't substitute is later work.
- **The tool definition is bound by the seal (v2).** The reply's `tool` block (`base_url`, `actions`, `slot`, `timeout_ms`, `private_allow`) and the tool version are in the AAD (`tooldef`, step 4). So a control plane that changes the definition on the way can't point a correctly-sealed key at another host or widen `private_allow`: the value doesn't open, and nothing is sent.
  - **What isn't bound:** `ttl`. It is how long the sidecar may keep the value, not where it goes, so a control plane could lengthen it. That's harmless while the sidecar keeps nothing (it resolves for every call); it must be bound before a sidecar caches a value for its `ttl`.
  - **What else holds:** the sidecar never reaches link-local addresses (cloud metadata), unspecified, multicast or reserved ones; it checks every resolved address and connects only to those; and Keyhole never stores a `private_allow` the sidecar would misread.
- Revocation: a revoked or disabled grant (`keyhole grants disable`), or a member taken out (`keyhole members remove`), stops at the next resolve; a value already held lives at most `ttl` (300 s by default). Pushing revocations to the sidecar is later work.
- Membership: `keyhole members add` gives the user or agent **every enabled grant of that workspace**. `members remove` removes plain members only; a `workspace_admin` row is vbcdx-control's and is left as it is.

## Setting it up by hand

`vbcdx install service keyhole-server` does all of this for you (the vbcdx installer's
`docs/keyhole-server.md`). By hand:

1. `keyhole service csr --key-out /etc/keyhole/svc.d/service.key --csr-out /etc/keyhole/svc.d/service.csr` (as the keyhole user; the key is created 0600).
2. On the control plane's admin socket: `vbcdx-control services add keyhole --url https://127.0.0.1:8444 --csr …/service.csr --cert-out …/service.crt`. The certificate names `vbcdx:service/keyhole` and `127.0.0.1` and lives 30 days; renew with `--replace`.
3. In `/etc/keyhole/keyhole.env`: `SERVICE_LISTEN_ADDR=127.0.0.1:8444`, `SERVICE_CERT_FILE`, `SERVICE_KEY_FILE`, `CONTROL_CA_FILE` (the control CA), `CONTROL_URL=https://127.0.0.1:8443`, `KEK_FILE`, and `CONTROL_SERVER_NAME` if control's tunnel certificate doesn't name 127.0.0.1.
4. The rows: the org id must be **the control plane's org id** (vbcdx-control scopes and asserts that id; [local-store.md](local-store.md), "The org id", says how to read it), and the member is the control-plane user (or, once agents have their own sessions, the agent) the sidecar's session belongs to: `member_kind` = the assertion's `kind`, `member_id` = its `sub`.
