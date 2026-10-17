// SPDX-License-Identifier: AGPL-3.0-only

// The console's pure helpers: no DOM, no global state. app.mjs builds the
// page with them; app.test.mjs tests them with `node --test`.

export const KEY_STORAGE = "ohs.console.key";

// The methods the console may send. In read-only mode, GET only.
export const WRITE_METHODS = ["POST", "PATCH", "PUT", "DELETE"];

// ReadOnlyError is thrown by the request wrapper for a request the mode or
// the origin rule refuses, before anything is sent.
export class ReadOnlyError extends Error {}

// ApiError is a non-2xx answer; detail comes from the problem+json body.
export class ApiError extends Error {
  constructor(status, detail) {
    super(`HTTP ${status}${detail ? ": " + detail : ""}`);
    this.status = status;
    this.detail = detail;
  }
}

// samePath is true for a path on this origin: it starts with one slash and
// holds no backslash.
function samePath(path) {
  return typeof path === "string" && path.startsWith("/") && !path.startsWith("//") && !path.includes("\\");
}

// makeApi returns api(path, opts), the only way the console talks to the
// server. opts: method (GET by default), body (sent as JSON), auth (false
// sends no key), download (true returns {blob, filename} instead of JSON).
// In read-only mode every method but GET is refused. A path off this origin
// is refused. The key goes as a bearer token, never in a URL or a cookie.
export function makeApi(fetchImpl, getKey, { readOnly = false } = {}) {
  return async function api(path, opts = {}) {
    const method = String(opts.method ?? "GET").toUpperCase();
    if (method !== "GET" && (readOnly || !WRITE_METHODS.includes(method))) {
      throw new ReadOnlyError(readOnly ? `the console is read-only: ${method} refused` : `${method} is not a method the console sends`);
    }
    const hasBody = opts.body !== undefined && opts.body !== null;
    if (hasBody && method === "GET") {
      throw new ReadOnlyError("a GET carries no body");
    }
    if (!samePath(path)) {
      throw new ReadOnlyError("the console only calls paths on this server");
    }
    const headers = { Accept: opts.download ? "*/*" : "application/json" };
    if (opts.auth !== false) {
      const key = getKey();
      if (key) headers.Authorization = "Bearer " + key;
    }
    if (hasBody) headers["Content-Type"] = "application/json";
    const res = await fetchImpl(path, {
      method,
      headers,
      body: hasBody ? JSON.stringify(opts.body) : undefined,
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      referrerPolicy: "no-referrer",
    });
    if (res.ok && opts.download) {
      const header = res.headers && typeof res.headers.get === "function" ? res.headers.get("Content-Disposition") : null;
      return { blob: await res.blob(), filename: filenameFrom(header) };
    }
    let body = null;
    if (res.status !== 204) {
      try {
        body = await res.json();
      } catch {
        body = null;
      }
    }
    if (!res.ok) {
      const detail = body && typeof body === "object" ? body.detail || body.title || "" : "";
      throw new ApiError(res.status, String(detail));
    }
    return body;
  };
}

// makeGet is makeApi in read-only mode: GET only.
export function makeGet(fetchImpl, getKey) {
  return makeApi(fetchImpl, getKey, { readOnly: true });
}

// filenameFrom reads the file name of a Content-Disposition header, keeping
// only characters safe in a file name.
export function filenameFrom(header) {
  const m = /filename="?([^";]+)"?/i.exec(String(header || ""));
  const name = m ? m[1].replace(/[^A-Za-z0-9._-]/g, "_") : "";
  return name && !/^\.+$/.test(name) ? name : "download";
}

// formatAgo turns a duration in seconds into "45 s", "12 min", "3 h 5 min",
// "2 d 4 h". Negative durations (a time in the future) read "in ...".
export function formatAgo(seconds) {
  if (seconds === null || seconds === undefined || !Number.isFinite(seconds)) return "-";
  const future = seconds < 0;
  let s = Math.round(Math.abs(seconds));
  let out;
  if (s < 60) out = `${s} s`;
  else if (s < 3600) out = `${Math.floor(s / 60)} min`;
  else if (s < 86400) {
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    out = m ? `${h} h ${m} min` : `${h} h`;
  } else {
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    out = h ? `${d} d ${h} h` : `${d} d`;
  }
  return future ? `in ${out}` : out;
}

// formatTime renders an RFC 3339 time as "2026-10-13 08:30 UTC (3 h ago)".
// null or an unparsable value reads "-" or the raw text.
export function formatTime(iso, now = Date.now()) {
  if (iso === null || iso === undefined || iso === "") return "-";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return String(iso);
  const stamp = new Date(t).toISOString().slice(0, 16).replace("T", " ") + " UTC";
  const ago = (now - t) / 1000;
  const rel = ago >= 0 ? `${formatAgo(ago)} ago` : formatAgo(ago);
  return `${stamp} (${rel})`;
}

// absTime renders an RFC 3339 time as "2026-10-13 08:30 UTC".
export function absTime(iso) {
  if (iso === null || iso === undefined || iso === "") return "-";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return String(iso);
  return new Date(t).toISOString().slice(0, 16).replace("T", " ") + " UTC";
}

// relTime is how long ago a time was, in one unit: "just now", "12 min
// ago", "3 h ago", "4 d ago", "in 5 min". Past 30 days it is the date.
export function relTime(iso, now = Date.now()) {
  if (iso === null || iso === undefined || iso === "") return "-";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return String(iso);
  const s = Math.round((now - t) / 1000);
  const a = Math.abs(s);
  if (a < 45) return "just now";
  let out;
  if (a < 3600) out = `${Math.max(1, Math.round(a / 60))} min`;
  else if (a < 86400) out = `${Math.round(a / 3600)} h`;
  else if (a < 30 * 86400) out = `${Math.round(a / 86400)} d`;
  else return new Date(t).toISOString().slice(0, 10);
  return s >= 0 ? `${out} ago` : `in ${out}`;
}

// formatWindow renders a sync window: "2026-10-16 03:33 to 04:33" within a
// day, both stamps in full otherwise.
export function formatWindow(start, end) {
  const a = absTime(start).replace(" UTC", "");
  const b = absTime(end).replace(" UTC", "");
  if (a === "-" || b === "-") return `${a} to ${b}`;
  return a.slice(0, 10) === b.slice(0, 10) ? `${a} to ${b.slice(11)}` : `${a} to ${b}`;
}

// splitFigure parts a value for a display numeral: the figure, drawn large,
// and what follows it, drawn small. "3 of 9" reads 3 and "of 9", "1.2 MB"
// reads 1.2 and "MB". A value with no figure ("Never") is a word.
export function splitFigure(value) {
  const s = String(value ?? "").trim() || "-";
  const m = /^([-+]?\d[\d,.]*)(?:\s+(.+))?$/.exec(s);
  if (m) return { figure: m[1], unit: m[2] || "", word: false };
  return { figure: s, unit: "", word: s !== "-" };
}

// formatCount groups thousands: 1234567 reads "1,234,567".
export function formatCount(n) {
  if (n === null || n === undefined || !Number.isFinite(Number(n))) return "-";
  return String(Math.round(Number(n))).replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

// humanize turns an API status into a label: "needs_reauth" reads "Needs
// reconnecting", "rate_limited" reads "Rate limited".
export function humanize(status) {
  if (status === null || status === undefined || status === "") return "-";
  const known = { needs_reauth: "Needs reconnecting", done: "Delivered", api_key: "API key" };
  if (Object.hasOwn(known, status)) return known[status];
  const s = String(status).replace(/[_.]/g, " ");
  return s.charAt(0).toUpperCase() + s.slice(1);
}

// initials are the one or two letters of a provider or tenant mark:
// "Apple Health" reads "AH", "intervals.icu" reads "I".
export function initials(name) {
  const words = String(name || "?")
    .split(/[\s_]+/)
    .filter((w) => /^[A-Za-z0-9]/.test(w));
  if (!words.length) return "?";
  return (words.length > 1 ? words[0][0] + words[1][0] : words[0][0]).toUpperCase();
}

// providerLabel names a provider from the names the server gave, else from
// its slug: "apple_health" reads "Apple health".
export function providerLabel(names, slug) {
  if (names && Object.hasOwn(names, slug)) return names[slug];
  return humanize(slug);
}

// formatBytes: 0 B, 950 B, 1.5 kB, 12 MB, 3.2 GB (powers of 1000).
export function formatBytes(n) {
  if (n === null || n === undefined || !Number.isFinite(Number(n))) return "-";
  let v = Number(n);
  const units = ["B", "kB", "MB", "GB", "TB"];
  let i = 0;
  while (v >= 1000 && i < units.length - 1) {
    v /= 1000;
    i++;
  }
  return i === 0 ? `${v} B` : `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`;
}

// Classes, used as CSS class names: ok (green), warn (amber), bad (red),
// off (grey).

// classifyFreshness maps a SourceFreshness to a class: stale, failing and
// needs_reauth are bad, syncing is warn, disconnected is off.
export function classifyFreshness(src) {
  switch (src && src.state) {
    case "fresh":
      return (src.consecutive_failures || 0) > 0 ? "warn" : "ok";
    case "syncing":
      return "warn";
    case "disconnected":
      return "off";
    case "stale":
    case "failing":
    case "needs_reauth":
      return "bad";
    default:
      return "warn";
  }
}

// classifyConnection maps a Connection status to a class.
export function classifyConnection(conn) {
  switch (conn && conn.status) {
    case "active":
      return "ok";
    case "needs_reauth":
      return "bad";
    case "disconnected":
      return "off";
    default:
      return "warn";
  }
}

// classifyWebhook: a disabled endpoint or one failing right now is bad.
export function classifyWebhook(w) {
  if (!w) return "warn";
  if (w.status === "disabled" || w.failing_since) return "bad";
  if (w.status === "active") return "ok";
  return "warn";
}

// webhookLabel names an endpoint's state: a failing one reads "Failing"
// even while it is still on, so the label agrees with its color.
export function webhookLabel(w) {
  if (!w) return "-";
  if (w.status === "disabled") return "Turned off";
  if (w.failing_since) return "Failing";
  return humanize(w.status);
}

// classifyDelivery: parked is bad, a retrying delivery is warn, done is ok.
export function classifyDelivery(d) {
  if (!d) return "warn";
  if (d.status === "parked") return "bad";
  if (d.status === "done") return "ok";
  if (d.last_error || (d.attempts || 0) > 0) return "warn";
  return "ok";
}

// classifyUser: a user being deleted is off.
export function classifyUser(u) {
  return u && u.status === "active" ? "ok" : "off";
}

// classifyProvider maps an admin provider status to a class.
export function classifyProvider(p) {
  switch (p && p.status) {
    case "active":
    case "built_in":
      return "ok";
    case "error":
      return "bad";
    default:
      // Off, or not configured: a provider nobody set up is not a fault.
      return "off";
  }
}

// providerStatusLabel is how the console names a provider status.
export function providerStatusLabel(status) {
  return (
    {
      active: "On",
      built_in: "Built in",
      off: "Off",
      not_configured: "Not configured",
      error: "Error",
    }[status] || String(status ?? "-")
  );
}

// classifyRun maps a sync run status to a class.
export function classifyRun(run) {
  switch (run && run.status) {
    case "succeeded":
      return "ok";
    case "failed":
      return "bad";
    case "retrying":
    case "rate_limited":
      return "warn";
    case "cancelled":
      return "off";
    default:
      return "ok";
  }
}

// isCloud is true for a provider users connect to or that notifies this
// server, false for a source an app uploads (always on, no connection).
export function isCloud(source) {
  const kinds = (source && source.kinds) || [];
  return kinds.includes("oauth") || kinds.includes("push");
}

// STAGE_STATES name the state of a stage on the overview.
export const STAGE_STATES = { ok: "Healthy", warn: "Check", bad: "Needs you", off: "Not set up" };

// pipelineStages turns GET /v1/pipeline into the five stages of the
// overview, in the order data flows. Each has a class, a one-line role and
// its figures; the first figure is the headline. A figure may link to the
// page that shows it.
export function pipelineStages(p) {
  const sources = p.sources || [];
  const enabled = sources.filter((s) => s.enabled && isCloud(s));
  const sum = (f) => sources.reduce((n, s) => n + f(s), 0);
  const active = sum((s) => (s.connections && s.connections.active) || 0);
  const reauth = sum((s) => (s.connections && s.connections.needs_reauth) || 0);
  const broken = sum((s) => s.broken || 0);
  const paused = sources.filter((s) => s.paused_until).length;
  const sync = p.sync || {};
  const proc = p.processing || {};
  const store = p.storage || {};
  const del = p.delivery || {};
  const users = p.users || {};
  const worst = (...cls) => (cls.includes("bad") ? "bad" : cls.includes("warn") ? "warn" : "ok");
  return [
    {
      key: "sources",
      title: "Sources",
      role: "Accounts users connect, and app uploads",
      cls: enabled.length === 0 ? "warn" : worst(reauth > 0 ? "bad" : "ok", broken > 0 ? "warn" : "ok"),
      figures: [
        { label: "Connections", value: formatCount(active) },
        { label: "Cloud providers on", value: formatCount(enabled.length), href: "#/providers" },
        { label: "Users", value: formatCount(users.active), href: "#/users" },
        { label: "Need reconnecting", value: formatCount(reauth), cls: reauth > 0 ? "bad" : null },
      ],
    },
    {
      key: "sync",
      title: "Sync",
      role: "Pulls from each provider",
      cls: worst(sync.failed_24h > 0 ? "bad" : "ok", sync.retrying > 0 || paused > 0 || sync.rate_limited > 0 ? "warn" : "ok"),
      figures: [
        { label: "Runs succeeded, 24\u00a0h", value: formatCount(sync.succeeded_24h) },
        { label: "Queued", value: formatCount((sync.queued || 0) + (sync.retrying || 0) + (sync.rate_limited || 0)) },
        { label: "Running", value: formatCount(sync.running) },
        { label: "Failed, 24\u00a0h", value: formatCount(sync.failed_24h), cls: sync.failed_24h > 0 ? "bad" : null, href: "#/jobs" },
      ],
    },
    {
      key: "processing",
      title: "Processing",
      role: "Raw files into records",
      cls: worst(proc.parked > 0 ? "bad" : "ok", proc.rejected_records > 0 ? "warn" : "ok"),
      figures: [
        { label: "Jobs waiting", value: formatCount(proc.pending) },
        { label: "Running", value: formatCount(proc.running) },
        { label: "Parked", value: formatCount(proc.parked), cls: proc.parked > 0 ? "bad" : null, href: "#/jobs" },
        { label: "Rejected records", value: formatCount(proc.rejected_records), cls: proc.rejected_records > 0 ? "warn" : null },
      ],
    },
    {
      key: "storage",
      title: "Storage",
      role: "The raw archive, kept for replay",
      cls: "ok",
      figures: [
        { label: "Raw data, 24\u00a0h", value: formatBytes(store.raw_bytes_24h) },
        { label: "Raw objects, 24\u00a0h", value: formatCount(store.raw_objects_24h) },
      ],
    },
    {
      key: "delivery",
      title: "Delivery",
      role: "Webhooks to your systems",
      cls:
        del.endpoints === 0
          ? "off"
          : worst(del.parked > 0 || del.endpoints_disabled > 0 ? "bad" : "ok", del.endpoints_failing > 0 ? "warn" : "ok"),
      figures: [
        { label: "Webhook endpoints", value: formatCount(del.endpoints), href: "#/webhooks" },
        { label: "Failing", value: formatCount(del.endpoints_failing), cls: del.endpoints_failing > 0 ? "warn" : null },
        { label: "Deliveries pending", value: formatCount(del.pending) },
        { label: "Deliveries parked", value: formatCount(del.parked), cls: del.parked > 0 ? "bad" : null },
      ],
    },
  ];
}

// attention lists what needs the operator, worst first, each with the page
// that fixes it.
export function attention(p) {
  const out = [];
  const sources = p.sources || [];
  const sync = p.sync || {};
  const proc = p.processing || {};
  const del = p.delivery || {};
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
  if (!sources.some((s) => s.enabled && isCloud(s))) {
    out.push({
      cls: "warn",
      text: "No cloud provider is on yet: turn one on so users can connect their accounts. App uploads work already.",
      href: "#/providers",
    });
  }
  for (const s of sources) {
    const reauth = (s.connections && s.connections.needs_reauth) || 0;
    if (reauth > 0) {
      out.push({ cls: "bad", text: `${plural(reauth, "user needs", "users need")} to reconnect ${s.name}.`, href: "#/users" });
    }
    if (!s.enabled && s.connections && s.connections.active > 0) {
      out.push({
        cls: "warn",
        text: `${s.name} is off but ${plural(s.connections.active, "connection is", "connections are")} kept: they sync again when it is on.`,
        href: "#/providers",
      });
    }
    if (s.paused_until) {
      out.push({ cls: "warn", text: `${s.name} asked to slow down: its pulls are paused until ${formatTime(s.paused_until)}.` });
    }
  }
  if (sync.failed_24h > 0) {
    out.push({ cls: "bad", text: `${plural(sync.failed_24h, "sync run", "sync runs")} failed in the last 24 hours.`, href: "#/jobs" });
  }
  if (proc.parked > 0) {
    out.push({ cls: "bad", text: `${plural(proc.parked, "processing job is", "processing jobs are")} parked after 5 failed attempts.`, href: "#/jobs" });
  }
  if (del.endpoints_disabled > 0) {
    out.push({ cls: "bad", text: `${plural(del.endpoints_disabled, "webhook endpoint is", "webhook endpoints are")} disabled.`, href: "#/webhooks" });
  }
  if (del.parked > 0) {
    out.push({ cls: "bad", text: `${plural(del.parked, "webhook delivery is", "webhook deliveries are")} parked.`, href: "#/webhooks" });
  }
  if (del.endpoints_failing > 0) {
    out.push({ cls: "warn", text: `${plural(del.endpoints_failing, "webhook endpoint is", "webhook endpoints are")} failing.`, href: "#/webhooks" });
  }
  if (proc.rejected_records > 0) {
    out.push({ cls: "warn", text: `${plural(proc.rejected_records, "record was", "records were")} rejected by processing (see each user's coverage).` });
  }
  const rank = { bad: 0, warn: 1 };
  return out.sort((a, b) => rank[a.cls] - rank[b.cls]);
}

// keysPatch builds the keys of a PATCH /v1/admin/providers body from the
// form: fields are [{name, secret, set, value, input, clear}] where value
// is what the API served (non-secret keys), input what the operator typed
// and clear a request to remove the key. An empty input keeps a key. Only
// changes are sent: null removes a key. It returns null when nothing changed.
export function keysPatch(fields) {
  const out = {};
  for (const f of fields) {
    const typed = String(f.input ?? "").trim();
    if (f.clear) {
      if (f.set) out[f.name] = null;
      continue;
    }
    if (typed === "") continue;
    if (!f.secret && f.set && typed === String(f.value ?? "")) continue;
    out[f.name] = typed;
  }
  return Object.keys(out).length ? out : null;
}

// confirmMatches is true when what the operator typed names what goes.
export function confirmMatches(typed, expected) {
  return typeof typed === "string" && typed.trim() !== "" && typed.trim() === String(expected);
}

// apiPath joins segments, each escaped, into a path, with an
// optional query object (null and undefined values are left out).
export function apiPath(segments, query) {
  const path = "/" + segments.map((s) => encodeURIComponent(String(s))).join("/");
  if (!query) return path;
  const qs = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== null && v !== undefined && v !== "") qs.set(k, String(v));
  }
  const s = qs.toString();
  return s ? `${path}?${s}` : path;
}

// VIEWS maps the first segment of the hash to its list view and, when an id
// follows, its detail view.
const VIEWS = {
  overview: ["overview"],
  providers: ["providers", "provider"],
  users: ["users", "user"],
  jobs: ["jobs"],
  webhooks: ["webhooks", "webhook"],
  keys: ["keys"],
  audit: ["audit"],
};

// parseRoute reads the location hash: #/overview, #/providers,
// #/providers/<slug>, #/users, #/users/<id>, #/jobs, #/webhooks,
// #/webhooks/<id>, #/keys, #/audit. Anything else is the overview.
export function parseRoute(hash) {
  const parts = String(hash || "")
    .replace(/^#\/?/, "")
    .split("/")
    .filter(Boolean)
    .map((p) => {
      try {
        return decodeURIComponent(p);
      } catch {
        return p;
      }
    });
  const [section, id] = parts;
  const views = Object.hasOwn(VIEWS, section) ? VIEWS[section] : null;
  if (!views) return { view: "overview", section: "overview" };
  if (id && views[1]) return { view: views[1], section, id };
  return { view: views[0], section };
}
