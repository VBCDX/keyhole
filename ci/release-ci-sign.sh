#!/usr/bin/env bash
# Signs a tag's packed npm tree in CI with the DEV release key (the passwordless minisign key the installer pins, held as the Actions secret
# MINISIGN_SECRET_KEY, used only on protected v* tags).
#   ci/release-ci-sign.sh VERSION NPMDIR        (env: MINISIGN_SECRET_KEY, KEYHOLE_RELEASE)
# The key leaves the environment before anything runs and reaches keyhole-release on stdin only;
# keyhole-release is built before this step, so no build ever runs with the key in its environment.
# The installer's own verifier then accepts the tree under release/minisign.pub.
set -euo pipefail

version=${1:?usage: ci/release-ci-sign.sh VERSION NPMDIR}
npm=${2:?usage: ci/release-ci-sign.sh VERSION NPMDIR}
tool=${KEYHOLE_RELEASE:-.release/keyhole-release}
fail() { echo "release-ci-sign: $*" >&2; exit 1; }

# Only a v* tag push signs. The workflow says so too; this holds if a step's `if:` is ever lost.
[ "${GITHUB_EVENT_NAME:-}" = push ] && [[ ${GITHUB_REF:-} == refs/tags/v* ]] \
  || fail "only a v* tag push is signed (event ${GITHUB_EVENT_NAME:-none}, ref ${GITHUB_REF:-none})"
[ -s release/minisign.pub ] || fail "release/minisign.pub is missing"
[ -x "$tool" ] || fail "$tool is not built (build keyhole-release before the key is in the environment)"

key=${MINISIGN_SECRET_KEY:-}
unset MINISIGN_SECRET_KEY
[ -n "$key" ] || fail "the MINISIGN_SECRET_KEY secret is empty"
# A passwordless key: an empty password line, then the key file (keyhole-release sign's stdin contract).
printf '\n%s\n' "$key" | "$tool" sign --public-key release/minisign.pub \
  "$npm/checksums.json" "$npm/@vbcdx/keyhole/checksums.json"
unset key

node ci/installer-verifier/verify.mjs "$npm" "$(sed -n 2p release/minisign.pub)" "$version"
