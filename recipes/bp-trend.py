#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Blood pressure trend from OMRON/Withings readings, weekly summary.

    python3 bp-trend.py              # the last 12 weeks
    python3 bp-trend.py --weeks 26

Weekly systolic/diastolic means with trend vs prior 4 weeks.
Reads `systolic_bp` and `diastolic_bp` daily summaries (OMRON/Withings).

Needs Python 3.9+ and nothing else. Recipe page: docs/recipes/bp-trend.md
"""

import argparse
from datetime import date, timedelta
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


# Connection: OHS_URL, OHS_KEY and OHS_USER from the environment, or from the
# file `ohs admin personal setup` writes (~/.ohs.env, or the path in OHS_ENV_FILE).
def settings():
    path = os.environ.get("OHS_ENV_FILE") or os.path.expanduser("~/.ohs.env")
    if os.path.isfile(path):
        with open(path, encoding="utf-8") as f:
            for line in f:
                name, sep, value = line.strip().partition("=")
                if sep and not name.startswith("#"):
                    os.environ.setdefault(name.strip(), value.strip())
    missing = [n for n in ("OHS_URL", "OHS_KEY", "OHS_USER") if not os.environ.get(n)]
    if missing:
        sys.exit(f"missing {', '.join(missing)}: run `ohs admin personal setup > ~/.ohs.env` first")
    return os.environ["OHS_URL"].rstrip("/"), os.environ["OHS_KEY"], os.environ["OHS_USER"]


URL, KEY, USER = settings()


def call(method, path, body=None, raw=False, **params):
    query = urllib.parse.urlencode({k: v for k, v in params.items() if v is not None})
    req = urllib.request.Request(
        URL + path + ("?" + query if query else ""),
        method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Authorization": "Bearer " + KEY, "Content-Type": "application/json"},
    )
    try:
        resp = urllib.request.urlopen(req, timeout=120)
    except urllib.error.HTTPError as e:
        try:
            detail = json.load(e).get("detail", "")
        except ValueError:
            detail = ""
        sys.exit(f"{method} {path}: {e.code} {detail}".rstrip())
    except urllib.error.URLError as e:
        sys.exit(f"cannot reach {URL}: {e.reason}")
    if raw:
        return resp
    with resp:
        return json.load(resp)


def records(resource, **params):
    """Every record of one of the user's lists, page after page."""
    params.setdefault("view", "all")
    cursor = None
    while True:
        page = call("GET", f"/v1/users/{USER}/{resource}", limit=1000, cursor=cursor, **params)
        yield from page["data"]
        cursor = page.get("next_cursor")
        if not cursor:
            return


def daily(field, start, end=None):
    """One value of a daily figure per date ({date: value}), and the app it came from."""
    rows = [r for r in records("daily", start=start.isoformat(), end=end and end.isoformat())
            if r.get(field) is not None]
    if not rows:
        return {}, None
    from collections import Counter
    app = Counter(r["provider"] for r in rows).most_common(1)[0][0]
    return {date.fromisoformat(r["date"]): r[field] for r in rows if r["provider"] == app}, app


def weekly(values, first, weeks):
    """Mean per 7 day window from first, oldest first; None for empty weeks."""
    out = []
    for w in range(weeks):
        ws = first + timedelta(weeks=w)
        vs = [v for d, v in values.items() if ws <= d < ws + timedelta(days=7)]
        out.append(sum(vs) / len(vs) if vs else None)
    return out


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--weeks", type=int, default=12, help="how many weeks (default 12)")
    weeks = max(p.parse_args().weeks, 2)
    first = date.today() - timedelta(days=7 * weeks - 1)

    sys_bp, sys_app = daily("systolic_bp", first)
    dia_bp, dia_app = daily("diastolic_bp", first)
    if not sys_bp and not dia_bp:
        sys.exit(f"no blood pressure readings since {first}: connect OMRON, Withings or add manually first")

    sys_w = weekly(sys_bp, first, weeks)
    dia_w = weekly(dia_bp, first, weeks)
    fmt = lambda v: f"{v:.0f} mmHg" if v is not None else "-"
    print(f"{'Week ending':<14}{'Systolic':>12}{'Diastolic':>12}")
    for w in range(weeks):
        last = first + timedelta(weeks=w, days=6)
        print(f"{last:%a %d %b}  {fmt(sys_w[w]):>12}{fmt(dia_w[w]):>12}")
    print(f"\nSources: systolic from {sys_app or '-'}, diastolic from {dia_app or '-'}")

    def change(series):
        before = [v for v in series[-5:-1] if v is not None]
        if series[-1] is None or not before:
            return None
        base = sum(before) / len(before)
        d = series[-1] - base
        return f"{d:+.0f} mmHg"

    parts = [f"{name} {c}" for name, c in (("systolic", change(sys_w)), ("diastolic", change(dia_w))) if c]
    if parts:
        print(f"Last week vs {min(4, weeks - 1)} weeks before: {', '.join(parts)}")


if __name__ == "__main__":
    main()