#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Weekly training load from workouts (energy + duration).

    python3 training-load.py           # the last 12 weeks
    python3 training-load.py --weeks 26

A simple weekly load score = sum of (energy_kcal + duration_min / 10) per workout.
Useful next to heart-trend.py: rising load with rising resting HR / falling HRV = fatigue.

Needs Python 3.9+ and nothing else. Recipe page: docs/recipes/training-load.md
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


def weekly_load(first, weeks):
    """Training load per 7-day window from first, oldest first; 0 for empty weeks."""
    by_week = [0.0] * weeks
    for w in records("workouts"):
        start = date.fromisoformat(w["start_date"][:10])
        if start < first:
            continue
        week_idx = (start - first).days // 7
        if week_idx >= weeks:
            continue
        energy = w.get("total_energy_burned") or 0
        duration = (w.get("duration_seconds") or 0) / 60  # minutes
        # Simple load: energy (kcal) + duration (min) / 10
        by_week[week_idx] += energy + duration / 10
    return by_week


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--weeks", type=int, default=12, help="how many weeks (default 12)")
    weeks = max(p.parse_args().weeks, 2)
    # Windows of 7 days that end today, so the last one is always "this week".
    first = date.today() - timedelta(days=7 * weeks - 1)

    load = weekly_load(first, weeks)
    if not any(load):
        sys.exit(f"no workouts since {first}: connect a watch or log a workout first")

    print(f"{'Week ending':<14}{'Load':>10}")
    for w in range(weeks):
        last = first + timedelta(weeks=w, days=6)
        print(f"{last:%a %d %b}  {load[w]:>9.0f}")

    # Compare last week vs previous 4 weeks
    before = [v for v in load[-5:-1] if v > 0]
    if load[-1] > 0 and before:
        base = sum(before) / len(before)
        d = load[-1] - base
        pct = d / base * 100
        print(f"\nLast week vs {min(4, weeks - 1)} weeks before: {d:+.0f} ({pct:+.0f}%)")


if __name__ == "__main__":
    main()