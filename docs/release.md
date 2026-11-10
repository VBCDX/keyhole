# Releasing Keyhole (npm)

The Keyhole server ships as the npm package `@vbcdx/keyhole`, in the same
signed layout as `@vbcdx/sidecar` (vbcdx-core). The vbcdx service installer
verifies that layout before it copies anything to a host. The package never
runs from `node_modules`: it has no `scripts` and no `bin`.

## What a release is

```text
npm/checksums.json(.minisig)                  what `--from npm` verifies
npm/@vbcdx/keyhole/                           the meta package (the same checksums.json + signature)
npm/@vbcdx/keyhole-linux-x64-gnu/bin/keyhole  one package per platform
npm/@vbcdx/keyhole-linux-x64-musl/bin/keyhole
```

- `keyhole` is one static binary (`CGO_ENABLED=0`, `-trimpath`), so the glibc and
  musl packages carry the same file. npm installs only the one whose `libc`
  matches the host. The names are the installer's `hostPlatform()` ids.
- `checksums.json` is `{"schema":1,"package":"@vbcdx/keyhole","version","files"}`,
  with `files` keyed `@vbcdx/keyhole-<platform>/bin/keyhole` (SHA-256).
- Its minisign signature is prehashed (Ed25519 over BLAKE2b-512). Its trusted
  comment is exactly `vbcdx-release @vbcdx/keyhole <version>`, so a signature
  can't be replayed under another package or version.
- `keyhole --version` prints `keyhole <version>`, stamped at build time with
  `-ldflags -X main.version=<version>`. CI fails a build whose binary prints
  anything else.

## The CI release job (`.forgejo/workflows/ci.yml`, job `release`)

Every run:

1. tests the release scripts: `ci/test-release-tag.sh` and `ci/test-release-ci.sh`;
2. builds the web app and the static binary **cold**, restoring no Go or npm cache, at version `0.0.0-ci.<run>` or, on a `v*` tag, the tag's version. The go and web jobs restore their caches on PR and main runs only, never on a tag run: any PR's run can write those caches, so a PR must never be able to seed what a tag builds, tests or signs. `ci/test-release-ci.sh` checks every job. (The docker and compose jobs build on the runner's shared Docker daemon, whose layer cache this doesn't cover.)
3. checks that the binary is static and that it reports that version;
4. packs the tree (`go run ./cmd/keyhole-release pack VERSION dist npm`);
5. runs the contract test: the installer's own minisign verifier
   (`ci/installer-verifier`, vendored from VBCDX/vbcdx and pinned by
   `SHA256SUMS`) must accept the tree signed with a throwaway key, and must refuse
   it under another key or with one byte of the binary changed;
6. keeps `dist/` and `npm/` as run artifacts.

On a `v*` tag push only:

1. `ci/release-tag.sh` gates the tag. It must be `v<semver>` and annotated, name
   itself, point at the commit, and be on `main`. Once `main` has a
   `release/allowed_signers`, the tag must also be signed by a key in it.
2. `keyhole-release` is built **before** any secret is in the environment.
3. `ci/release-ci-sign.sh` signs with the DEV release key. It reads the
   `MINISIGN_SECRET_KEY` secret (a passwordless minisign key) on stdin only, and
   refuses a key that isn't `release/minisign.pub`'s. The installer's verifier
   then checks the result under that pinned key.
4. `ci/release-ci-publish.sh` publishes the platform packages, then the meta
   package, to the org's Forgejo npm registry (`NPM_PUBLISH_TOKEN`) and, when
   the `NPM_TEST_REGISTRY` variable is set, to the test registry
   (`NPM_TEST_REGISTRY_TOKEN`). It publishes nothing unsigned, and a pre-release
   goes out as `next`, never `latest`. It refuses a public registry, an http URL
   and credentials in the URL.

Each secret appears in exactly one tag-only step, and `ci/test-release-ci.sh`
fails CI if that ever changes. A repository secret can be read by any writer's
workflow run: that is accepted for the DEV key, which is replaced before any
public publish.

## Locally

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=0.1.0-dev.1" -o dist/keyhole ./cmd/keyhole
(cd dist && sha256sum keyhole > SHA256SUMS)
go run ./cmd/keyhole-release pack 0.1.0-dev.1 dist npm
go run ./cmd/keyhole-release contract-sign --public-key-out /tmp/key.pub npm/checksums.json npm/@vbcdx/keyhole/checksums.json
go run ./cmd/keyhole-release verify npm /tmp/key.pub 0.1.0-dev.1
```

Installing on a host is the installer's side: `vbcdx install service keyhole-server`,
documented in the vbcdx installer's `docs/keyhole-server.md` (and summarised in this repo's
README, "Install").
