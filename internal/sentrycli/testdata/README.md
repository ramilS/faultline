# `sentry-cli` protocol fixtures

Every file in this directory is **real captured bytes**, recorded on 2026-09-11 by pointing
`sentry-cli` 2.58.6 (see `VERSION`) and `@sentry/browser` 10.74.0 at a throwaway Go HTTP
recorder that logged each request verbatim and answered with plausible stubs. Nothing here was
hand-written or reconstructed from documentation, and no value in any file was changed. Two
files were reformatted, in ways that change no byte a parser reads: `event-debug-meta.json` was
pretty-printed, and `chunk-upload-request.txt`'s header block was rewritten from the recorder's
bare `\n` to the `\r\n` that was actually on the wire (see the note below). **Never edit these
files by hand** — a fixture that no longer matches the wire is worse than no fixture, because
every test that reads it keeps passing while the real CLI breaks.

To change them, re-record and replace the whole set at once, so the debug ids stay consistent
across files. The recording rig is: a throwaway HTTP server that writes every request verbatim to
a file and answers `GET .../chunk-upload/` with the capability JSON, `.../artifactbundle/assemble/`
with `{"state":"not_found","missingChunks":[<the chunks[] from the request>]}` and then
`{"state":"ok","missingChunks":[]}`, and everything else with `{}`; then
`sentry-cli sourcemaps inject <dir>` followed by
`sentry-cli --url <recorder> --auth-token <anything> sourcemaps upload --org demo --project demo --release 'web@1.0.0' --wait <dir>`.
For `event-debug-meta.json`, serve the same injected bundle from that server as a page with
`@sentry/browser` pointed at a DSN on the same origin, throw, and `Sentry.flush()`.

| File | What it is |
|---|---|
| `VERSION` | The pinned `sentry-cli` version. Everything else is only true of this version. |
| `chunk-upload-request.txt` | The verbatim `POST /api/0/organizations/demo/chunk-upload/` — request line, headers, then the raw `multipart/form-data` body. |
| `bundle.zip` | The artifact bundle reassembled from the chunk parts of that request, in the order `chunks[]` lists them in the assemble body. Note the 8-byte `SYSB\x02\x00\x00\x00` prefix before the zip's `PK` magic: it is part of the real payload, and both `unzip` and Go's `archive/zip` read the file as-is. |
| `manifest.json` | `manifest.json` extracted from `bundle.zip`, kept separately so a unit test can read it without unzipping. |
| `event-debug-meta.json` | The `debug_meta` object of a real error event, produced by `@sentry/browser` 10.74.0 running in Chromium against a page that loaded the same `app.js` this `bundle.zip` carries. Its `images[0].debug_id` equals the per-file `headers["debug-id"]` in `manifest.json`, so the two fixtures form one end-to-end debug-id oracle. |

`chunk-upload-request.txt` is byte-exact from the request line onward except for the line
endings noted above: the recorder wrote the request line and the header/body separator with bare
`\n`, so the header block was rewritten with `\r\n` to match what was actually on the wire. The
multipart body was never touched. The whole file parses with `http.ReadRequest` + `mime/multipart`, and gunzipping the one
part reproduces `bundle.zip` byte for byte, with its SHA-1 equal to the part's filename.

The capture used `--org demo --project demo --release 'web@1.0.0'` and a fake auth token; the
recorder advertised `"compression":["gzip"]`, which is why the multipart parts are named
`file_gzip` and are gzip-encoded. With `"compression":[]` the same CLI names them `file` and
sends the chunk uncompressed — a server must accept both.
