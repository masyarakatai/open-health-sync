# Plan: share links

Spec: [specs/share-links.md](../specs/share-links.md).

1. Tests first, in `internal/shl`: the JWE (a round trip; a file built by hand in the IG's
   documented structure, with an invented plaintext, decrypted by the package; tampered header,
   IV, ciphertext and tag, a wrong key and a wrong `alg` or `enc` refused alike; the DEFLATE
   bound), the `shlink:/` payload (fields, flag order, label length, key length, base64url
   without padding, parsing a viewer URL), the passcode hash (round trip, wrong passcode,
   length rules) and the expiry and attempt rules. A Go test writes an encrypted fixture with
   a fixed invented key that the viewer's node test decrypts, so both sides agree.
2. Migration 0037: `ohs_sync.shares`, `ohs_sync.share_accesses`, the `share_link` audit actor.
   The migration test and the provider erasure classification learn the tables.
3. `internal/shl`: JWE, payload, passcode, the store (create, list, get, revoke, revoke by
   source, manifest lookup under a row lock, access rows and tickets, the expiry sweep), the
   webhook events, and the embedded viewer.
4. `internal/api`: the management routes (snapshot built from the merged observation list into a
   DEFLATE stream, the 50000 cap, pending purges left out), the public manifest and file routes
   behind the rate limiter, audit actions, revocation on consent change. `internal/deletion`:
   revocation on provider erasure. `internal/etl/automation`: the hourly expiry sweep.
5. Integration tests: create (the file decrypts with the key of `uri`, holds what the categories
   and window say and nothing else), manifest (recipient required, embedded and `location`,
   single use ticket), passcode limits, expiry, revoke, audit rows, consent revocation, provider
   erasure, user deletion, tenant isolation, the list without secrets. Contract tests for every
   new route.
6. `api/openapi.yaml`, the server SDKs and the Postman collection (`make sdk`).
7. Docs: the guide `docs/share-links.md`, api, webhooks, audit log, security model threat
   notes, self-hosting rate limits, the docs site list, CHANGELOG.

New dependency: none.
