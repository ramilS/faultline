# Captured SDK envelopes

Envelopes captured from real Sentry SDKs (Python, Go, Ruby, PHP, Rust, Scala), two
runs each, replayed by `TestCapturedSDKEnvelopes` in `../../sdk_fixtures_test.go`.
Each `<name>.expect.json` sidecar holds facts known independently of Faultline's
output: SDK name prefix, outer exception type, culprit substring and exclusions.
Each language's `README` records the SDK version, capture command and date.

## Adding a language

1. Write a program under `<language>/src` that throws the same known error: `main`
   calls `runCheckout`, which calls `chargeCard`, which fails with a cause wrapped in
   an outer `CheckoutError` "could not check out".
2. Write the `<name>.expect.json` first, from what the program is known to do.
3. `capture.sh <language> <name> <image> <command...>`
4. `go test ./internal/ingest -run TestCapturedSDKEnvelopes`

## Known limits

- Fixtures are a snapshot of the SDK version in each language's README. They are not
  re-captured automatically, so a later SDK release can change the payload unseen.
- Framework entries (Django, FastAPI, Flask, Rails, Laravel, Symfony) share their
  language's fixture and differ only in the setup snippet. A payload difference that
  only a framework integration introduces is not verified.
- Setup snippets other than the plain-language ones follow the SDK docs as of
  2026-09-29. Some paths are convention, not stated by the docs: the Rails initializer
  path and the Symfony config file created by the Flex recipe.

## What this harness has already caught

- The Go SDK sends `exception` as a bare array, not `{"values": [...]}`.
- The Ruby SDK sends `"user": {}` on every event.
- The PHP SDK omits `event_id` from the event and sends it only in the envelope header.
- The Rust SDK puts the stack on `threads` instead of on the exception.
