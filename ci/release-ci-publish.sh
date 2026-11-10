#!/usr/bin/env bash
# Publishes a tag's signed npm tree to the org's Forgejo npm registry and,
# when NPM_TEST_REGISTRY names one, to the test registry users install from:
# the platform packages first, then the @vbcdx/keyhole meta package that depends on them.
#   ci/release-ci-publish.sh VERSION NPMDIR
#     env: NPM_PUBLISH_TOKEN, GITHUB_SERVER_URL; optional NPM_TEST_REGISTRY + NPM_TEST_REGISTRY_TOKEN
# The tokens are never written down: npm's config names the variables.
set -euo pipefail

version=${1:?usage: ci/release-ci-publish.sh VERSION NPMDIR}
npm=${2:?usage: ci/release-ci-publish.sh VERSION NPMDIR}
fail() { echo "release-ci-publish: $*" >&2; exit 1; }

[ "${GITHUB_EVENT_NAME:-}" = push ] && [[ ${GITHUB_REF:-} == refs/tags/v* ]] \
  || fail "only a v* tag push is published (event ${GITHUB_EVENT_NAME:-none}, ref ${GITHUB_REF:-none})"
[ -n "${NPM_PUBLISH_TOKEN:-}" ] || fail "the NPM_PUBLISH_TOKEN secret is empty"
[ -s "$npm/@vbcdx/keyhole/checksums.json.minisig" ] || fail "the tree is not signed; nothing is published unsigned"
server=${GITHUB_SERVER_URL:?GITHUB_SERVER_URL is not set}
registry="$server/api/packages/VBCDX/npm/"
# The test registry is checked before anything is published, so a bad setting publishes nothing.
# Its value is never printed: a URL can carry credentials.
test_registry=${NPM_TEST_REGISTRY:-}
if [ -n "$test_registry" ]; then
  [[ $test_registry == https://*/ ]] || fail "NPM_TEST_REGISTRY must be an https URL ending in /"
  authority=${test_registry#https://}; authority=${authority%%/*}
  [[ $authority != *@* ]] || fail "NPM_TEST_REGISTRY must not carry credentials in the URL"
  [ "$test_registry" != "$registry" ] || fail "NPM_TEST_REGISTRY is the Forgejo registry; it must be a different one"
  # A public registry is a release decision, never a test registry.
  case ${authority%%:*} in
    registry.npmjs.org|npm.pkg.github.com) fail "NPM_TEST_REGISTRY names a public registry; that is a release decision, not this variable";;
  esac
  [ -n "${NPM_TEST_REGISTRY_TOKEN:-}" ] || fail "NPM_TEST_REGISTRY is set but the NPM_TEST_REGISTRY_TOKEN secret is empty"
fi

conf=$(mktemp -d)
trap 'rm -rf "$conf"' EXIT
chmod 0700 "$conf"

# A pre-release must not become `latest`.
tag=latest
[[ $version == *-* ]] && tag=next

# A missing platform directory leaves the glob unexpanded: its package.json check fails.
dirs=("$npm"/@vbcdx/keyhole-*/ "$npm/@vbcdx/keyhole/")
for dir in "${dirs[@]}"; do
  [ -f "$dir/package.json" ] || fail "$dir has no package.json"
  [ "$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$dir/package.json")" = "$version" ] \
    || fail "$dir is not version $version"
done

# publish_to URL TOKENVAR: npm takes a scoped package's registry from `@scope:registry` before
# `--registry`, and a project .npmrc in the working directory outranks --userconfig. So the scope is
# also given on the command line, which outranks every config file. The userconfig is per registry
# and holds only that registry's token; npm expands ${TOKENVAR} from the environment.
publish_to() {
  local to=$1 var=$2 published=0 dir rc
  rc="$conf/npmrc-$var"
  {
    printf '@vbcdx:registry=%s\n' "$to"
    printf '//%s:_authToken=${%s}\n' "${to#*://}" "$var"
  } > "$rc"
  for dir in "${dirs[@]}"; do
    npm publish "$dir" --registry "$to" "--@vbcdx:registry=$to" --userconfig "$rc" --tag "$tag"
    published=$((published + 1))
  done
  local host=${to#*://}
  echo "release-ci-publish: $published packages of $version published to ${host%%/*}"
}
publish_to "$registry" NPM_PUBLISH_TOKEN
if [ -n "$test_registry" ]; then
  publish_to "$test_registry" NPM_TEST_REGISTRY_TOKEN
else
  echo "release-ci-publish: NPM_TEST_REGISTRY is not set, so the test registry gets nothing"
fi
