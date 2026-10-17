# Plan: ECG read

Spec: [specs/ecg-read.md](../specs/ecg-read.md).

1. Tests first: the classification and symptoms labels per provider; a golden
   `observation-ecg-recording.json` and an ECG in the mixed searchset golden file; integration
   tests on invented rows for the list (window, paging, another tenant), the read by id (samples
   in offset order, truncation, 404s), the FHIR search (`code` by LOINC 11524-6 and by record
   type, `type` and `category` leaving ECGs out, the 100 ECG page cap), read by id, Provenance
   and `$everything`, the export and the archive, and the MCP tool (list, summary, no voltage
   array); the contract run reads every new route.
2. `internal/api/read`: `ecg.go` (types, list, read by id with samples, summary) and the ECG kind
   in the merged observation list (branch, by-id fetch, cursor, page cap); the Device search
   gains the ECG branch.
3. `internal/fhir`: `FromECG`, the `ecg-` id prefix, the record type and code system constants,
   `RecordDevice`.
4. `internal/api`: the two routes, the `ecg` export dataset, `canonical/ecg.ndjson` in the
   archive, the `code` search values.
5. `internal/mcp`: `get_ecg_recordings`, and the server instructions.
6. `api/openapi.yaml`, regenerated server SDKs and Postman collection (`make sdk`).
7. Docs: api, fhir, mcp, exports, the Apple Health guide, the data model generator note,
   CHANGELOG.

No migration (the existing indexes serve the reads) and no new dependency.
