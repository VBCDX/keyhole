#!/usr/bin/env bash
# Table test for ci/release-tag.sh, run by CI on every event: a scratch origin, real annotated
# tags and throwaway SSH signing keys (made here, deleted on exit). The gate must accept the
# one good tag (given the commit or the tag object) and refuse every other case with its own
# message, so removing any of its checks fails CI. The signers file is main's: cases that check
# out an older commit (as CI checks out the tag) prove the checkout's copy never counts.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
for tool in git ssh-keygen; do
  command -v "$tool" > /dev/null || { echo "test-release-tag: $tool is required" >&2; exit 1; }
done

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# The runner's own git config (signing keys, rewrites) must not take part.
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

ssh-keygen -q -t ed25519 -N "" -C signer -f "$tmp/signer"
ssh-keygen -q -t ed25519 -N "" -C other -f "$tmp/other"
git init -q --bare "$tmp/origin.git"
git init -q -b main "$tmp/work"
cd "$tmp/work"
git config user.name test
git config user.email test@example.invalid
mkdir -p ci release
cp "$here/release-tag.sh" ci/release-tag.sh
# Older main commits, before the first signer: no file, a symlink, a directory at the path.
git add -A
git commit -q -m "before any signer"
early=$(git rev-parse HEAD)
ln -s /dev/null release/allowed_signers
git add -A
git commit -q -m "a symlink at the path"
linked=$(git rev-parse HEAD)
git rm -q release/allowed_signers
mkdir -p release/allowed_signers
echo x > release/allowed_signers/x
git add -A
git commit -q -m "a directory at the path"
dir=$(git rev-parse HEAD)
git rm -rq release/allowed_signers
mkdir -p release
echo "signer@example.invalid $(cat "$tmp/signer.pub")" > release/allowed_signers
git add -A
git commit -q -m main
git remote add origin "$tmp/origin.git"
git push -q origin main

signed() { git -c gpg.format=ssh -c user.signingkey="$1" tag -s -m "$2" "$2" "$3"; git push -q origin "refs/tags/$2"; }
signed "$tmp/signer" v1.2.3 main
git tag -a -m unsigned v1.2.4 main && git push -q origin refs/tags/v1.2.4
git tag v1.2.5 main && git push -q origin refs/tags/v1.2.5
signed "$tmp/other" v1.2.6 main
git checkout -q -b side
git commit -q --allow-empty -m side
signed "$tmp/signer" v1.2.7 side
git checkout -q main
signed "$tmp/signer" v1.2 main
signed "$tmp/signer" 1.2.8 main
# A validly signed v1.0.0, pushed a second time under another name.
signed "$tmp/signer" v1.0.0 main
git push -q origin refs/tags/v1.0.0:refs/tags/v9.9.9
git tag -a -m unsigned v2.0.0 "$early" && git push -q origin refs/tags/v2.0.0
git tag -a -m unsigned v2.0.1 "$linked" && git push -q origin refs/tags/v2.0.1
git tag -a -m unsigned v2.0.2 "$dir" && git push -q origin refs/tags/v2.0.2
signed "$tmp/signer" v2.0.3 "$early"
main=$(git rev-parse main)
side=$(git rev-parse side)
tag_object=$(git rev-parse refs/tags/v1.2.3)

failed=0
check() {
  local name=$1 want=$2 got out
  shift 2
  if out=$(ci/release-tag.sh "$@" 2>&1); then
    got="accepted $(tail -n 1 <<< "$out")"
  else
    got="refused: $(grep '^release-tag:' <<< "$out" | tail -n 1)"
  fi
  if [ "$got" = "$want" ]; then
    echo "ok    $name"
  else
    echo "FAIL  $name"
    echo "        want: $want"
    echo "        got:  $got"
    failed=1
  fi
}

check "signed by a release signer, on main" "accepted 1.2.3" v1.2.3 "$main"
check "the same, given the tag object" "accepted 1.2.3" v1.2.3 "$tag_object"
check "annotated but unsigned" "refused: release-tag: v1.2.4 is not signed by a key in main's release/allowed_signers" v1.2.4 "$main"
check "lightweight" "refused: release-tag: v1.2.5 is not an annotated tag" v1.2.5 "$main"
check "signed by another key" "refused: release-tag: v1.2.6 is not signed by a key in main's release/allowed_signers" v1.2.6 "$main"
check "signed, its commit not on main" "refused: release-tag: $side is not on main" v1.2.7 "$side"
check "points elsewhere than the pushed commit" "refused: release-tag: v1.2.3 does not point at $side" v1.2.3 "$side"
check "not a commit" "refused: release-tag: 0000000 is not a commit" v1.2.3 0000000
check "not a semantic version" "refused: release-tag: 1.2 is not a semantic version" v1.2 "$main"
check "no v prefix" "refused: release-tag: 1.2.8 is not v<version>" 1.2.8 "$main"
check "a signed tag replayed under another name" "refused: release-tag: v9.9.9 is a tag object named something else (a replayed tag)" v9.9.9 "$main"
# Run from a checkout of the tagged commit, as CI does: main's file decides, not the checkout's.
at() { local ref=$1; shift; git checkout -q --detach "$ref"; check "$@"; git checkout -q main; }
at "$early" "main lists a signer: unsigned, on an older commit with no file" "refused: release-tag: v2.0.0 is not signed by a key in main's release/allowed_signers" v2.0.0 "$early"
at "$linked" "main lists a signer: unsigned, on an older commit with a symlink at the path" "refused: release-tag: v2.0.1 is not signed by a key in main's release/allowed_signers" v2.0.1 "$linked"
at "$dir" "main lists a signer: unsigned, on an older commit with a directory at the path" "refused: release-tag: v2.0.2 is not signed by a key in main's release/allowed_signers" v2.0.2 "$dir"
at "$early" "main lists a signer: signed, on an older commit with no file" "accepted 2.0.3" v2.0.3 "$early"

# main's own file changes from here on: each mode is a new main commit, so the tags above stay on main.
main_signers() {
  git rm -rq --ignore-unmatch release/allowed_signers
  mkdir -p release
  case $1 in
    missing) ;;
    empty) : > release/allowed_signers ;;
    blank) printf '  \n\t\n\n' > release/allowed_signers ;;
    comment) echo "# no signer yet" > release/allowed_signers ;;
    symlink) ln -s /dev/null release/allowed_signers ;;
    directory) mkdir release/allowed_signers && echo x > release/allowed_signers/x ;;
  esac
  git add -A
  git commit -q --allow-empty -m "main: allowed_signers $1"
  git push -q origin main
}
# Anything in main's file requires a signature, even with no valid signer line.
for mode in blank comment symlink; do
  main_signers "$mode"
  check "main's allowed_signers is $mode: still requires a signature" "refused: release-tag: v1.2.4 is not signed by a key in main's release/allowed_signers" v1.2.4 "$main"
done
main_signers directory
check "main's allowed_signers is a directory: refused" "refused: release-tag: main's release/allowed_signers is not a file" v1.2.4 "$main"
# No signer set up on main (the file missing, then empty): the signature is not checked, and every
# other check still is.
for mode in missing empty; do
  main_signers "$mode"
  check "$mode allowed_signers: an unsigned annotated tag on main is accepted" "accepted 1.2.4" v1.2.4 "$main"
  out=$(ci/release-tag.sh v1.2.4 "$main" 2>&1)
  if grep -qx "release-tag: main's release/allowed_signers is missing or empty, so v1.2.4's signature is not checked (development release)" <<< "$out"; then
    echo "ok    $mode allowed_signers: it says the signature is not checked"
  else
    echo "FAIL  $mode allowed_signers: it says the signature is not checked"; failed=1
  fi
  check "$mode allowed_signers: a tag signed by any key is accepted" "accepted 1.2.6" v1.2.6 "$main"
  check "$mode allowed_signers: still refuses a lightweight tag" "refused: release-tag: v1.2.5 is not an annotated tag" v1.2.5 "$main"
  check "$mode allowed_signers: still refuses a commit not on main" "refused: release-tag: $side is not on main" v1.2.7 "$side"
  check "$mode allowed_signers: still refuses a tag pointing elsewhere" "refused: release-tag: v1.2.3 does not point at $side" v1.2.3 "$side"
  check "$mode allowed_signers: still refuses a replayed tag" "refused: release-tag: v9.9.9 is a tag object named something else (a replayed tag)" v9.9.9 "$main"
  check "$mode allowed_signers: still refuses a non-semantic version" "refused: release-tag: 1.2 is not a semantic version" v1.2 "$main"
  check "$mode allowed_signers: still refuses no v prefix" "refused: release-tag: 1.2.8 is not v<version>" 1.2.8 "$main"
done

[ "$failed" = 0 ] && echo "test-release-tag: all cases as expected"
exit "$failed"
