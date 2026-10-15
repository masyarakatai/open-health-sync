// SPDX-License-Identifier: AGPL-3.0-only

// Package shl makes share links: SMART Health Links (HL7 FHIR IG "SMART
// Health Cards and Links" STU1) that let a person show a read-only snapshot
// of some of their own data to someone outside the tenant's app.
//
// A share is one FHIR Bundle, written at creation into a DEFLATE stream and
// encrypted as a compact JWE (alg dir, enc A256GCM) with a fresh 32 byte key.
// The key goes into the shlink:/ payload returned once and is never stored:
// the instance keeps the ciphertext, the SHA-256 of the manifest id, and the
// PBKDF2 hash of an optional passcode. The manifest route serves the file to
// whoever holds the link, records every access, counts wrong passcodes and
// disables the share after too many. A share expires, is revoked by its
// owner, by a consent change that refuses a category it holds, or by the
// erasure of a provider whose records it holds; revoking removes the file.
//
// The package has the encryption (jwe.go), the payload (payload.go), the
// passcode hash (passcode.go), the request rules (rules.go), the Bundle
// writer (snapshot.go), the store (store.go) and the built-in viewer
// (viewer.go).
package shl
