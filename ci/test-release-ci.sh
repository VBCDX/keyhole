#!/usr/bin/env bash
# Tests for the CI release steps: ci/release-ci-sign.sh and ci/release-ci-publish.sh, run from
# a scratch directory with fake keyhole-release, node and npm that record what they were given; plus a
# check that the workflow uses each secret in one tag-only step and nowhere else.
set -uo pipefail
repo=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
failed=0
ok() { echo "ok    $1"; }
bad() { echo "FAIL  $1"; failed=1; }

bin=$tmp/bin log=$tmp/log
mkdir -p "$bin"
fake() { printf '#!/usr/bin/env bash\n%s\n' "$2" > "$bin/$1"; chmod 0755 "$bin/$1"; }
# keyhole-release sign: records its argv, its stdin and whether the key is in its environment, and
# writes a signature beside each checksums file. XFAIL makes it fail.
fake keyhole-release "echo \"keyhole-release \$*\" >> '$log'
cat > '$tmp/keyhole-release-stdin'
echo \"keyhole-release-env key=\${MINISIGN_SECRET_KEY:+present}\" >> '$log'
[ -e '$tmp/XFAIL' ] && exit 1
shift 3; for f in \"\$@\"; do echo sig > \"\$f.minisig\"; done"
fake node "echo \"node \$*\" >> '$log'"
# npm publish: records its argv and the userconfig it was given, and resolves the registry a scoped
# package goes to the way npm does: `--@vbcdx:registry=` on the command line, then a project .npmrc
# in the working directory, then the userconfig's `@vbcdx:registry`, and only then `--registry`. It
# records that registry and which token variable the userconfig's auth line for it names. A
# registry named in FAIL_REGISTRY fails its publish.
npm_body=$(cat <<'EOF'
echo "npm $*" >> '@LOG@'
args=("$@") uc= cli= reg=
for ((i = 0; i < ${#args[@]}; i++)); do
  case ${args[i]} in
    --userconfig) uc=${args[i+1]};;
    --registry) reg=${args[i+1]};;
    --@vbcdx:registry=*) cli=${args[i]#--@vbcdx:registry=};;
  esac
done
[ -n "$uc" ] && cp "$uc" '@TMP@/npmrc-seen'
scope() { [ -f "$1" ] && sed -n 's/^@vbcdx:registry=//p' "$1" | tail -n 1; }
eff=$cli
[ -n "$eff" ] || eff=$(scope .npmrc)
[ -n "$eff" ] || eff=$(scope "$uc")
[ -n "$eff" ] || eff=$reg
host=${eff#*://}
auth=$(grep -F "//$host:_authToken=" "$uc" | sed -n 's/.*_authToken=\${\(.*\)}$/\1/p' | tail -n 1)
echo "npm-effective $2 $eff auth=${auth:-none}" >> '@LOG@'
echo "npm-env token=${NPM_PUBLISH_TOKEN:+present} test-token=${NPM_TEST_REGISTRY_TOKEN:+present}" >> '@LOG@'
if [ -e '@TMP@/FAIL_REGISTRY' ] && [ "$eff" = "$(cat '@TMP@/FAIL_REGISTRY')" ]; then echo "npm: E500 from $eff" >&2; exit 1; fi
exit 0
EOF
)
npm_body=${npm_body//@LOG@/$log}
fake npm "${npm_body//@TMP@/$tmp}"

KEY=$'untrusted comment: minisign encrypted secret key\nSECRET-KEY-LINE-CANARY'
TOKEN=TOKEN-CANARY-0123456789
work=$tmp/work
setup() {
  rm -rf "$work"
  mkdir -p "$work/release" "$work/npm/@vbcdx/keyhole" "$work/npm/@vbcdx/keyhole-linux-x64-gnu" "$work/npm/@vbcdx/keyhole-linux-x64-musl"
  printf 'untrusted comment: test key\nRWTESTPUBLICKEY\n' > "$work/release/minisign.pub"
  for d in keyhole keyhole-linux-x64-gnu keyhole-linux-x64-musl; do
    printf '{"name":"@vbcdx/%s","version":"%s"}\n' "$d" "${1:-1.2.3}" > "$work/npm/@vbcdx/$d/package.json"
  done
  echo '{}' > "$work/npm/checksums.json"
  echo '{}' > "$work/npm/@vbcdx/keyhole/checksums.json"
  : > "$log"
  rm -f "$tmp/keyhole-release-stdin" "$tmp/npmrc-seen" "$tmp/XFAIL" "$tmp/FAIL_REGISTRY"
}
# run SCRIPT [VAR=value …] -- ARGS…: one run as a v* tag push (override with EVENT/REF), in the
# scratch tree, with only the given extra variables; stderr goes to err.txt.
run() {
  local script=$1 vars=()
  shift
  while [ "$1" != -- ]; do vars+=("$1"); shift; done
  shift
  (cd "$work" && env PATH="$bin:$PATH" KEYHOLE_RELEASE="$bin/keyhole-release" GITHUB_EVENT_NAME="${EVENT:-push}" GITHUB_REF="${REF:-refs/tags/v1.2.3}" \
    GITHUB_SERVER_URL=https://forge.example "${vars[@]}" "$repo/ci/$script" "$@") > "$tmp/out.txt" 2> "$tmp/err.txt"
}
sign() { run release-ci-sign.sh MINISIGN_SECRET_KEY="${SKEY-$KEY}" "$@" -- 1.2.3 npm; }
TTOKEN_DEFAULT=TEST-TOKEN-CANARY-0123456789
# TREG set: also publish to that test registry, with TTOKEN (default TTOKEN_DEFAULT) as its token.
publish() {
  run release-ci-publish.sh NPM_PUBLISH_TOKEN="${PTOKEN-$TOKEN}" \
    ${TREG+"NPM_TEST_REGISTRY=$TREG"} ${TREG+"NPM_TEST_REGISTRY_TOKEN=${TTOKEN-$TTOKEN_DEFAULT}"} -- "${VERSION:-1.2.3}" npm
}
nocanary() { ! grep -rqs -e CANARY "$log" "$tmp/out.txt" "$tmp/err.txt" "$tmp/npmrc-seen"; }

# --- sign ------------------------------------------------------------------------------------
setup
if sign; then ok "a v* tag push signs"; else bad "a v* tag push signs: $(tail -n 1 "$tmp/err.txt")"; fi
grep -qx "keyhole-release sign --public-key release/minisign.pub npm/checksums.json npm/@vbcdx/keyhole/checksums.json" "$log" \
  && ok "keyhole-release signs both checksums copies under release/minisign.pub" || bad "keyhole-release argv: $(grep '^keyhole-release ' "$log")"
[ "$(cat "$tmp/keyhole-release-stdin")" = $'\n'"$KEY" ] && ok "the key reaches keyhole-release on stdin after an empty password line" \
  || bad "keyhole-release stdin: $(od -c "$tmp/keyhole-release-stdin" | head -n 2)"
grep -qx "keyhole-release-env key=" "$log" && ok "the key is not in keyhole-release's environment" || bad "keyhole-release env: $(grep keyhole-release-env "$log")"
grep -qx "node ci/installer-verifier/verify.mjs npm RWTESTPUBLICKEY 1.2.3" "$log" && ok "the installer's verifier checks the result under the pinned key" \
  || bad "verifier: $(grep '^node' "$log")"
[ "$(grep -n -m1 '^keyhole-release ' "$log" | cut -d: -f1)" -lt "$(grep -n -m1 '^node ' "$log" | cut -d: -f1)" ] && ok "verify runs after sign" || bad "order: $(cat "$log")"
nocanary && ok "the key is in no argv, log or output" || bad "the key leaked: $(grep -rl CANARY "$log" "$tmp/out.txt" "$tmp/err.txt")"

refuses_sign() {
  local name=$1 want=$2
  shift 2
  if "$@"; then bad "$name: it signed"; return; fi
  if grep -q '^keyhole-release ' "$log"; then bad "$name: keyhole-release ran"; return; fi
  grep -q -- "$want" "$tmp/err.txt" && ok "sign refuses $name" || bad "sign refuses $name: $(tail -n 1 "$tmp/err.txt")"
}
setup; EVENT=pull_request refuses_sign "a pull request" "only a v\* tag push is signed" sign
setup; REF=refs/heads/main refuses_sign "a push to main" "only a v\* tag push is signed" sign
setup; REF=refs/tags/x1.2.3 refuses_sign "a tag that isn't v*" "only a v\* tag push is signed" sign
setup; SKEY= refuses_sign "an empty key secret" "MINISIGN_SECRET_KEY secret is empty" sign
setup; rm "$work/release/minisign.pub"; refuses_sign "without a pinned public key" "release/minisign.pub is missing" sign
setup; refuses_sign "an unbuilt keyhole-release" "is not built" sign KEYHOLE_RELEASE="$tmp/none"
setup; touch "$tmp/XFAIL"
if sign; then bad "a failed sign: it went on"; else
  grep -q '^node ' "$log" && bad "a failed sign: the verifier ran anyway" || ok "a failed sign stops before the verifier"
fi

# --- publish ---------------------------------------------------------------------------------
setup; sign
if publish; then ok "a signed tag tree is published"; else bad "a signed tag tree is published: $(tail -n 1 "$tmp/err.txt")"; fi
reg=https://forge.example/api/packages/VBCDX/npm/
mapfile -t pubs < <(grep '^npm publish' "$log" | sed 's/ --userconfig [^ ]*//')
[ "${pubs[*]}" = "npm publish npm/@vbcdx/keyhole-linux-x64-gnu/ --registry $reg --@vbcdx:registry=$reg --tag latest npm publish npm/@vbcdx/keyhole-linux-x64-musl/ --registry $reg --@vbcdx:registry=$reg --tag latest npm publish npm/@vbcdx/keyhole/ --registry $reg --@vbcdx:registry=$reg --tag latest" ] \
  && ok "the platform packages, then the meta package, to the org registry, as latest" || bad "publish calls: ${pubs[*]}"
# Where npm actually sends each one, and with which token (the fake resolves it as npm does).
effective() { grep '^npm-effective ' "$log" | cut -d' ' -f3-; }
want=()
for d in keyhole-linux-x64-gnu keyhole-linux-x64-musl keyhole; do want+=("$reg auth=NPM_PUBLISH_TOKEN"); done
mapfile -t eff < <(effective)
[ "$(printf '%s|' "${eff[@]}")" = "$(printf '%s|' "${want[@]}")" ] && ok "npm resolves all three to Forgejo, with Forgejo's token" || bad "effective: $(printf '%s|' "${eff[@]}")"
[ "$(grep -c '^npm-env token=present test-token=$' "$log")" = 3 ] && ok "npm has the token in its environment" || bad "npm env: $(grep npm-env "$log")"
grep -qxF '@vbcdx:registry=https://forge.example/api/packages/VBCDX/npm/' "$tmp/npmrc-seen" \
  && grep -qxF '//forge.example/api/packages/VBCDX/npm/:_authToken=${NPM_PUBLISH_TOKEN}' "$tmp/npmrc-seen" \
  && ok "the userconfig names the token's variable, not its value" || bad "npmrc: $(cat "$tmp/npmrc-seen")"
nocanary && ok "the token is in no argv, file, log or output" || bad "the token leaked"
conf=$(grep -m1 -o -- '--userconfig [^ ]*' "$log" | cut -d' ' -f2)
[ ! -e "$conf" ] && ok "the userconfig is removed afterwards" || bad "the userconfig is left at $conf"
grep -q '^release-ci-publish: NPM_TEST_REGISTRY is not set, so the test registry gets nothing$' "$tmp/out.txt" \
  && ok "with no test registry set, it says so and publishes to Forgejo only" || bad "no test registry: $(cat "$tmp/out.txt")"

# The test registry: the same packages, after Forgejo's, with its own token's variable.
treg=https://npm.test.example/
setup; sign; TREG=$treg publish && ok "a tag tree is published to both registries" || bad "both registries: $(tail -n 1 "$tmp/err.txt")"
want=()
for r in "$reg auth=NPM_PUBLISH_TOKEN" "$treg auth=NPM_TEST_REGISTRY_TOKEN"; do for d in 1 2 3; do want+=("$r"); done; done
mapfile -t eff < <(effective)
[ "$(printf '%s|' "${eff[@]}")" = "$(printf '%s|' "${want[@]}")" ] \
  && ok "npm sends three to Forgejo with its token, then three to the test registry with the test token" || bad "effective: $(printf '%s|' "${eff[@]}")"
mapfile -t dirs < <(grep '^npm-effective ' "$log" | cut -d' ' -f2)
[ "${dirs[*]}" = "npm/@vbcdx/keyhole-linux-x64-gnu/ npm/@vbcdx/keyhole-linux-x64-musl/ npm/@vbcdx/keyhole/ npm/@vbcdx/keyhole-linux-x64-gnu/ npm/@vbcdx/keyhole-linux-x64-musl/ npm/@vbcdx/keyhole/" ] \
  && ok "each registry gets the platform packages, then the meta package" || bad "order: ${dirs[*]}"
grep -q NPM_PUBLISH_TOKEN "$tmp/npmrc-seen" && bad "the test registry's userconfig holds the Forgejo token's line" \
  || ok "the test registry's userconfig holds only its own token's line"
[ "$(grep -c '^npm-env token=present test-token=present$' "$log")" = 6 ] && ok "npm has both tokens in its environment" || bad "npm env: $(grep npm-env "$log")"
grep -qxF '//npm.test.example/:_authToken=${NPM_TEST_REGISTRY_TOKEN}' "$tmp/npmrc-seen" \
  && ok "the userconfig names the test token's variable, not its value" || bad "npmrc: $(cat "$tmp/npmrc-seen")"
nocanary && ok "neither token is in any argv, file, log or output" || bad "a token leaked"
grep -q 'npm.test.example' "$tmp/out.txt" && grep -q 'published to npm.test.example$' "$tmp/out.txt" \
  && ok "the log names the host only" || bad "log: $(cat "$tmp/out.txt")"

# A project .npmrc in the working directory outranks --userconfig: the command-line scope still wins.
setup; sign; printf '@vbcdx:registry=https://wrong.example/\n' > "$work/.npmrc"; TREG=$treg publish
grep -q 'wrong.example' "$log" && bad "a project .npmrc redirected a publish: $(effective | sort -u)" \
  || ok "a project .npmrc can't redirect either registry's publish"
rm -f "$work/.npmrc"

# The test registry failing after Forgejo succeeded: Forgejo is complete, the job is red.
setup; sign; printf '%s' "$treg" > "$tmp/FAIL_REGISTRY"
if TREG=$treg publish; then bad "a failing test registry: the job passed"
else
  [ "$(effective | grep -c "^$reg auth=NPM_PUBLISH_TOKEN$")" = 3 ] && [ "$(effective | grep -c "^$treg ")" = 1 ] \
    && ok "a failing test registry: Forgejo has all three, the job fails at the first test-registry publish" || bad "after a test-registry failure: $(effective | tr '\n' '|')"
fi

setup 1.3.0-rc.1; sign; VERSION=1.3.0-rc.1 publish
[ "$(grep -c -- '--tag next' "$log")" = 3 ] && ok "a pre-release is published as next, never latest" || bad "pre-release tag: $(grep '^npm publish' "$log")"

refuses_publish() {
  local name=$1 want=$2
  shift 2
  if "$@"; then bad "$name: it published"; return; fi
  if grep -q '^npm ' "$log"; then bad "$name: npm ran"; return; fi
  grep -q -- "$want" "$tmp/err.txt" && ok "publish refuses $name" || bad "publish refuses $name: $(tail -n 1 "$tmp/err.txt")"
}
setup; sign; : > "$log"; EVENT=pull_request refuses_publish "a pull request" "only a v\* tag push is published" publish
setup; sign; : > "$log"; REF=refs/heads/main refuses_publish "a push to main" "only a v\* tag push is published" publish
setup; sign; : > "$log"; PTOKEN= refuses_publish "an empty token secret" "NPM_PUBLISH_TOKEN secret is empty" publish
setup; refuses_publish "an unsigned tree" "the tree is not signed" publish
setup; sign; : > "$log"; TREG=$treg TTOKEN= refuses_publish "a test registry with an empty token" "NPM_TEST_REGISTRY_TOKEN secret is empty" publish
setup; sign; : > "$log"; TREG=http://npm.test.example/ refuses_publish "a cleartext test registry" "must be an https URL ending in /" publish
setup; sign; : > "$log"; TREG=https://npm.test.example refuses_publish "a test registry URL with no trailing /" "must be an https URL ending in /" publish
setup; sign; : > "$log"; TREG=$reg refuses_publish "a test registry that is the Forgejo registry" "is the Forgejo registry" publish
setup; sign; : > "$log"; TREG=https://u:URL-PW-CANARY@npm.test.example/ refuses_publish "credentials in the test registry URL" "must not carry credentials" publish
grep -q URL-PW-CANARY "$tmp/err.txt" "$tmp/out.txt" "$log" && bad "the refused URL's credentials reached the output" || ok "a refused URL is not printed"
setup; sign; : > "$log"; TREG=https://registry.npmjs.org/ refuses_publish "npmjs as the test registry" "names a public registry" publish
setup; sign; : > "$log"; TREG=https://npm.pkg.github.com/ refuses_publish "GitHub's registry as the test registry" "names a public registry" publish
setup; sign; : > "$log"; VERSION=9.9.9 refuses_publish "a package of another version" "is not version 9.9.9" publish
setup; sign; : > "$log"; rm -r "$work/npm/@vbcdx/keyhole-linux-x64-gnu" "$work/npm/@vbcdx/keyhole-linux-x64-musl"
refuses_publish "a tree with no platform package" "has no package.json" publish

# --- the workflow ----------------------------------------------------------------------------
# Each secret is used in exactly one step, and that step runs on v* tag pushes only; keyhole-release
# is built in a step of its own before the key's step, and the token's step comes after signing.
python3 - "$repo/.forgejo/workflows/ci.yml" <<'PY' && ok "each secret is in one v*-tag-only step; build, sign, publish in that order" || bad "the workflow's secret steps"
import re, sys
text = open(sys.argv[1]).read()
steps = re.split(r"\n(?=      - )", text)
tag_if = "if: github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')"
names = ("MINISIGN_SECRET_KEY", "NPM_PUBLISH_TOKEN", "NPM_TEST_REGISTRY_TOKEN")
uses = {name: [i for i, s in enumerate(steps) if f"secrets.{name}" in s] for name in names}
all_secret_refs = re.findall(r"secrets\.(\w+)", text)
assert sorted(set(all_secret_refs)) == sorted(names), all_secret_refs
assert all(len(v) == 1 for v in uses.values()), uses
sign, pub = uses["MINISIGN_SECRET_KEY"][0], uses["NPM_PUBLISH_TOKEN"][0]
# The test registry's token rides in the same publish step, never another.
assert uses["NPM_TEST_REGISTRY_TOKEN"] == [pub], uses
assert tag_if in steps[sign] and "ci/release-ci-sign.sh" in steps[sign], steps[sign]
assert tag_if in steps[pub] and "ci/release-ci-publish.sh" in steps[pub], steps[pub]
build = [i for i, s in enumerate(steps) if "go build -o .release/keyhole-release ./cmd/keyhole-release" in s and tag_if in s]
assert build and build[0] < sign < pub, (build, sign, pub)
assert "secrets." not in steps[build[0]], steps[build[0]]
PY

# No job a v* tag run reaches restores a cache a PR run could have written. The workflow triggers on v* tags, and no job has an event `if:`, so that is
# every job. setup-go must say `cache: false`, or (go/web only) the exact tag-off expression;
# setup-node names no cache, or the exact tag-off expression; the release job is cold on every
# run; a step has at most one `cache:` key; no actions/cache step; no run step touches a
# cache directory or cache variable.
python3 - "$repo/.forgejo/workflows/ci.yml" <<'PY' && ok "no job a v* run reaches restores a PR-writable cache" || bad "a job a v* run reaches restores a cache"
import re, sys
text = open(sys.argv[1]).read()
GO_OFF = "${{ !startsWith(github.ref, 'refs/tags/') }}"
NODE_OFF = "${{ !startsWith(github.ref, 'refs/tags/') && 'npm' || '' }}"
body = text[text.index("\njobs:\n"):]
jobs = re.findall(r"\n  ([A-Za-z0-9_-]+):\n(.*?)(?=\n  [A-Za-z0-9_-]+:\n|\Z)", body, re.S)
names = [n for n, _ in jobs]
assert {"go", "web", "release"} <= set(names), names
for name, job in jobs:
    # An event filter would change which jobs a tag run reaches: then this check must change too.
    assert not re.search(r"^    if:", job, re.M), f"{name}: a job-level if: (update this check)"
    for step in re.split(r"\n(?=      - )", job):
        caches = re.findall(r"^          cache: *(.*?) *$", step, re.M)
        assert len(caches) <= 1, f"{name}: a step with {len(caches)} cache: keys"
        cache = caches[0] if caches else None
        if re.search(r"uses: \S*/setup-go@", step):
            ok = cache == "false" or (name != "release" and cache == GO_OFF)
            assert ok, f"{name}: setup-go cache: {cache!r}"
        elif re.search(r"uses: \S*/setup-node@", step):
            ok = cache is None or (name != "release" and cache == NODE_OFF)
            assert ok, f"{name}: setup-node cache: {cache!r}"
        else:
            assert cache is None, f"{name}: a cache: key on another step"
        assert not re.search(r"uses: \S*/cache(@|/)", step), f"{name}: an actions/cache step"
        assert not re.search(r"\.cache\b|go/pkg/mod|/\.npm\b|\bGOCACHE\b|\bGOMODCACHE\b|npm_config_cache|--cache\b", step), \
            f"{name}: a step touches a cache directory or variable"
PY

exit "$failed"
