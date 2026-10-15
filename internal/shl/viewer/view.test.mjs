// SPDX-License-Identifier: AGPL-3.0-only

// Tests of the share link viewer's pure helpers: node --test internal/shl/viewer/view.test.mjs
// (no dependency, no DOM; the module only starts the page when document
// exists). testdata/fixture.json is written by the Go test TestViewerFixture
// with an invented key and invented records, so the viewer decrypts what the
// server encrypts.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

import {
  MAX_RECIPIENT,
  ViewerError,
  buildManifestRequest,
  codeName,
  decodeBase64Url,
  decryptFile,
  formatValue,
  manifestOutcome,
  observationGroups,
  outcomeMessage,
  parsePayload,
  sameServer,
  basePath,
  timeOf,
} from "./view.mjs";

const fixture = JSON.parse(readFileSync(new URL("./testdata/fixture.json", import.meta.url), "utf8"));
const same = (s) => s;

function b64url(s) {
  return Buffer.from(s, "utf8").toString("base64url");
}

test("decodeBase64Url", () => {
  assert.deepEqual([...decodeBase64Url("AAECAw")], [0, 1, 2, 3]);
  assert.deepEqual([...decodeBase64Url("-_8")], [251, 255]);
  assert.equal(decodeBase64Url(fixture.key).length, 32);
  for (const bad of ["a+b", "a/b", "abc=", "a", 42, null]) {
    assert.throws(() => decodeBase64Url(bad), ViewerError, String(bad));
  }
});

test("parsePayload reads the fixture's link from a fragment", () => {
  const p = parsePayload("#" + fixture.uri);
  assert.equal(p.key, fixture.key);
  assert.equal(p.label, "Invented share");
  assert.equal(p.passcode, false);
  assert.equal(p.exp, undefined);
  assert.ok(p.url.startsWith("https://ohs.example/shl/"));
  assert.deepEqual(parsePayload(fixture.uri), p, "with or without #");
});

test("parsePayload flags, version and damage", () => {
  const base = { url: "https://ohs.example/shl/x", key: fixture.key };
  const link = (o) => "#shlink:/" + b64url(JSON.stringify({ ...base, ...o }));
  assert.equal(parsePayload(link({ flag: "P" })).passcode, true);
  assert.equal(parsePayload(link({ flag: "LP" })).passcode, true);
  assert.equal(parsePayload(link({ exp: 1798761600 })).exp, 1798761600);
  assert.equal(parsePayload(link({ label: "x".repeat(100) })).label.length, 80);
  assert.equal(parsePayload(link({ v: 1 })).url, base.url);
  assert.throws(() => parsePayload(link({ v: 2 })), /version/);
  assert.throws(() => parsePayload(link({ flag: "U" })), /single file/);
  assert.throws(() => parsePayload(link({ key: "short" })), /damaged/);
  for (const bad of ["", "#", "#shc:/abc", "#shlink:/***", "#shlink:/" + b64url("not json"), "#shlink:/" + b64url("[]")]) {
    assert.throws(() => parsePayload(bad), ViewerError, bad);
  }
});

test("sameServer allows this origin's share routes only", () => {
  const origin = "https://ohs.example";
  assert.ok(sameServer("https://ohs.example/shl/abc", origin));
  assert.ok(sameServer("https://ohs.example/shl/file/abc", origin));
  for (const bad of ["https://other.example/shl/abc", "http://ohs.example/shl/abc", "https://ohs.example/v1/users",
    "https://u:p@ohs.example/shl/abc", "not a url", ""]) {
    assert.equal(sameServer(bad, origin), false, bad);
  }
});

test("sameServer follows the path the server is published under", () => {
  const origin = "https://ohs.example";
  assert.equal(basePath("/shl/view"), "");
  assert.equal(basePath("/ohs/shl/view"), "/ohs");
  assert.ok(sameServer("https://ohs.example/ohs/shl/abc", origin, "/ohs"));
  assert.equal(sameServer("https://ohs.example/shl/abc", origin, "/ohs"), false);
  assert.equal(sameServer("https://ohs.example/ohs/v1/users", origin, "/ohs"), false);
});

test("buildManifestRequest", () => {
  assert.deepEqual(JSON.parse(buildManifestRequest("  Dr. Invented  ", "", false)), { recipient: "Dr. Invented" });
  assert.deepEqual(JSON.parse(buildManifestRequest("Coach", "secret-1", true)), { recipient: "Coach", passcode: "secret-1" });
  assert.deepEqual(JSON.parse(buildManifestRequest("Coach", "secret-1", false)), { recipient: "Coach" },
    "no passcode unless the link asks");
  assert.doesNotThrow(() => buildManifestRequest("é".repeat(MAX_RECIPIENT), "", false));
  assert.throws(() => buildManifestRequest("é".repeat(MAX_RECIPIENT + 1), "", false), ViewerError);
  assert.throws(() => buildManifestRequest("   ", "", false), /name/);
  assert.throws(() => buildManifestRequest("a\nb", "", false), ViewerError);
  assert.throws(() => buildManifestRequest("Dr. \u202eevil", "", false), ViewerError, "a bidi override");
  assert.doesNotThrow(() => buildManifestRequest("\u0645\u06cc\u200c\u0646\u0627", "", false), "a joiner a script needs");
  assert.throws(() => buildManifestRequest("Coach", "", true), /passcode/);
});

test("manifestOutcome and its messages", () => {
  const file = (f) => ({ status: "finalized", files: [{ contentType: "application/fhir+json;fhirVersion=4.0.1", ...f }] });
  assert.deepEqual(manifestOutcome(200, file({ embedded: "a.b" })), { kind: "embedded", jwe: "a.b" });
  assert.deepEqual(manifestOutcome(200, file({ location: "https://ohs.example/shl/file/t" })),
    { kind: "location", location: "https://ohs.example/shl/file/t" });
  assert.equal(manifestOutcome(200, { files: [] }).kind, "error");
  assert.equal(manifestOutcome(200, null).kind, "error");
  assert.deepEqual(manifestOutcome(401, { remainingAttempts: 3 }), { kind: "passcode", remaining: 3 });
  assert.equal(manifestOutcome(404, null).kind, "inactive");
  assert.equal(manifestOutcome(429, null).kind, "busy");
  assert.equal(manifestOutcome(500, null).kind, "error");
  assert.match(outcomeMessage({ kind: "passcode", remaining: 3 }), /3 attempts left/);
  assert.match(outcomeMessage({ kind: "passcode", remaining: 1 }), /1 attempt left/);
  assert.match(outcomeMessage({ kind: "passcode", remaining: 0 }), /disabled/);
  assert.match(outcomeMessage({ kind: "busy" }, "7"), /in 7 seconds/);
  assert.match(outcomeMessage({ kind: "inactive" }), /not active/);
  assert.match(outcomeMessage({ kind: "error", status: 500 }), /500/);
});

test("decryptFile opens the file the server encrypted", async () => {
  const text = await decryptFile(fixture.key, fixture.jwe);
  const bundle = JSON.parse(text);
  assert.equal(bundle.resourceType, "Bundle");
  assert.equal(bundle.type, "collection");
  assert.equal(bundle.entry.length, fixture.entries);
});

test("decryptFile refuses a wrong key and a changed file", async () => {
  const other = Buffer.alloc(32, 7).toString("base64url");
  await assert.rejects(decryptFile(other, fixture.jwe), /cannot be decrypted/);
  const parts = fixture.jwe.split(".");
  const flip = (s) => {
    const b = Buffer.from(s, "base64url");
    b[b.length >> 1] ^= 1;
    return b.toString("base64url");
  };
  for (const i of [0, 2, 3, 4]) {
    const p = [...parts];
    p[i] = flip(p[i]);
    await assert.rejects(decryptFile(fixture.key, p.join(".")), ViewerError, `part ${i}`);
  }
  await assert.rejects(decryptFile(fixture.key, parts.slice(0, 4).join(".")), ViewerError);
  const header = (h) => [b64url(JSON.stringify(h)), ...parts.slice(1)].join(".");
  await assert.rejects(decryptFile(fixture.key, header({ alg: "A256KW", enc: "A256GCM" })), /format/);
  await assert.rejects(decryptFile(fixture.key, header({ alg: "dir", enc: "A128GCM" })), /format/);
  await assert.rejects(decryptFile(fixture.key, header({ alg: "dir", enc: "A256GCM", zip: "GZIP" })), /format/);
});

test("observationGroups: one table per kind, with unit and provenance", async () => {
  const bundle = JSON.parse(await decryptFile(fixture.key, fixture.jwe));
  const groups = observationGroups(bundle, same);
  const names = groups.map((g) => g.name);
  assert.deepEqual(names, [...names].sort((a, b) => a.localeCompare(b)), "sorted by name");
  const hr = groups.find((g) => g.name === "heart rate");
  assert.ok(hr, names.join(", "));
  assert.equal(hr.rows.length, 2);
  assert.deepEqual(hr.rows[0], {
    start: "2026-10-01T07:31:00Z", time: "2026-10-01T07:31:00Z", value: "61 count/min",
    provider: "invented_ring", source: "invented", device: "ring-1",
  });
  const bp = groups.find((g) => g.rows.some((r) => r.provider === "invented_cuff"));
  assert.match(bp.rows[0].value, /118 mmHg/);
  assert.match(bp.rows[0].value, /76 mmHg/);
  const total = groups.reduce((n, g) => n + g.rows.length, 0);
  assert.equal(total, 6, "every Observation of the fixture, no Patient or Device");
  assert.throws(() => observationGroups({ resourceType: "Patient" }), ViewerError);
});

test("formatValue, timeOf and codeName", () => {
  assert.equal(formatValue({ valueQuantity: { value: 5.1, unit: "mmol/L" } }), "5.1 mmol/L");
  assert.equal(formatValue({ valueQuantity: { value: 0, code: "%" } }), "0 %");
  assert.equal(formatValue({ dataAbsentReason: { text: "Not a Number (NaN)" } }), "no value (Not a Number (NaN))");
  assert.equal(formatValue({
    component: [
      { code: { text: "deep sleep" }, valueQuantity: { value: 3600, unit: "s" } },
      { code: { coding: [{ code: "rem_sleep_seconds" }] }, dataAbsentReason: { text: "unknown" } },
    ],
  }), "deep sleep 3600 s; rem_sleep_seconds no value (unknown)");
  assert.deepEqual(timeOf({ effectiveDateTime: "2026-10-01T07:00:00Z" }, same), ["2026-10-01T07:00:00Z", "2026-10-01T07:00:00Z"]);
  assert.deepEqual(timeOf({ effectivePeriod: { start: "a", end: "b" } }, same), ["a", "a to b"]);
  assert.deepEqual(timeOf({}, same), ["", ""]);
  assert.equal(codeName({ coding: [{ system: "x", display: "Steps" }] }), "Steps");
  assert.equal(codeName(undefined), "Unnamed record");
});
