#!/usr/bin/env bash
# Runs real Sentry SDKs against a real, disposable Faultline: starts
# TimescaleDB in Docker (same image + pattern as internal/testdatabase),
# builds and migrates faultline, seeds a project/key via SQL, starts the
# server, then runs the @sentry/node and sentry-sdk checks against it.
#
# Self-contained: everything it starts, it tears down on exit, including on
# failure. This is a test run (like `make test-integration`), not a resident
# dev server.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

FAULTLINE_PORT="${FAULTLINE_PORT:-18080}"
DB_CONTAINER="faultline-e2e-db-$$"
PG_PASSWORD="faultline-e2e"
BUILD_DIR="$(mktemp -d)"
BIN_PATH="$BUILD_DIR/faultline"
SERVER_PID=""

cleanup() {
  local status=$?
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    # SIGTERM is not guaranteed to be honored (a wedged server, a signal
    # handler bug). Without a bounded wait, an unresponsive process here blocks
    # this trap forever, and the container/build-dir cleanup below never runs.
    local kill_deadline=$((SECONDS + 10))
    while kill -0 "$SERVER_PID" 2>/dev/null; do
      if (( SECONDS > kill_deadline )); then
        kill -9 "$SERVER_PID" 2>/dev/null || true
        break
      fi
      sleep 0.2
    done
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  docker rm --force "$DB_CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$BUILD_DIR"
  exit "$status"
}
trap cleanup EXIT INT TERM

echo "==> Starting TimescaleDB container ($DB_CONTAINER)"
# The label is the one the Go integration suite uses. The trap above removes the
# container on a normal exit, but a SIGKILL or a crashed shell skips the trap,
# and the label is then what lets make test-integration-clean find the leftover.
docker run --detach --rm --name "$DB_CONTAINER" \
  --label faultline-itest=1 \
  --env POSTGRES_DB=faultline \
  --env POSTGRES_USER=faultline \
  --env POSTGRES_PASSWORD="$PG_PASSWORD" \
  --publish 127.0.0.1::5432 \
  timescale/timescaledb:latest-pg18@sha256:9508616d5b941ed931198504c5db3fb47e8f53f790732ea1e889591f1062057c >/dev/null

echo "==> Waiting for Postgres to accept connections"
deadline=$((SECONDS + 60))
until docker exec "$DB_CONTAINER" pg_isready -U faultline -d faultline >/dev/null 2>&1; do
  if (( SECONDS > deadline )); then
    echo "Postgres did not become ready in time" >&2
    exit 1
  fi
  sleep 0.5
done

DB_PORT="$(docker inspect --format '{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}' "$DB_CONTAINER")"
DATABASE_URL="postgres://faultline:${PG_PASSWORD}@127.0.0.1:${DB_PORT}/faultline?sslmode=disable"

echo "==> Building faultline binary"
(cd "$REPO_ROOT" && go build -o "$BIN_PATH" ./cmd/faultline)

echo "==> Running migrations"
# pg_isready above only proves the *first* Postgres process the entrypoint
# starts is up; the official Postgres/Timescale entrypoint then stops that
# process and restarts it once init scripts finish, and a connection landing
# in that restart window fails with "unexpected EOF" even though the port is
# already accepting TCP. Retry the actual connection instead of trusting
# pg_isready alone.
deadline=$((SECONDS + 60))
until DATABASE_URL="$DATABASE_URL" "$BIN_PATH" migrate up; do
  if (( SECONDS > deadline )); then
    echo "migrations did not succeed in time" >&2
    exit 1
  fi
  sleep 1
done

echo "==> Seeding project and ingest key"
PUBLIC_KEY="e2e_$(printf '%04x%04x%04x%04x' "$RANDOM" "$RANDOM" "$RANDOM" "$RANDOM")"
# Values are script-generated, not attacker-controlled, but psql -v/:'name'
# parameter binding is the pattern to teach here regardless — string-building
# SQL from shell variables is how that stops being true the next time this
# script grows a new caller. Note: psql only performs :'var' interpolation on
# SQL it reads as a script (stdin/-f); passed via -c it is sent as a literal
# command and ':' is a syntax error, so the statements below go over stdin.
PROJECT_ID="$(docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -tAq -U faultline -d faultline \
  -v slug='sentry-e2e' -v name='Sentry E2E' -v platform='other' <<'SQL'
INSERT INTO projects (slug, name, platform) VALUES (:'slug', :'name', :'platform') RETURNING id;
SQL
)"
docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -U faultline -d faultline \
  -v project_id="$PROJECT_ID" -v public_key="$PUBLIC_KEY" <<'SQL' >/dev/null
INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst) VALUES (:'project_id'::bigint, :'public_key', 1000, 1000);
SQL

echo "==> Seeding an artifact token"
# The raw token is fl_art_<prefix>_<secret>, both parts 32 random bytes in
# base64url, and token_hash is sha256 of the whole raw string — see
# internal/artifactauth.Service.Create. Seeded via SQL for the same reason the
# ingest key is: there is no CLI for it and the admin dance buys nothing here.
read -r ARTIFACT_TOKEN ARTIFACT_PREFIX ARTIFACT_HASH < <(python3 - <<'PYTOKEN'
import base64, hashlib, os
part = lambda: base64.urlsafe_b64encode(os.urandom(32)).rstrip(b"=").decode()
prefix, secret = part(), part()
raw = f"fl_art_{prefix}_{secret}"
print(raw, prefix, hashlib.sha256(raw.encode()).hexdigest())
PYTOKEN
)
docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -U faultline -d faultline \
  -v project_id="$PROJECT_ID" -v prefix="$ARTIFACT_PREFIX" -v hash="$ARTIFACT_HASH" <<'SQL' >/dev/null
INSERT INTO artifact_tokens (project_id, name, token_prefix, token_hash)
VALUES (:'project_id'::bigint, 'e2e', :'prefix', decode(:'hash', 'hex'));
SQL
export FAULTLINE_E2E_ARTIFACT_TOKEN="$ARTIFACT_TOKEN"
export FAULTLINE_E2E_PROJECT_SLUG='sentry-e2e'
export FAULTLINE_E2E_URL="http://127.0.0.1:${FAULTLINE_PORT}"

echo "==> Starting Faultline on :${FAULTLINE_PORT}"
DATABASE_URL="$DATABASE_URL" FAULTLINE_ADDR=":${FAULTLINE_PORT}" FAULTLINE_COOKIE_SECURE=false \
  "$BIN_PATH" &
SERVER_PID=$!

echo "==> Waiting for /readyz"
deadline=$((SECONDS + 30))
until curl -fsS "http://127.0.0.1:${FAULTLINE_PORT}/readyz" >/dev/null 2>&1; do
  if (( SECONDS > deadline )); then
    echo "faultline did not become ready in time" >&2
    exit 1
  fi
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "faultline exited before becoming ready" >&2
    exit 1
  fi
  sleep 0.5
done

echo "==> Checking the project access boundary"
# A member reaches a project only through a group. This flow seeds a member
# granted the ingest project and nothing else, then asks the server for the
# project it was never granted.
#
# The session is written straight into `sessions` the way the auth adapter
# would (token_hash is sha256 of the *raw* 32 bytes the cookie carries
# base64url-encoded — see internal/auth.Service.Login), because `faultline
# user create` only ever produces an owner and reads its password from a
# terminal. The request itself is a real, cookie-authenticated HTTP call, so
# RequireSession resolves the scope exactly as it would for a browser.
FOREIGN_SLUG="e2e-foreign"
docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -U faultline -d faultline \
  -v foreign_slug="$FOREIGN_SLUG" -v granted_slug='sentry-e2e' <<'SQL' >/dev/null
INSERT INTO projects (slug, name, platform) VALUES (:'foreign_slug', 'Foreign', 'other');
INSERT INTO users (email, name, password_hash, role)
    VALUES ('member@e2e.test', 'Member', 'seeded-no-password-login', 'member');
INSERT INTO groups (name) VALUES ('e2e-granted');
INSERT INTO group_members (group_id, user_id)
    SELECT g.id, u.id FROM groups g, users u WHERE g.name = 'e2e-granted' AND u.email = 'member@e2e.test';
INSERT INTO group_projects (group_id, project_id)
    SELECT g.id, p.id FROM groups g, projects p WHERE g.name = 'e2e-granted' AND p.slug = :'granted_slug';
SQL

read -r SESSION_TOKEN SESSION_TOKEN_HASH < <(python3 - <<'PYSESSION'
import base64, hashlib, os
raw = os.urandom(32)
print(base64.urlsafe_b64encode(raw).rstrip(b"=").decode(), hashlib.sha256(raw).hexdigest())
PYSESSION
)
docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -U faultline -d faultline \
  -v token_hash="$SESSION_TOKEN_HASH" <<'SQL' >/dev/null
INSERT INTO sessions (user_id, token_hash, created_at, expires_at, last_seen_at)
    SELECT u.id, decode(:'token_hash', 'hex'), now(), now() + interval '1 hour', now()
    FROM users u WHERE u.email = 'member@e2e.test';
SQL

member_get() {
  curl -sS -o "$2" -w '%{http_code}' \
    --cookie "faultline_session=${SESSION_TOKEN}" \
    "http://127.0.0.1:${FAULTLINE_PORT}$1"
}

# The granted project answers first: without it, a server that refused
# everything would satisfy the boundary assertion below for the wrong reason.
granted_status="$(member_get "/api/v1/projects/sentry-e2e/issues" /dev/null)"
if [[ "$granted_status" != "200" ]]; then
  echo "granted project's issue stream returned $granted_status, want 200" >&2
  exit 1
fi

# 404, not 403: a 403 would confirm the project exists.
foreign_status="$(member_get "/api/v1/projects/${FOREIGN_SLUG}/issues" /dev/null)"
if [[ "$foreign_status" != "404" ]]; then
  echo "foreign project's issue stream returned $foreign_status, want 404" >&2
  exit 1
fi

PROJECT_LIST_BODY="$BUILD_DIR/projects.json"
list_status="$(member_get "/api/v1/projects" "$PROJECT_LIST_BODY")"
if [[ "$list_status" != "200" ]]; then
  echo "project list returned $list_status, want 200" >&2
  exit 1
fi
if grep -q "\"${FOREIGN_SLUG}\"" "$PROJECT_LIST_BODY"; then
  echo "project switcher lists the ungranted project:" >&2
  cat "$PROJECT_LIST_BODY" >&2
  exit 1
fi
if ! grep -q '"sentry-e2e"' "$PROJECT_LIST_BODY"; then
  echo "project switcher is missing the granted project:" >&2
  cat "$PROJECT_LIST_BODY" >&2
  exit 1
fi
echo "==> Project access boundary holds"

# DSN format: {scheme}//{public_key}@{host}/{project_id} — see web/src/lib/dsn.ts.
export FAULTLINE_E2E_DSN="http://${PUBLIC_KEY}@127.0.0.1:${FAULTLINE_PORT}/${PROJECT_ID}"
export FAULTLINE_E2E_PROJECT_ID="$PROJECT_ID"
export FAULTLINE_E2E_DB_CONTAINER="$DB_CONTAINER"
export FAULTLINE_E2E_NONCE="$(printf '%04x%04x' "$RANDOM" "$RANDOM")"

echo "==> Installing e2e npm dependencies"
(cd "$SCRIPT_DIR" && npm install --no-audit --no-fund)

# @sentry/cli's postinstall fetches a platform binary from Sentry's CDN; on a
# network that blocks that download, the postinstall script itself exits
# non-zero. It is listed in optionalDependencies precisely so npm tolerates
# that: a failing optional dependency's whole subtree is removed from
# node_modules rather than failing `npm install` (verified empirically —
# forcing the download to fail left node_modules/@sentry empty and npm still
# exited 0). This check is optional: skip it visibly rather than failing the
# whole E2E over a binary this repository does not vendor.
SENTRY_CLI_BIN="$SCRIPT_DIR/node_modules/.bin/sentry-cli"
if [[ ! -x "$SENTRY_CLI_BIN" ]]; then
  echo "==> sentry-cli binary not found at $SENTRY_CLI_BIN; skipping the real sentry-cli source map upload check"
else
  echo "==> Running the real sentry-cli source map upload"
  SOURCEMAP_DIR="$BUILD_DIR/dist"
  mkdir -p "$SOURCEMAP_DIR"
  cat > "$SOURCEMAP_DIR/app.js" <<'JS'
export function boom() { throw new Error("e2e sourcemap probe"); }
JS
  cat > "$SOURCEMAP_DIR/app.js.map" <<'MAP'
{"version":3,"file":"app.js","sources":["app.ts"],"sourcesContent":["export function boom(){ throw new Error('e2e sourcemap probe') }\n"],"names":["boom"],"mappings":"AAAA,SAASA"}
MAP

  # sentry-cli's `sourcemaps upload` does not inject debug ids itself — that is
  # the bundler plugins' job at build time, not this CLI subcommand's. Without
  # this step the manifest carries no per-file debug-id header at all and the
  # debug_refs assertion below can never pass.
  "$SENTRY_CLI_BIN" sourcemaps inject "$SOURCEMAP_DIR"

  SENTRY_URL="$FAULTLINE_E2E_URL" \
    SENTRY_AUTH_TOKEN="$FAULTLINE_E2E_ARTIFACT_TOKEN" \
    "$SENTRY_CLI_BIN" sourcemaps upload \
      --org e2e --project "$FAULTLINE_E2E_PROJECT_SLUG" \
      --release 'e2e@1.0.0' "$SOURCEMAP_DIR"

  echo "==> Asserting the bundle landed under both addresses"
  docker exec -i "$DB_CONTAINER" psql -v ON_ERROR_STOP=1 -tAq -U faultline -d faultline <<'SQL' > "$BUILD_DIR/refs.txt"
SELECT count(*) FILTER (WHERE debug_id IS NOT NULL),
       count(*) FILTER (WHERE debug_id IS NULL),
       count(DISTINCT blob_id),
       coalesce(max(generated_path), '')
FROM source_map_refs;
SQL
  IFS='|' read -r debug_refs path_refs blobs generated_path < "$BUILD_DIR/refs.txt"
  if [[ "$debug_refs" -lt 1 ]]; then
    echo "no debug-id refs stored; sentry-cli upload did not land" >&2
    exit 1
  fi
  if [[ "$path_refs" -lt 1 ]]; then
    echo "no release+path refs stored; the --release branch of assemble is broken" >&2
    exit 1
  fi
  if [[ "$blobs" -ne 1 ]]; then
    echo "stored $blobs blobs for one map; both refs must share one blob" >&2
    exit 1
  fi
  # The release+path ref is addressed by what a browser stack frame names —
  # the minified file — not by the map's own url. A ref at "/app.js.map" is
  # a row the resolver can never look up, and every count above still passes
  # with one stored there.
  if [[ "$generated_path" != "/app.js" ]]; then
    echo "release+path ref stored at '$generated_path'; the resolver looks up /app.js" >&2
    exit 1
  fi
  echo "==> sentry-cli upload stored $debug_refs debug-id and $path_refs release+path refs on 1 blob at $generated_path"
fi

echo "==> Running @sentry/node compatibility check (query-string auth)"
(cd "$SCRIPT_DIR" && npm test)

echo "==> Running sentry-sdk compatibility check (X-Sentry-Auth header auth)"
python3 -m venv "$SCRIPT_DIR/.venv"
"$SCRIPT_DIR/.venv/bin/pip" install --quiet -r "$SCRIPT_DIR/requirements.txt"
"$SCRIPT_DIR/.venv/bin/python" "$SCRIPT_DIR/compat_python.py"

echo "==> Both Sentry SDK compatibility checks passed"
