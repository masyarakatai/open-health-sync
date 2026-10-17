# Plan: self reports

Spec: [specs/self-reports.md](../specs/self-reports.md).

1. Tests first: the connector's line validation and mapping (kinds, scales, whole numbers,
   times, ids, corrections), the range rules, the consent categories, the FHIR category.
2. Catalog: the eight types in `internal/model`, their storage unit, migration 0036 seeding
   `quantity_codes` (LOINC 72514-3 and UCUM `{score}`), range rules in `internal/etl`, consent
   rules in `internal/consent`, the `survey` category in `internal/fhir`.
3. `internal/selfreport` defines a report (kinds, checks, line format, sample), shared by the
   route and the connector. Connector `connectors/selfreport`: `ParseUpload` checks each NDJSON
   line, `Map` writes one replaceable quantity sample per report, keyed by its id.
4. `collect.StorePart`: the store step of the upload handler, callable without HTTP, so the
   JSON route writes the raw part exactly as an upload does.
5. `POST /v1/users/{id}/self-reports` in `internal/api`, wired in `internal/app`, described in
   `api/openapi.yaml`; contract and integration tests (tenant key, user token, wrong user,
   validation, duplicate, correction, consent, FHIR read back).
6. Docs: guide `docs/self-reports.md`, `docs/api.md`, `docs/fhir.md`, regenerated
   `docs/data-model.md`, server SDKs and Postman collection (`make sdk`), CHANGELOG.
