#!/usr/bin/env bash
# The gate in front of the release signature: run by the release job for a `v*` tag push only.
#   ci/release-tag.sh TAG COMMIT     prints the release version (TAG without its `v`)
# Refuses unless TAG is v<semver>, is an annotated tag, names itself TAG, points at COMMIT,
# and COMMIT is on main; and, once main's release/allowed_signers lists a signer, is signed by
# a key in it (git's SSH signing). While main's file is missing or empty the signature is not checked; every other check still is. The file is always
# read from origin/main, never from the checkout: CI runs this from the tagged tree, which may be
# an older main commit from before the first signer.
# COMMIT may be the commit or the tag object (it is peeled), so the check holds whichever
# the CI passes for a tag push. Run by CI for a `v*` tag push. Tested by ci/test-release-tag.sh.
set -euo pipefail

tag=${1:?usage: ci/release-tag.sh TAG COMMIT}
commit=${2:?usage: ci/release-tag.sh TAG COMMIT}
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

fail() { echo "release-tag: $*" >&2; exit 1; }

version=${tag#v}
[ "$tag" = "v$version" ] || fail "$tag is not v<version>"
# The same rule as the installer's and `keyhole-release pack`'s (which checks it again).
id='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
semver="^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-$id(\.$id)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$"
[[ $version =~ $semver ]] || fail "$version is not a semantic version"

path=release/allowed_signers
signers=$(mktemp)
trap 'rm -f -- "$signers"' EXIT

git fetch --quiet --no-tags origin "+refs/heads/main:refs/remotes/origin/main" "+refs/tags/$tag:refs/tags/$tag"
[ "$(git cat-file -t "refs/tags/$tag")" = tag ] || fail "$tag is not an annotated tag"
# The tag object names itself: a signed v1.0.0 pushed as refs/tags/v9.9.9 is a replay.
[ "$(git cat-file tag "refs/tags/$tag" | sed -n 's/^tag //p' | head -n 1)" = "$tag" ] \
  || fail "$tag is a tag object named something else (a replayed tag)"
commit=$(git rev-parse --verify --quiet "$commit^{commit}") || fail "$2 is not a commit"
[ "$(git rev-parse "refs/tags/$tag^{commit}")" = "$commit" ] || fail "$tag does not point at $commit"
git merge-base --is-ancestor "$commit" refs/remotes/origin/main || fail "$commit is not on main"
# main's copy decides (a symlink there is its target's name, so non-empty). Any content, even
# lines that are not a valid signer, requires a valid signature.
if [ -n "$(git ls-tree refs/remotes/origin/main -- "$path")" ]; then
  git cat-file blob "refs/remotes/origin/main:$path" > "$signers" || fail "main's $path is not a file"
fi
if [ -s "$signers" ]; then
  git -c gpg.format=ssh -c gpg.ssh.allowedSignersFile="$signers" verify-tag "refs/tags/$tag" >&2 \
    || fail "$tag is not signed by a key in main's $path"
else
  echo "release-tag: main's $path is missing or empty, so $tag's signature is not checked (development release)" >&2
fi

echo "$version"
