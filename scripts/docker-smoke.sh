#!/usr/bin/env bash
# Smoke test for a Keyhole started with Docker: the from-scratch
# `docker compose up` stack, or the single container on SQLite.
#
#   scripts/docker-smoke.sh compose              # after `docker compose up -d --build`
#   scripts/docker-smoke.sh container <name>     # after `docker run -d --name <name> ...`
#
# It waits until the containers are healthy, then probes Keyhole from a
# throwaway curl container on Keyhole's own Docker network, so it needs no
# published port and works against a remote daemon (DOCKER_HOST) too:
#
#   - GET /healthz and /readyz answer 200 (readyz: database ok);
#   - GET / serves the sign-in page (the web app's HTML and its script);
#   - /api/auth/config offers local accounts and no Zitadel;
#   - the Zitadel entry point does nothing (no OIDC provider is configured);
#   - the bootstrap admin signs in, /api/me knows them, a wrong password is 401;
#   - the server log names the expected database (postgres or sqlite).
#
# The bootstrap admin is read from BOOTSTRAP_ADMIN_EMAIL and
# BOOTSTRAP_ADMIN_PASSWORD, or, when unset, from ./.env. Exit status is the
# number of failed checks (0 = all passed). Every network call is bounded.
set -euo pipefail

# curlimages/curl:8.16.0, pinned by digest like every other image here.
CURL_IMAGE=${SMOKE_CURL_IMAGE:-curlimages/curl:8.16.0@sha256:463eaf6072688fe96ac64fa623fe73e1dbe25d8ad6c34404a669ad3ce1f104b6}
WAIT_SECONDS=${SMOKE_WAIT_SECONDS:-240}
ORIGIN=${PUBLIC_URL:-http://localhost:8080}

usage() { echo "usage: $0 compose | container <name>" >&2; exit 2; }

mode=${1:-}
case "$mode" in
  compose)
    want_dialect=postgres
    app=$(docker compose ps -aq keyhole)
    db=$(docker compose ps -aq postgres)
    [ -n "$app" ] && [ -n "$db" ] || { echo "FAIL: docker compose has no keyhole and postgres containers; run docker compose up first" >&2; exit 1; }
    wait_for=("$db" "$app")
    host=keyhole
    ;;
  container)
    [ -n "${2:-}" ] || usage
    want_dialect=sqlite
    app=$(docker inspect -f '{{.Id}}' "$2")
    wait_for=("$app")
    host=$2
    ;;
  *) usage ;;
esac

env_value() { # the value of $1 in ./.env, if any
  [ -f .env ] || return 0
  sed -n "s/^$1=//p" .env | tail -n 1
}
email=${BOOTSTRAP_ADMIN_EMAIL:-$(env_value BOOTSTRAP_ADMIN_EMAIL)}
password=${BOOTSTRAP_ADMIN_PASSWORD:-$(env_value BOOTSTRAP_ADMIN_PASSWORD)}
[ -n "$email" ] && [ -n "$password" ] || { echo "FAIL: set BOOTSTRAP_ADMIN_EMAIL and BOOTSTRAP_ADMIN_PASSWORD (or put them in .env)" >&2; exit 1; }

logs() { docker logs "$app" 2>&1; }

# 1. Wait for health. A container that exits or restarts is a failure now,
# not after the timeout.
for c in "${wait_for[@]}"; do
  name=$(docker inspect -f '{{.Name}}' "$c")
  deadline=$((SECONDS + WAIT_SECONDS))
  while :; do
    state=$(docker inspect -f '{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}} {{.RestartCount}}' "$c")
    read -r status health restarts <<<"$state"
    if [ "$health" = healthy ]; then echo "PASS: $name is healthy"; break; fi
    if [ "$status" = exited ] || [ "$status" = dead ] || [ "$restarts" != 0 ]; then
      echo "FAIL: $name stopped before it was healthy (status $status, restarts $restarts)"
      docker logs "$c" 2>&1 | tail -n 50
      exit 1
    fi
    if [ "$health" = none ]; then echo "FAIL: $name has no health check"; exit 1; fi
    if [ $SECONDS -ge $deadline ]; then
      echo "FAIL: $name not healthy after ${WAIT_SECONDS}s (status $status, health $health)"
      docker logs "$c" 2>&1 | tail -n 50
      exit 1
    fi
    sleep 2
  done
done

net=$(docker inspect -f '{{range $k, $v := .NetworkSettings.Networks}}{{$k}}{{"\n"}}{{end}}' "$app" | head -n 1)
[ -n "$net" ] || { echo "FAIL: $host is on no Docker network" >&2; exit 1; }
echo "Probing http://$host:8080 from network $net"

# 2. The probes, all in one curl container so the session cookie carries over.
# The credentials go in through the environment, not the command line.
probe=$(cat <<'SH'
set -u
B="http://$HOST:8080"
fails=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*"; fails=$((fails + 1)); }
req() { # req <outfile> <curl args...>; prints the status code
  out=$1; shift
  curl -sS --max-time 20 -o "$out" -w '%{http_code}' "$@" || echo 000
}

code=$(req /tmp/b "$B/healthz")
[ "$code" = 200 ] && grep -qx ok /tmp/b && pass "/healthz 200" || fail "/healthz $code: $(head -c 200 /tmp/b)"

code=$(req /tmp/b "$B/readyz")
{ [ "$code" = 200 ] && grep -q '"database":"ok"' /tmp/b; } \
  && pass "/readyz 200 $(cat /tmp/b)" || fail "/readyz $code: $(head -c 200 /tmp/b)"

code=$(req /tmp/page "$B/")
if [ "$code" = 200 ] && grep -q '<div id="root">' /tmp/page; then
  pass "sign-in page (/) 200 with the web app mount point"
  js=$(grep -o 'src="/assets/[^"]*\.js"' /tmp/page | head -n 1 | sed 's/^src="//; s/"$//')
  if [ -n "$js" ]; then
    code=$(curl -sS --max-time 20 -o /dev/null -w '%{http_code} %{content_type}' "$B$js" || echo 000)
    case "$code" in
      "200 text/javascript"*|"200 application/javascript"*) pass "web app script $js: $code" ;;
      *) fail "web app script $js: $code" ;;
    esac
  else
    fail "sign-in page references no /assets/*.js script"
  fi
else
  fail "sign-in page (/) $code: $(head -c 200 /tmp/page)"
fi

code=$(req /tmp/b "$B/api/auth/config")
{ [ "$code" = 200 ] && grep -q '"local":true' /tmp/b && grep -q '"zitadel":false' /tmp/b; } \
  && pass "/api/auth/config: local accounts on, Zitadel off" || fail "/api/auth/config $code: $(head -c 300 /tmp/b)"

loc=$(curl -sS --max-time 20 -o /dev/null -w '%{http_code} %{redirect_url}' "$B/auth/oidc/login" || echo 000)
case "$loc" in
  "302 "*"/?error=zitadel_not_enabled") pass "/auth/oidc/login is inert (302 to zitadel_not_enabled)" ;;
  *) fail "/auth/oidc/login: $loc" ;;
esac

json_escape() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'; }
body=$(printf '{"email":"%s","password":"%s"}' "$(json_escape "$EMAIL")" "$(json_escape "$PASSWORD")")
code=$(printf '%s' "$body" | req /tmp/b -c /tmp/jar -H 'Content-Type: application/json' -H "Origin: $ORIGIN" --data-binary @- "$B/api/auth/login")
[ "$code" = 200 ] && pass "bootstrap admin signs in (POST /api/auth/login 200)" || fail "bootstrap admin sign-in $code: $(head -c 300 /tmp/b)"

code=$(req /tmp/b -b /tmp/jar "$B/api/me")
{ [ "$code" = 200 ] && grep -qF "\"email\":\"$EMAIL\"" /tmp/b && grep -q '"method":"local"' /tmp/b; } \
  && pass "/api/me 200 as the bootstrap admin (method local)" || fail "/api/me $code: $(head -c 300 /tmp/b)"

body=$(printf '{"email":"%s","password":"%s"}' "$(json_escape "$EMAIL")" "not-the-password")
code=$(printf '%s' "$body" | req /tmp/b -H 'Content-Type: application/json' -H "Origin: $ORIGIN" --data-binary @- "$B/api/auth/login")
[ "$code" = 401 ] && pass "a wrong password is refused (401)" || fail "wrong password answered $code"

exit "$fails"
SH
)

set +e
docker run --rm --network "$net" -e HOST="$host" -e ORIGIN="$ORIGIN" -e EMAIL="$email" -e PASSWORD="$password" \
  --entrypoint sh "$CURL_IMAGE" -c "$probe"
fails=$?
set -e

# 3. The server log: the expected database, local sign-in only, and no
# OpenID Connect activity. The log is read once and grepped from a here-string:
# `docker logs | grep -q` under pipefail fails whenever grep exits at its first
# match and docker logs then dies of SIGPIPE, which would turn a present line
# into a FAIL, and an OpenID Connect line into a false PASS.
log_text=$(logs)
[ -n "$log_text" ] || { echo "FAIL: the server log is empty"; fails=$((fails + 1)); }
if grep -q "\"msg\":\"database ready\",\"dialect\":\"$want_dialect\"" <<<"$log_text"; then
  echo "PASS: server log: database ready, dialect $want_dialect"
else
  echo "FAIL: server log does not say the $want_dialect database is ready"; fails=$((fails + 1))
fi
if grep -q '"msg":"keyhole listening".*"auth_mode":"local"' <<<"$log_text"; then
  echo "PASS: server log: listening with auth_mode local"
else
  echo "FAIL: server log does not show auth_mode local"; fails=$((fails + 1))
fi
if grep -qiE 'oidc|openid|zitadel|discovery' <<<"$log_text"; then
  echo "FAIL: server log mentions OpenID Connect in local mode:"; grep -iE 'oidc|openid|zitadel|discovery' <<<"$log_text" | head -n 10 || true
  fails=$((fails + 1))
else
  echo "PASS: server log has no OpenID Connect activity"
fi

echo "---"
if [ "$fails" -gt 0 ]; then echo "FAILED: $fails check(s)"; logs | tail -n 50; else echo "PASSED: all checks"; fi
exit "$fails"
