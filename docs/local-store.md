# The Local store: values, the KEK and the admin CLI

This page says how a Local key's value is
stored, who can write it, and what that does and does not protect against.

## The org id: the control plane's

Every row Keyhole resolves belongs to an org, and that org must be **the
control plane's own org id** (`org_…`): vbcdx-control asserts that id on every
resolve, and Keyhole finds nothing under any other. It's the id, not the slug
(`acme`) that sits next to it. Read it on the control plane's host, as
the `vbcdx-control` user (`--instance`/`--instance-dir` as for every command):

- `bootstrap --org NAME` printed it when it made the org, as `data.org.id`;
  `orgs create` printed it as `data.id`.
- Later, with a sidecar enrolled in the org: `sidecars list --org SLUG` (the slug
  works here) prints the id as `org` on each sidecar.

Use that id for `--org` below and for every `org_id` in a fixture you `load`.
`docs/examples/example-litellm.json` uses `org_example`: replace it with yours
(every `org_id`, and the `orgs` row's `id`) before you load it. One Keyhole can hold several orgs; each resolves only under its
own id.

## Writing a value

Values come in one way only: on standard input to `keyhole keys put`, on
the host Keyhole runs on.

```sh
keyhole kek generate --out /etc/keyhole/kek          # once, as the keyhole user
keyhole load --file my-org.json                      # the tool, key row and grant, under your org id
some-secret-source | keyhole keys put --org org_… --key k_litellm_test --kek-file /etc/keyhole/kek
```

- `keys put` reads the value from a pipe or file. It refuses a terminal, so a
  value is never typed where it echoes, and it has no flag or argument that
  takes a value. One trailing newline (`\n` or `\r\n`) is dropped; nothing else
  is changed. Values are 1 byte to 64 KiB.
- Each put stores the next version and marks the previous one superseded. The
  key's address points at the new version.
- The audit log records "value written (version N)" for the key, with actor
  `system` / `keyhole-cli`. It never records the value or its length.
- No read says how long a value is: a put clears `keys.length`, the column
  `GetKey` and `ListKeys` return. `local_key_values.length` is kept for the
  resolution path; no query returns it, and the ciphertext beside it already
  shows the length (plus 16) to anyone who can read the database.
- Two puts of one key at the same moment can't both land: the second gets
  "another put … ran at the same time" and stores nothing.
- `keyhole load` writes non-secret rows from a fixture (organizations,
  workspaces, agents, stores, key rows, tools, grants). It refuses accounts,
  sessions, tokens, issuer keys, audit rows and every value table.
- There is no HTTP route for any of this. Writing a value or a grant can't be
  reached from the network.

## How a value is stored

`local_key_values.envelope` = `{alg, nonce, ciphertext, aad, dek {wrapped, kmsKeyRef}}`:

1. A fresh 256-bit data key (DEK) per value encrypts it with AES-256-GCM.
2. The KEK wraps the DEK with AES-256-GCM. Only the wrapped DEK is stored.
3. Both layers authenticate the same additional data: the tag
   `keyhole-local-v1`, then org, key and version, each as a 4-byte
   big-endian length and its bytes (so ids containing any byte can't
   collide). An envelope copied onto another key's row, organization or
   version does not open.
4. `kmsKeyRef` is `file:` plus 16 hex digits of SHA-256 over the KEK. It names
   the KEK without revealing it.

Every failure to open returns the same error, so a failure doesn't say which
check failed.

## The KEK file

`keyhole kek generate` writes 32 random bytes, base64, to a new file
(O_EXCL, mode 0600), and removes the file if the write fails. `LoadKEK`
opens the file first, with `O_NOFOLLOW` (a symlink is refused) and
`O_NONBLOCK` (a FIFO can't hang it), then checks what it opened: a regular
file, no group or other permission bit, owned by the user running Keyhole.
Checking the open file, not the path, means a file swapped in between can't
skip the checks. It reads at most 64 bytes. So `keys put` runs as that user
(`runuser -u keyhole -- keyhole keys put …`).

## What this protects against, and what it doesn't

| Threat | Covered? |
|---|---|
| A database dump or a copy of the database file, without the KEK | **Yes.** Values are ciphertext; the KEK is not in the database. |
| A row's envelope moved to another key, org or version | **Yes.** The AAD binds it; it fails to open. |
| A value leaking through the admin API, the CLI output, logs or audit | **Yes, by test**: a canary over every output of the put/load paths and every table they write. |
| Someone with root, or the `keyhole` user, on the Keyhole host | **No.** They can read the KEK and the database together. |
| **A backup of the whole guest or host (a VM or container image)** | **No.** The backup holds the KEK file and the ciphertext together, so anyone who can read the backup can open every value. |
| A malicious or compromised control plane ("Resolve and sealing", below) | **No.** |

**Because of the whole-guest backup row, the file KEK is for test keys
only** (a budget-capped key you can afford to lose). Before any production key
goes in, the KEK must move to a KMS (OpenBao) or be kept off
the guest, so a guest backup no longer holds both halves.

## Resolve and sealing

On a resolve, the sidecar asks for a value over the tunnel. The Keyhole server
returns it sealed to a fresh X25519 key that the sidecar generates for that
one request. This keeps the value out of the control plane's memory, logs
and traces as it relays the reply, and it needs no registered sidecar key
for v1.

It does **not** protect against a malicious control plane. vbcdx-control
mints the `Vbcdx-Assertion` that names the agent and the sidecar, and rule 2
trusts it, so a control plane that lies can request the value with its own
ephemeral key. A registered per-sidecar key that the control plane can't
substitute is later work.
