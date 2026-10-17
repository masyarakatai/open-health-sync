# Plan: Xiaomi

Spec: [specs/xiaomi.md](../specs/xiaomi.md).

1. Tests first: an invented Mi Fitness copy (`connectors/xiaomi/export/testdata/mi_fitness`) with
   both CSV layouts seen in copies (plain, and every line wrapped in quotes), a golden conversion
   (`connectors/xiaomi/testdata/upload.ndjson`), and mapping tests on every key of the table.
2. `connectors/xiaomi`: device connector (`ParseUpload` checks `table` and `key` on every line,
   `Map` is pure), registered in `connectors/all`.
3. `connectors/xiaomi/export`: folder or zip reader, header normalization, account id and alias
   dropped, device joined by data source id, parts of at most 8 MiB, batch id from the files.
4. Route `POST /v1/ingest/xiaomi` in `internal/api`, wired in `internal/app`; OpenAPI path,
   regenerated server SDKs and Postman collection.
5. `ohs import mi-fitness` in `cmd/ohs`, reusing the Apple Health uploader (retries, 409 and
   auth handling); help text and CLI tests (no token, account id or value in the output).
6. Health Connect source slugs for the Xiaomi packages.
7. Docs: connector guide, connectors table, API page, CHANGELOG.

No migration and no new dependency.
