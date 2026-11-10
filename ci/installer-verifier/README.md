# The installer's release verifier (vendored)

`src/` is a byte-for-byte copy of these files from `VBCDX/vbcdx` at
`6514b37d063d2d92d859aeacb6cb8be098cd8eaf`:

- `src/service/minisign.js` (`verifyMinisign`)
- `src/errors.js`

`verify.mjs` is the contract test: it checks a packed `@vbcdx/keyhole` tree with the
installer's own minisign verifier, then applies the installer's `verifyRelease` rules
(`src/service/artifact.js` at the same commit) to `@vbcdx/keyhole`: schema 1, the package
name, a semantic version, the trusted comment `vbcdx-release @vbcdx/keyhole <version>`, and
each platform binary's SHA-256. It must also refuse the tree under another key and with one
byte of a binary changed (a replayed trusted comment is pinned in `internal/release`'s Go tests).

`verifyRelease` itself isn't called here: the vendored copy predates the installer's
`keyhole-server` entry. The installer knows the Keyhole server from 0.2.5 on, so a later
update can vendor `artifact.js` and `fs/safe-writer.js` too and call `verifyRelease` directly,
as vbcdx-core's `ci/installer-verifier` does.

`SHA256SUMS` pins every file in this directory, and CI refuses the directory if a file is
missing, changed or unlisted before the contract test runs. CI cannot clone `VBCDX/vbcdx`
(it is private and the job holds no token for it), so the code is vendored rather than fetched.

To update: check out the new commit, copy the two files here, change the commit above, and
regenerate `SHA256SUMS`
(`find . -type f ! -name SHA256SUMS | sed 's|^\./||' | sort | xargs sha256sum > SHA256SUMS`).
