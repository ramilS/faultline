# Sentry SDK compatibility harness

Twelve tasks of unit and integration tests assert against our reading of the
Sentry ingestion protocol. A bug in that reading is shared by the code and its
tests, so only a real Sentry SDK talking to a real, running Faultline can find
it. This harness is that check.

Two SDK families, deliberately:

- **`@sentry/node`** — the query-string auth form (`?sentry_key=…`), the same
  transport code `@sentry/browser` uses.
- **`sentry-sdk`** (Python) — the `X-Sentry-Auth` header form.

Alongside them, a real **`sentry-cli`** binary uploads a source map. Every
other test of the source map upload protocol — unit and integration alike —
parses a bundle that our own code also built, so a divergence between our
reading of the wire format and the real CLI's would stay green through both
sides changing together. Only a real `sentry-cli` talking to a real,
running Faultline can catch that.

Both send a real nested exception (an outer error wrapping an inner cause)
through a real call stack. The harness asserts on the resulting issue's
**title** and **culprit**, not merely that an issue appeared:

- **Title** must name the *outer* exception. Sentry orders `exception.values`
  oldest-first, so the outer exception is the last element — nothing in the
  unit tests independently proves that ordering; only a real SDK can.
- **Culprit** must name the function that threw, not the process entry point.
  Sentry's `stacktrace.frames` are caller-to-callee; Faultline reverses them
  to callee-first before reading the first in-app frame. If that reversal
  were backwards, culprit would be the entry point on every issue.

## Running it

```sh
make test-e2e
```

or directly:

```sh
./e2e/run.sh
```

`FAULTLINE_PORT` (default `18080`) selects the port the disposable server
binds — port 8080 is commonly taken locally.

## What it does

`run.sh` is self-contained: everything it starts, it tears down on exit
(`trap cleanup EXIT INT TERM`), including on failure.

1. Starts a disposable TimescaleDB container (same image and pattern as
   `internal/testdatabase`, used by `make test-integration`).
2. Builds the `faultline` binary and runs `faultline migrate up` against it.
3. Seeds a project, an ingest key, and an artifact token **directly via
   SQL** — keys are stored in plaintext, so this needs no admin-login dance,
   matching established local practice for ingestion E2E on this repo.
4. Starts `faultline` on `FAULTLINE_PORT` and waits for `/readyz`.
5. Exports the seeded DSN and project id as `FAULTLINE_E2E_DSN` and
   `FAULTLINE_E2E_PROJECT_ID`, plus the DB container name and a random nonce,
   then runs the `sentry-cli` check followed by the two SDK checks.

### `compat.test.ts` (`@sentry/node`)

Installs its own dependencies (`e2e/package.json` — deliberately outside the
root npm workspace, so `@sentry/node` never enters the production dependency
graph), initializes the SDK against `FAULTLINE_E2E_DSN`, and throws a real
nested error three call frames deep. `Sentry.captureException` +
`Sentry.flush` deliver it over the query-string auth path.

### `compat_python.py` (`sentry-sdk`)

Same shape, run against a throwaway virtualenv (`e2e/.venv`, created and
populated from `e2e/requirements.txt` by `run.sh`). Delivers over the
`X-Sentry-Auth` header path — the second auth form Faultline accepts.

### The real `sentry-cli` check (inline in `run.sh`)

`@sentry/cli` is pinned to `2.58.6` in `e2e/package.json` — the exact version
`internal/sentrycli/testdata/VERSION` records, not a caret range. A caret
range would let the CLI drift to a newer protocol silently; pinning makes
that drift a loud, explicit failure here instead, which is the entire point
of running a real binary rather than a fixture.

The check itself, run by `run.sh` before the two SDK checks:

1. Writes a throwaway `app.js` / `app.js.map` pair to a scratch directory.
2. Runs `sentry-cli sourcemaps inject` on that directory. `sourcemaps
   upload` does not inject debug ids itself — the `@sentry/*` bundler
   plugins inject at build time, before ever calling `upload` — so without
   this step the manifest carries no debug-id header and the assertion below
   can never pass.
3. Runs `sentry-cli sourcemaps upload`, authenticated with the seeded
   artifact bearer token, against the running Faultline.
4. Asserts on the **shape of the stored rows**, not merely a 200: it queries
   `source_map_refs` directly via SQL for a debug-id ref, a release+path ref,
   and confirms both point at the same `blob_id` — proving the one file the
   CLI uploaded was addressed both ways from a single stored copy. It also
   asserts the release+path ref's `generated_path` is `/app.js`, the minified
   file a browser stack frame names, which is the address the resolver in
   `internal/sourcemap` actually looks up. The three counts alone pass just as
   happily with the ref stored at the map's own `/app.js.map`, where nothing
   can ever reach it.

`sentry-cli`'s own binary is fetched by its npm postinstall script; if that
download is unavailable (a restricted network), the check logs that it is
skipping and the rest of the harness still runs — a missing optional binary
is not a harness failure.

### Verifying delivery

Both checks query the `issues` table **directly via SQL**
(`docker exec <container> psql`), not the authenticated dashboard API: every
dashboard route requires a session cookie, and standing up that login flow
here would test the login flow, not Sentry-protocol compatibility. Querying
Postgres directly is the same choice already made for seeding, applied
consistently to verification.

## Why real SDKs, not a hand-written HTTP client

A hand-written fixture that posts a Sentry-shaped envelope would only prove
that Faultline agrees with our own reading of the spec — the exact thing
under test. `@sentry/node` and `sentry-sdk` are Sentry's own client
implementations: their envelope construction, auth headers, gzip framing, and
frame/exception ordering come from Sentry, not from us.

## Cleaning up manually

If a run is interrupted in a way the trap can't catch (e.g. `kill -9` on the
shell itself), remove the stray container by name:

```sh
docker ps -a --filter name=faultline-e2e-db
docker rm --force faultline-e2e-db-<pid>
```

`e2e/node_modules` and `e2e/.venv` are left in place between runs (like any
other dependency cache) and are gitignored.
