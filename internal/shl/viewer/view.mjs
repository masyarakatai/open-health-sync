// SPDX-License-Identifier: AGPL-3.0-only

// The share link viewer (/shl/view#shlink:/...). It reads the link from the
// address fragment (never sent to the server), asks for the reader's name
// and, when the link says so, the passcode, posts the manifest request to
// the link's url on this origin, decrypts the file with WebCrypto (AES-GCM),
// inflates it with DecompressionStream, and shows one table per kind of
// record with its unit and provenance. It sends no cookie and no key, and
// writes every value with textContent. The helpers above main() are pure and
// tested with `node --test`.

// PREFIX starts every share link.
export const PREFIX = "shlink:/";

// MAX_RECIPIENT mirrors the server's limit on the reader's name, in characters.
export const MAX_RECIPIENT = 256;

// PAGE_ROWS is how many rows a table shows before its "show all" button.
export const PAGE_ROWS = 500;

const NAMESPACE = "https://github.com/soma-intelligence/open-health-sync/fhir";
const PROVIDER_PREFIX = NAMESPACE + "/provider/";
const SOURCE_SYSTEM = NAMESPACE + "/CodeSystem/source";

// ViewerError is a failure whose message is for the reader.
export class ViewerError extends Error {}

// decodeBase64Url decodes base64url without padding to bytes.
export function decodeBase64Url(s) {
  if (typeof s !== "string" || !/^[A-Za-z0-9_-]*$/.test(s) || s.length % 4 === 1) {
    throw new ViewerError("not base64url");
  }
  const bin = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

const DAMAGED = "This share link is damaged: copy it again in full.";

// parsePayload reads the link from an address fragment ("#shlink:/...").
export function parsePayload(hash) {
  const s = String(hash ?? "").replace(/^#/, "");
  if (!s.startsWith(PREFIX)) {
    throw new ViewerError("This page opens a share link: its address ends with #shlink:/ and the link.");
  }
  let p;
  try {
    p = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(decodeBase64Url(s.slice(PREFIX.length))));
  } catch {
    throw new ViewerError(DAMAGED);
  }
  if (!p || typeof p !== "object" || typeof p.url !== "string" || typeof p.key !== "string" || p.key.length !== 43) {
    throw new ViewerError(DAMAGED);
  }
  if (p.v !== undefined && p.v !== 1) {
    throw new ViewerError("This share link uses a version this viewer does not read.");
  }
  const flag = typeof p.flag === "string" ? p.flag : "";
  if (flag.includes("U")) {
    throw new ViewerError("This viewer does not open single file links (flag U).");
  }
  return {
    url: p.url,
    key: p.key,
    exp: Number.isInteger(p.exp) && p.exp > 0 ? p.exp : undefined,
    label: typeof p.label === "string" ? p.label.slice(0, 80) : "",
    passcode: flag.includes("P"),
  };
}

// basePath is the path the server is published under ("" at the root,
// "/ohs" behind a prefix), from the viewer's own path (base + "/shl/view").
export function basePath(pathname) {
  return String(pathname ?? "").replace(/\/shl\/view\/?$/, "");
}

// sameServer reports whether a manifest url is a share route of origin
// under base: the page may only call its own server (connect-src 'self').
export function sameServer(url, origin, base = "") {
  try {
    const u = new URL(url);
    return u.origin === origin && u.pathname.startsWith(base + "/shl/") && !u.username && !u.password;
  } catch {
    return false;
  }
}

// buildManifestRequest is the body of the manifest request.
export function buildManifestRequest(recipient, passcode, needsPasscode) {
  const r = typeof recipient === "string" ? recipient.trim() : "";
  if (r === "") throw new ViewerError("Enter your name.");
  if ([...r].length > MAX_RECIPIENT) throw new ViewerError(`Your name can be at most ${MAX_RECIPIENT} characters.`);
  if (/[\u0000-\u001f\u007f-\u009f\u2028\u2029\u202a-\u202e\u2066-\u2069]/.test(r)) {
    throw new ViewerError("Your name must be one line of plain text.");
  }
  const body = { recipient: r };
  if (needsPasscode) {
    if (typeof passcode !== "string" || passcode === "") throw new ViewerError("Enter the passcode.");
    body.passcode = passcode;
  }
  return JSON.stringify(body);
}

// manifestOutcome reads the manifest answer: the file (embedded or at a
// location), a wrong passcode with the attempts left, an inactive link, too
// many requests, or another failure.
export function manifestOutcome(status, body) {
  switch (status) {
    case 200: {
      const files = Array.isArray(body?.files) ? body.files : [];
      const f = files.find((x) => typeof x?.contentType === "string" && x.contentType.startsWith("application/fhir+json"));
      if (f && typeof f.embedded === "string") return { kind: "embedded", jwe: f.embedded };
      if (f && typeof f.location === "string") return { kind: "location", location: f.location };
      return { kind: "error", status };
    }
    case 401:
      return { kind: "passcode", remaining: Number.isInteger(body?.remainingAttempts) ? body.remainingAttempts : undefined };
    case 404:
      return { kind: "inactive" };
    case 429:
      return { kind: "busy" };
  }
  return { kind: "error", status };
}

// outcomeMessage explains an outcome that is not a file.
export function outcomeMessage(o, retryAfter) {
  switch (o.kind) {
    case "passcode":
      if (o.remaining === 0) return "Wrong passcode. The link is now disabled.";
      if (o.remaining === undefined) return "Wrong passcode.";
      return `Wrong passcode. ${o.remaining} ${o.remaining === 1 ? "attempt" : "attempts"} left.`;
    case "inactive":
      return "This link is not active: it expired, was revoked by the person who shared it, or does not exist.";
    case "busy":
      return `Too many requests from this network. Try again${retryAfter ? ` in ${retryAfter} seconds` : " later"}.`;
  }
  return `The server could not open this link (status ${o.status}). Try again later.`;
}

const BAD_FILE = "The shared file is not in a format this viewer reads.";

// decryptFile decrypts a compact JWE (alg dir, enc A256GCM, zip absent or
// DEF) with the link's key and returns its text.
export async function decryptFile(keyB64, jwe, subtle = globalThis.crypto.subtle) {
  const parts = String(jwe).split(".");
  if (parts.length !== 5 || parts[1] !== "") throw new ViewerError(BAD_FILE);
  let header;
  try {
    header = JSON.parse(new TextDecoder().decode(decodeBase64Url(parts[0])));
  } catch {
    throw new ViewerError(BAD_FILE);
  }
  if (header?.alg !== "dir" || header.enc !== "A256GCM" || (header.zip !== undefined && header.zip !== "DEF") ||
    header.crit !== undefined) {
    throw new ViewerError(BAD_FILE);
  }
  let keyBytes, iv, ct, tag;
  try {
    keyBytes = decodeBase64Url(keyB64);
    iv = decodeBase64Url(parts[2]);
    ct = decodeBase64Url(parts[3]);
    tag = decodeBase64Url(parts[4]);
  } catch {
    throw new ViewerError(BAD_FILE);
  }
  if (keyBytes.length !== 32 || iv.length !== 12 || tag.length !== 16) throw new ViewerError(BAD_FILE);
  const data = new Uint8Array(ct.length + tag.length);
  data.set(ct);
  data.set(tag, ct.length);
  let plain;
  try {
    const key = await subtle.importKey("raw", keyBytes, { name: "AES-GCM" }, false, ["decrypt"]);
    plain = await subtle.decrypt(
      { name: "AES-GCM", iv, additionalData: new TextEncoder().encode(parts[0]), tagLength: 128 }, key, data);
  } catch {
    throw new ViewerError("The shared file cannot be decrypted with this link's key.");
  }
  let bytes = new Uint8Array(plain);
  if (header.zip === "DEF") {
    try {
      const stream = new Blob([bytes]).stream().pipeThrough(new DecompressionStream("deflate-raw"));
      bytes = new Uint8Array(await new Response(stream).arrayBuffer());
    } catch {
      throw new ViewerError(BAD_FILE);
    }
  }
  return new TextDecoder().decode(bytes);
}

// codeName is the name a code shows: its text, else its first display or code.
export function codeName(code) {
  if (typeof code?.text === "string" && code.text !== "") return code.text;
  const c = Array.isArray(code?.coding) ? code.coding.find((x) => x?.display || x?.code) : undefined;
  return c?.display || c?.code || "Unnamed record";
}

function quantity(q) {
  if (!q || q.value === undefined || q.value === null) return "";
  return `${q.value} ${q.unit ?? q.code ?? ""}`.trim();
}

function absent(reason) {
  return `no value (${codeName(reason)})`;
}

// formatValue is an Observation's value as text, with its unit: the value,
// or each component as "name value unit".
export function formatValue(o) {
  if (o.valueQuantity) return quantity(o.valueQuantity);
  if (o.valueCodeableConcept) return codeName(o.valueCodeableConcept);
  if (typeof o.valueString === "string") return o.valueString;
  if (o.valueBoolean !== undefined) return String(o.valueBoolean);
  if (typeof o.valueInteger === "number") return String(o.valueInteger);
  if (Array.isArray(o.component) && o.component.length > 0) {
    return o.component.map((c) => {
      let v = "";
      if (c.valueQuantity) v = quantity(c.valueQuantity);
      else if (c.valueCodeableConcept) v = codeName(c.valueCodeableConcept);
      else if (c.dataAbsentReason) v = absent(c.dataAbsentReason);
      return `${codeName(c.code)} ${v}`.trim();
    }).join("; ");
  }
  if (o.dataAbsentReason) return absent(o.dataAbsentReason);
  return "";
}

// localTime is an instant in the reader's time zone and locale.
export function localTime(iso) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(d);
}

// timeOf is an Observation's time: [start instant, text].
export function timeOf(o, fmt = localTime) {
  if (typeof o.effectiveDateTime === "string") return [o.effectiveDateTime, fmt(o.effectiveDateTime)];
  const p = o.effectivePeriod;
  if (p && typeof p.start === "string") {
    return [p.start, typeof p.end === "string" ? `${fmt(p.start)} to ${fmt(p.end)}` : fmt(p.start)];
  }
  return ["", ""];
}

function deviceName(d) {
  const n = Array.isArray(d.deviceName) ? d.deviceName.find((x) => x?.name) : undefined;
  return n?.name || d.modelNumber || d.identifier?.[0]?.value || d.id || "";
}

// observationGroups turns a Bundle into one group per kind of record (by
// its code's name, sorted), each row with its time, value and unit,
// provider, source and device.
export function observationGroups(bundle, fmt = localTime) {
  if (bundle?.resourceType !== "Bundle" || !Array.isArray(bundle.entry)) {
    throw new ViewerError("The shared file is not a FHIR Bundle.");
  }
  const devices = new Map();
  for (const e of bundle.entry) {
    const r = e?.resource;
    if (r?.resourceType === "Device" && typeof r.id === "string") devices.set("Device/" + r.id, deviceName(r));
  }
  const groups = new Map();
  for (const e of bundle.entry) {
    const o = e?.resource;
    if (o?.resourceType !== "Observation") continue;
    const name = codeName(o.code);
    let g = groups.get(name);
    if (!g) {
      g = { name, rows: [] };
      groups.set(name, g);
    }
    const [start, time] = timeOf(o, fmt);
    const src = typeof o.meta?.source === "string" ? o.meta.source : "";
    const tag = Array.isArray(o.meta?.tag) ? o.meta.tag.find((t) => t?.system === SOURCE_SYSTEM) : undefined;
    g.rows.push({
      start,
      time,
      value: formatValue(o),
      provider: src.startsWith(PROVIDER_PREFIX) ? src.slice(PROVIDER_PREFIX.length) : src,
      source: tag?.code ?? "",
      device: o.device?.display || devices.get(o.device?.reference) || "",
    });
  }
  return [...groups.values()].sort((a, b) => a.name.localeCompare(b.name));
}

const COLUMNS = [["time", "Time"], ["value", "Value and unit"], ["provider", "Provider"], ["source", "Source"],
  ["device", "Device"]];

function el(tag, attrs = {}, text) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text !== undefined) n.textContent = text;
  return n;
}

function addRows(tbody, rows) {
  for (const r of rows) {
    const tr = el("tr");
    for (const [key] of COLUMNS) {
      const td = el("td");
      if (key === "time" && r.start) td.append(el("time", { datetime: r.start }, r.time));
      else td.textContent = r[key];
      tr.append(td);
    }
    tbody.append(tr);
  }
}

function renderGroup(g, i) {
  const wrap = el("div", { class: "table-wrap", role: "region", "aria-labelledby": `caption-${i}`, tabindex: "0" });
  const table = el("table");
  const caption = el("caption", { id: `caption-${i}` },
    `${g.name} (${g.rows.length} ${g.rows.length === 1 ? "record" : "records"})`);
  const thead = el("thead");
  const head = el("tr");
  for (const [, label] of COLUMNS) head.append(el("th", { scope: "col" }, label));
  thead.append(head);
  const tbody = el("tbody");
  addRows(tbody, g.rows.slice(0, PAGE_ROWS));
  table.append(caption, thead, tbody);
  wrap.append(table);
  const out = [wrap];
  if (g.rows.length > PAGE_ROWS) {
    const more = el("button", { type: "button", class: "more" }, `Show all ${g.rows.length} records of ${g.name}`);
    more.addEventListener("click", () => {
      addRows(tbody, g.rows.slice(PAGE_ROWS));
      more.remove();
      wrap.focus();
    });
    out.push(more);
  }
  return out;
}

function main() {
  const $ = (id) => document.getElementById(id);
  const status = $("status");
  const error = $("error");
  let payload;
  try {
    payload = parsePayload(location.hash);
  } catch (e) {
    error.textContent = e.message;
    return;
  }
  // The link holds the key: it leaves the address bar and the browser
  // history. Reloading the page needs the link again.
  history.replaceState(null, "", location.pathname + location.search);
  const base = basePath(location.pathname);
  if (!sameServer(payload.url, location.origin, base)) {
    error.textContent = "This viewer opens the share links of this server only. Open the link with the app it came with.";
    return;
  }
  if (payload.label) {
    $("share-title").textContent = payload.label;
    document.title = `${payload.label} (shared health data)`;
  }
  if (payload.exp) {
    $("share-exp").textContent = localTime(new Date(payload.exp * 1000).toISOString());
    $("share-facts").hidden = false;
    if (payload.exp * 1000 <= Date.now()) {
      error.textContent = "This link has expired.";
      return;
    }
  }
  const form = $("open-form");
  const submit = $("open-submit");
  const passcode = $("passcode");
  $("passcode-field").hidden = !payload.passcode;
  passcode.required = payload.passcode;
  form.hidden = false;
  $("recipient").focus();

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    error.textContent = "";
    let body;
    try {
      body = buildManifestRequest($("recipient").value, passcode.value, payload.passcode);
    } catch (e) {
      error.textContent = e.message;
      return;
    }
    submit.disabled = true;
    status.textContent = "Opening the link...";
    try {
      const res = await fetch(payload.url, {
        method: "POST", headers: { "Content-Type": "application/json" }, body,
        cache: "no-store", credentials: "omit", referrerPolicy: "no-referrer",
      });
      let json = null;
      try {
        json = await res.json();
      } catch {
        json = null;
      }
      const o = manifestOutcome(res.status, json);
      let jwe;
      if (o.kind === "embedded") {
        jwe = o.jwe;
      } else if (o.kind === "location" && sameServer(o.location, location.origin, base)) {
        const file = await fetch(o.location, { cache: "no-store", credentials: "omit", referrerPolicy: "no-referrer" });
        if (!file.ok) throw new ViewerError(outcomeMessage(manifestOutcome(file.status, null)));
        jwe = await file.text();
      } else if (o.kind === "location") {
        throw new ViewerError("The file of this link is on another server, which this viewer does not call.");
      } else {
        status.textContent = "";
        error.textContent = outcomeMessage(o, res.headers.get("Retry-After"));
        submit.disabled = o.kind === "inactive" || (o.kind === "passcode" && o.remaining === 0);
        if (o.kind === "passcode" && !submit.disabled) passcode.focus();
        return;
      }
      status.textContent = "Decrypting...";
      const text = await decryptFile(payload.key, jwe);
      let bundle;
      try {
        bundle = JSON.parse(text);
      } catch {
        throw new ViewerError(BAD_FILE);
      }
      const groups = observationGroups(bundle);
      const tables = $("tables");
      tables.replaceChildren(...groups.flatMap((g, i) => renderGroup(g, i)));
      const n = groups.reduce((sum, g) => sum + g.rows.length, 0);
      const made = typeof bundle.timestamp === "string" ? ` Snapshot made ${localTime(bundle.timestamp)}.` : "";
      $("data-summary").textContent = n === 0
        ? `The share holds no records.${made}`
        : `${n} ${n === 1 ? "record" : "records"} in ${groups.length} ${groups.length === 1 ? "table" : "tables"}.${made}`;
      form.hidden = true;
      $("data").hidden = false;
      status.textContent = "The shared data is shown below.";
      $("data-title").focus();
    } catch (e) {
      status.textContent = "";
      error.textContent = e instanceof ViewerError ? e.message : "The link could not be opened. Check your connection and try again.";
      submit.disabled = false;
    }
  });
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", main);
  else main();
}
