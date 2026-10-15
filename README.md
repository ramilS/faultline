# Faultline

Self-hosted error and incident tracking. Faultline speaks the Sentry ingestion
protocol, so any Sentry SDK — browser, Python, Go, Ruby, PHP, Rust, Java, Scala,
whatever — reports to it by pointing its `dsn` at Faultline instead of Sentry. Faultline groups reported
events into issues, and an embedded dashboard lets you search, triage, and drill
down to individual events with full stack traces.

The whole deployment is one Go binary plus TimescaleDB. The dashboard is compiled
into the binary; there is no separate service, queue, or worker to run.

## What you get

- **Sentry-protocol-compatible ingestion** — one gzip-capable envelope endpoint
  reachable from any browser origin, per-key rate limits, and optional durable
  ack-before-write. `sentry-cli` and the `@sentry/*` bundler plugins upload source
  maps directly, authenticated with a per-project artifact token; Faultline's own
  upload endpoint keeps working alongside them. `sentry-cli upload-proguard` and
  the Sentry Android Gradle plugin upload ProGuard/R8 mappings the same way (see
  [Android](#android-readable-stack-traces)). Native debug-file upload
  (`sentry-cli debug-files upload`, dSYMs) is not supported. Commit association
  (`setCommits`) is accepted and discarded — Faultline tracks no repositories.
- **Deterministic grouping** — events fingerprint into issues across releases, with
  per-project rules when the default grouping is wrong, plus manual issue merge.
- **Data scrubbing** — on by default for every project: before an event is stored,
  values under keys such as `password`, `token`, `api_key`, `auth` or `cookie`, card
  numbers, and the event's `user.ip_address` (unless the project stores user IPs) are
  replaced with `[Filtered]`. Owners add sensitive fields, safe fields, or turn it off
  per project under *Data scrubbing*. Owners can also add regex rules that replace
  matches in any string (a bot token in a URL, an account number in a message): per
  project under *Data scrubbing*, or for every project under *Settings → Data
  scrubbing*. Instance-wide rules still apply to a project that turns scrubbing off.
  Events stored earlier are not rewritten, and with durable ingestion the raw envelope
  stays in the spill log until it is persisted. A stored rule that no longer validates
  (for example a hand-edited row) makes ingest fall back to the built-in scrubbing and
  report an error for each envelope until the rule is fixed.
- **Triage** — resolve, ignore, reopen, assign, and merge, with automatic reopen when
  a resolved issue recurs. *All issues* lists every project you can access in one
  stream; environment, release, and tag pickers stay per project, because tag values
  belong to a project.
- **Notifications** — a Telegram message or a webhook call when an issue first
  appears or a resolved one recurs (see [Notifications](#notifications)).
- **Issue detail** — occurrence charts, tag facets, event paging, request timelines,
  browser stack traces symbolicated from uploaded source maps, and Android stack
  traces retraced from uploaded ProGuard/R8 mappings.
- **Explicit operations** — no implicit migrations, no implicit accounts.

## Quickstart

Needs only Docker. Five minutes to a first issue in the dashboard.

```sh
cp .env.example .env
docker compose up --build -d
```

Create the schema and the first account. Neither happens automatically:

```sh
docker compose exec faultline faultline migrate up
docker compose exec faultline faultline migrate version   # expect: 18 clean
docker compose exec faultline faultline user create --email admin@example.com --name "Administrator"
```

The last command prompts for a password (12 characters minimum) without echoing
it. This account is an **owner**, so it reaches every project unconditionally.
Now sign in at **http://localhost:8080**, create a project, and create an
ingest key under its **Settings → Keys** — the dashboard shows that key's DSN
right next to it.

A **member** account is different: it reaches a project only through a group
that has been granted it, and there is no seed group, so a member with no
group sees no projects. `faultline user create` only ever produces an owner —
create a member from the dashboard's **Settings → Users** screen, then grant
it a project:

```sh
docker compose exec faultline faultline group create --name backend
docker compose exec faultline faultline group members --name backend --add member@example.com
docker compose exec faultline faultline group projects --name backend --add web
```

Send an event with that key's DSN and watch it appear in the project's stream:

```sh
curl -i "http://localhost:8080/api/1/envelope/?sentry_key=$FAULTLINE_KEY" \
  --data-binary $'{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc"}\n{"type":"event"}\n{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","level":"error","message":"hello from curl","exception":{"values":[{"type":"DemoError","value":"hello from curl"}]}}\n'
```

Replace `1` with your project's numeric id (the last segment of the DSN) and
`$FAULTLINE_KEY` with the key's public value. A `200` with `{"id":"…"}` means it
was accepted. This same DSN is what you paste into any Sentry SDK's `init` call.

## SDK setup

The dashboard's project setup screen offers a snippet per platform: browser and
JavaScript frameworks, Android, Python (Django, FastAPI, Flask), Go, Ruby (Rails),
PHP (Laravel, Symfony), Rust, and Scala (through the Java SDK). Python, Go, Ruby,
PHP, Rust and Scala were verified with events captured from the real SDKs, replayed
in the Go tests; see
[internal/ingest/testdata/sdk/README.md](internal/ingest/testdata/sdk/README.md)
for how that works and what it does not cover, notably framework-specific payloads.

## Android: readable stack traces

R8 and ProGuard rename classes and methods, so an Android crash arrives as
`a.b.c`. Upload the build's `mapping.txt` and Faultline retraces the frames each
time an issue is opened. Events are stored as the SDK sent them, and grouping and
fingerprints still see the obfuscated names.

Uploads authenticate with a per-project **artifact token**. An owner creates one
on the project's *Artifact tokens* screen, and the raw token is shown once, at
creation; the list afterwards shows only a prefix. The same is available over the
API with `POST /api/v1/projects/<slug>/artifact-tokens` and a JSON body of
`{"name": "gradle"}`, whose reply carries the `raw_token`.

With the Sentry Android Gradle plugin, point it at Faultline. `org` must be set
but Faultline ignores its value, and `projectName` is the project slug:

```kotlin
sentry {
    url = "https://faultline.example.com"
    org = "faultline"
    projectName = "web"
    // export SENTRY_AUTH_TOKEN=<raw_token>
}
```

The plugin uploads the mapping by default. Or upload one by hand:

```sh
SENTRY_AUTH_TOKEN=<raw_token> sentry-cli --url https://faultline.example.com \
  upload-proguard --org faultline --project web path/to/mapping.txt
```

Both `sentry-cli` 2.x (one multipart request) and 3.x (chunked) work; the protocols
were recorded from 2.58.6 and 3.8.0. The Gradle plugin runs whichever `sentry-cli`
it bundles, which was not tested. A frame is retraced only when its event names the
mapping in `debug_meta` (`{"type": "proguard", "uuid": …}`), which the SDK does from
the `sentry-debug-meta.properties` the plugin or `upload-proguard --write-properties`
generates. Source file names come from the SDK, or are guessed as `<Class>.java`, so
a Kotlin frame can show a `.java` name.

A mapping is capped at 100MB uncompressed and, once uploaded, is immutable: a
second upload naming the same mapping UUID with different content is rejected.
There is no way to delete a stored mapping short of direct SQL.

Each project may store 1 GiB (uncompressed) of source maps and mappings
combined; an upload that would exceed it is refused with `413`. Re-uploading
content the project already holds always succeeds. Set
`FAULTLINE_ARTIFACT_QUOTA_BYTES` to change the cap, or to `0` to remove it.

## Notifications

Owners add channels per project under *Notifications*: a Telegram chat (bot token
from [@BotFather](https://t.me/BotFather) plus a chat id or `@channel` username;
add the bot to the chat first) or a webhook URL. Each channel chooses new issues,
regressions (a resolved issue seen again), or both, a minimum level, and
optionally a list of environments. *Test* sends a sample message right away and
shows the destination's own error if it refuses.

The environment filter is judged on the events that created or reopened the
issue: an issue first seen in `staging` does not notify a `production`-only
channel, and later production events for it do not either, because the issue is
no longer new.

Set `FAULTLINE_PUBLIC_URL` (for example `https://faultline.example.com`) to put a
link to the issue in each message; without it messages carry no link.

Delivery is at least once. The notification is queued in the same transaction
that creates or reopens the issue, then sent in the background and retried with
backoff (30s doubling to an hour, or longer if the destination's `Retry-After`
asks for it) for up to 8 attempts. A rejection that retrying cannot fix, such as
a bad token or a 404, is not retried. The latest failure is shown on the
channel. Bot tokens and signing secrets are stored in the database in plain
text and are never returned by the API.

A webhook receives `POST` with a JSON body:

```json
{
  "event": "issue.new",
  "delivery_id": 42,
  "sent_at": "2026-10-15T09:30:00Z",
  "project": { "slug": "shop", "name": "Shop" },
  "issue": { "id": 7, "title": "TypeError: x is undefined", "culprit": "app.js", "level": "error", "url": "https://faultline.example.com/shop/issues/7" },
  "environment": "production"
}
```

`event` is `issue.new` or `issue.regression`, also sent as the
`X-Faultline-Event` header; `issue.url` is `null` without a public URL. A retry
repeats the same `X-Faultline-Delivery` id, so a receiver can drop duplicates.
With a signing secret set, `X-Faultline-Timestamp` carries Unix seconds and
`X-Faultline-Signature` is `sha256=` followed by the hex HMAC-SHA256, under the
secret, of the timestamp, a `.`, and the raw body. Redirects are not followed.

The request leaves from the Faultline server, which can reach what an outside
caller cannot, so a webhook URL is not allowed to point inside. Loopback
(`localhost`, `127.0.0.0/8`, `::1`), link-local (including the cloud metadata
address `169.254.169.254`), multicast and unspecified addresses are always
refused. Private ranges (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`,
`fc00::/7`, `100.64.0.0/10`) are refused unless the server runs with
`FAULTLINE_WEBHOOK_ALLOW_PRIVATE=true`, which a receiver on the same network
(n8n, Mattermost, an internal service by its container name) needs. The check
is made on the address a hostname resolves to when each request is sent, so a
name that later re-resolves inside is caught too; a refused delivery is not
retried, and the reason is shown on the channel. Webhook requests ignore
`HTTPS_PROXY`, since through a proxy only the proxy's address could be checked.

## Single sign-on (Google Workspace)

Optional. Members of a Google Workspace domain sign in with Google, and an
account is created on their first sign-in. Configuration is environment-only
and takes effect at startup — there is no UI for it.

| Variable | Meaning |
|---|---|
| `FAULTLINE_OIDC_CLIENT_ID` | OAuth client id. **This one is the switch**: SSO is off while it is unset. |
| `FAULTLINE_OIDC_ISSUER` | `https://accounts.google.com` for Google. |
| `FAULTLINE_OIDC_CLIENT_SECRET` | OAuth client secret. |
| `FAULTLINE_OIDC_REDIRECT_URL` | Where the provider sends the caller back: faultline serves the callback at `/api/v1/auth/sso/callback`, and this value must be the redirect URI registered with the provider. |
| `FAULTLINE_OIDC_ALLOWED_HD` | Comma-separated Workspace domains permitted to sign in. |

Once `FAULTLINE_OIDC_CLIENT_ID` is set, **every other variable is required**:
the process refuses to start if any is missing or empty, rather than falling
back to a permissive default. `FAULTLINE_OIDC_ISSUER` and
`FAULTLINE_OIDC_REDIRECT_URL` must be `https` (`http` is accepted only for
`localhost`, which is what Google itself permits for loopback development).

`FAULTLINE_OIDC_ALLOWED_HD` is matched **exactly**, case-insensitively, against
the `hd` claim inside the verified ID token. It is not the email domain and it
is not a suffix: configuring `company.com` admits `hd == "company.com"` and
nothing else — not `evil-company.com`, not `eu.company.com`, and not somebody
whose address merely ends in `@company.com`. A personal Google account carries
no `hd` claim at all, so it can never pass, whatever its address.

What a first sign-in produces:

- A **member** account with no group membership. Members reach projects only
  through groups, and there is no seed group, so a new member sees no projects
  until an owner adds them to one (**Settings → Users**, or `faultline group
  members --add`). This is deliberate: no IdP attribute is mapped to a
  faultline role, so signing in cannot grant access to anything.
- A session that lasts **12 hours**, against 7 days for a password session.
  Faultline speaks no SCIM, so it never learns that an IdP account was
  disabled; session expiry is its only revocation mechanism, and the shorter
  lifetime is what bounds how long a disabled Workspace account keeps working.

**An owner account is never linked by SSO.** Linking an identity to an existing
account matches on the `email` claim, which Google documents as neither unique
to an account nor stable over time — for an owner that would hand control of
the whole deployment to whoever holds that mailbox. Owners sign in with a
password, which is retained precisely so the team can still reach its own error
tracker when Google is unreachable. Members are linked as normal, which is how
an existing team migrates to SSO.

The sign-in callback is rate-limited **per source address**, not per account,
because at that point in the flow there is no account yet to key on. If you
terminate TLS at a reverse proxy, faultline sees the proxy's address for
everyone, so the limit becomes deployment-wide: five sign-ins burst, then one
every twelve seconds. A team arriving together drains that in about two
minutes — a refused caller re-clicks the Google button — but it is worth
knowing before you conclude SSO is broken.

> **Before enabling this in production, complete one manual sign-in against
> real Google.** The automated tests verify faultline's own OIDC handling
> against a locally signed token, with real signatures and real discovery — but
> a test IdP cannot reproduce a Google-specific quirk. That gap is known and
> accepted; the manual sign-in is what closes it.

## Repository layout

- `cmd/faultline` — the single server process entrypoint
- `internal` — backend module boundaries and infrastructure
- `web` — the embedded React dashboard
- `migrations` — PostgreSQL and TimescaleDB migrations
- `e2e` — real Sentry SDKs run against a disposable Faultline; see
  [e2e/README.md](e2e/README.md) for why unit tests cannot replace it

## Development

Building from source needs Go 1.27+ and Node.js 22+, matching the `go`
directive in `go.mod`. That floor already clears the 1.25.0 minimum
`github.com/coreos/go-oidc/v3` declares for the ID-token verification the SSO
sign-in path depends on. The Docker image builds the shipped binary on the
toolchain pinned in `go.mod`'s `toolchain` line, one patch release ahead of
the floor, to get current compiler and runtime fixes.

`make help` lists every target. The ones you will use:

```sh
make install           # npm ci
make build             # dashboard and the server binary
make test              # lint, Go and JavaScript checks; no Docker required
make lint              # Go linters alone; downloads a pinned golangci-lint on first run
make test-integration  # real PostgreSQL/TimescaleDB and ingest suites; needs Docker
make test-integration-clean  # remove integration containers an interrupted run left behind
make test-e2e          # real Sentry SDKs against a disposable stack; needs Docker, Node, and Python
```

`make test-integration` runs every package against a single TimescaleDB
container, started on first use and removed when the last test binary exits.
Each test still gets its own database inside it, so tests stay isolated
without paying for a container each. Kill a run hard enough to skip that
teardown (`kill -9`, a crashed IDE) and the container survives — the next run
reuses it, and `make test-integration-clean` removes it now.

Run `make test-e2e` after touching anything on the ingest path. The unit and
integration suites assert against our own reading of the Sentry protocol, so a
misreading is shared by the code and the tests that cover it — only a real SDK
talking to a real server catches that class of bug.

Building the dashboard before the binary matters: the dashboard is embedded at
compile time and build output is not tracked. `make build` does this in the right
order.

## License

Faultline is released under the [MIT License](LICENSE).
