# Plan: activity files

Spec: [specs/activity-files.md](../specs/activity-files.md).

1. Tests first: FIT fixtures written by the library's encoder from a test helper (a Garmin ride
   with GPS, heart rate and power; a Wahoo indoor ride; a COROS run with running power; a Zwift
   virtual ride; a two-session file; a monitoring file; a file with a broken CRC), regenerated
   with `-update` and checked against the committed bytes. Hand-written GPX (Garmin
   TrackPointExtension v1 and v2) and TCX (Strava's leading whitespace included) with invented
   values. Mapping tests on every field of the mapping table.
2. `connectors/activityfiles`: device connector. `ParseUpload` reads the one line, decodes the
   base64 file (at most 20 MiB) and refuses it unless it parses as an activity; `Map` parses
   it again and emits workouts, route points and samples. FIT through
   `github.com/muktihari/fit/decoder` with a message listener (no full file in memory beyond
   the decoded bytes); GPX and TCX through `encoding/xml` tokens. Registered in
   `connectors/all`.
3. `connectors/activityfiles/export`: walks a file, a folder or a zip (nested zips through a
   temporary file, `.gz` members decompressed with a cap), skips macOS metadata entries, yields
   one file at a time with its format and SHA-256.
4. `internal/model`: `Workout.SourceSlug` and `Workout.DeviceID`; the ETL writer fills
   `workouts.source_slug` and `device_id` from them (provider when empty, as before);
   regenerated `docs/data-model.md`.
5. Route `POST /v1/ingest/activity-files` in `internal/api`, wired in `internal/app`; OpenAPI
   path, regenerated server SDKs and Postman collection; contract test.
6. `ohs import files` in `cmd/ohs`, reusing the Apple Health uploader (retries, 409 and auth
   handling); help text; CLI tests (no file name, token or value in the output) and an
   integration test against a running server (a second run uploads nothing).
7. Docs: connector guide with the export steps per vendor, connectors table, API page, the
   pointer in the Garmin guide, CHANGELOG. Binary size measured with `make build` before and
   after, written in the spec.

No migration. One new dependency, `github.com/muktihari/fit` (reason in the spec).
