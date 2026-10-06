# `sentry-cli upload-proguard` protocol fixtures

Real captured bytes, recorded on 2026-09-28 by pointing `sentry-cli` at a throwaway Go HTTP
recorder that wrote each request verbatim (`httputil.DumpRequest`) and answered with plausible
stubs. Nothing was hand-written or edited. **Never edit these files by hand**; to change them,
re-record the whole set so the mapping, its SHA-1 and its UUID stay consistent.

Two sentry-cli generations speak two different protocols for the same command, and Android
builds get whichever CLI their Gradle plugin bundles, so both are pinned.

| File | CLI | What it is |
|---|---|---|
| `mapping.txt` | both | The R8-style mapping every capture uploaded. SHA-1 `2c4a86fe85e18a7ffc127825adf217fa694a6878`; the CLI derives mapping UUID `cd395950-361a-563d-9df3-49aea740c6f6` from its content. |
| `legacy-2.58.6-request.txt` | 2.58.6 | The only request `upload-proguard` sends: a multipart `POST /api/0/projects/demo/demo/files/dsyms/` whose one `file` part is a zip holding `proguard/<uuid>.txt`. There is no capability probe and no chunk upload. The multipart `filename` is a random UUID, not the mapping UUID. |
| `chunked-3.8.0-options-request.txt` | 3.8.0 | `GET /api/0/organizations/demo/chunk-upload/`, the capability probe. |
| `chunked-3.8.0-assemble-request.txt` | 3.8.0 | `POST /api/0/projects/demo/demo/files/difs/assemble/`. The key is the SHA-1 of the raw mapping; `name` is `/proguard/<uuid>.txt`; `chunks` lists that one SHA-1 (there is no debug id). Sent twice: first to learn the missing chunks, again after upload. |
| `chunked-3.8.0-chunk-upload-request.txt` | 3.8.0 | `POST /api/0/organizations/demo/chunk-upload/`, one `file_gzip` part named by the SHA-1 whose gzip body is the raw mapping. |

Recording rig: for each CLI run `npx --yes @sentry/cli@<version> --url <recorder> --auth-token <anything> upload-proguard --org demo --project demo mapping.txt`.
The recorder answered `GET .../chunk-upload/` with the capability JSON this server advertises
(`accept` including `debug_files`), the first assemble with
`{"<sha1>":{"state":"not_found","missingChunks":["<sha1>"]}}` and the second with
`{"<sha1>":{"state":"ok","missingChunks":[]}}`.

## What each CLI needs back

Found by probing the recorder with candidate bodies, not from documentation:

- 2.58.6 parses the legacy response as a JSON **array** of debug files. An object (including
  `{}`) fails with `invalid type: map, expected a sequence`. Each element must carry `id`,
  `debugId`, `objectName`, `cpuName`, `sha1`, `size`, `dateCreated` and `headers`; a missing
  `objectName` fails with `missing field`. `[]` is accepted and reports zero uploaded files.
- 3.8.0 succeeded against the existing chunk-upload options, whose `accept` list already
  contains `debug_files`, and the same assemble response shape the bundle route uses.
