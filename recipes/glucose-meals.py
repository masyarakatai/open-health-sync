#!/usr/bin/env python3
# SPDX-License-Identifier: AGPL-3.0-only
"""Glucose around meals with nutrition data.

    python3 glucose-meals.py           # the last 14 days
    python3 glucose-meals.py --days 30

Shows glucose trends aligned with meal times and carb intake.
Reads `glucose` samples and `nutrition` records (carb grams).
Needs Python 3.9+ and nothing else. Recipe page: docs/recipes/glucose-meals.md
"""

import argparse
from collections import defaultdict
from datetime import date, datetime, timedelta
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


def nutrition_by_date(start, end=None):
    """Carb grams per date from nutrition records."""
    by_date = defaultdict(float)
    for n in records("nutrition", start=start.isoformat(), end=end and end.isoformat()):
        carbs = n.get("carbohydrates_grams")
        if carbs is not None:
            d = date.fromisoformat(n["date"][:10])
            by_date[d] += carbs
    return by_date


def glucose_by_datetime(start, end=None):
    """Glucose readings grouped by datetime."""
    by_dt = defaultdict(list)
    for g in records("samples", type="glucose", start=start.isoformat(), end=end and end.isoformat()):
        dt = datetime.fromisoformat(g["start_date"][:19])
        by_dt[dt].append(g["value"])
    return by_dt


def glucose_around_meals(glucose, nutrition, start, end=None):
    """Glucose readings within 2 hours after meals with carb data."""
    results = []
    for dt in sorted(glucose):
        if dt < start:
            continue
        if end and dt > end:
            continue
        # Find meal within 30 min before this glucose reading
        meal_carbs = None
        meal_time = None
        for nd, carbs in nutrition.items():
            # Assume meals at 8:00, 13:00, 19:00 local time
            for meal_hour in (8, 13, 19):
                meal_dt = datetime.combine(nd, datetime.min.time().replace(hour=meal_hour))
                if meal_dt <= dt <= meal_dt + timedelta(hours=2):
                    if carbs > 0:
                        meal_carbs = carbs
                        meal_time = meal_dt
                        break
            if meal_carbs is not None:
                break
        if meal_carbs is not None:
            avg_glucose = sum(glucose[dt]) / len(glucose[dt])
            results.append((dt, avg_glucose, meal_time, meal_carbs))
    return results


def main():
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--days", type=int, default=14, help="how many days (default 14)")
    days = max(p.parse_args().days, 1)
    first = date.today() - timedelta(days=days - 1)

    nutrition = nutrition_by_date(first)
    glucose = glucose_by_datetime(first)

    if not nutrition and not glucose:
        sys.exit(f"no glucose or nutrition data since {first}: connect Dexcom, log meals first")

    results = glucose_around_meals(glucose, nutrition, datetime.combine(first, datetime.min.time()))

    if not results:
        print("No glucose readings found within 2h after logged meals.")
        print("Log meals (nutrition) and wear CGM for aligned data.")
        return

    print(f"{'Meal time':<16}{'Glucose':>10}{'Carbs':>8}{'Δ from prev':>12}")
    prev = None
    for dt, glucose_val, meal_time, carbs in results:
        delta = ""
        if prev is not None:
            delta = f"{glucose_val - prev:+.1f}"
        print(f"{meal_time:%m-%d %H:%M}  {glucose_val:>7.1f} mg/dL  {carbs:>6.0f}g  {delta:>12}")
        prev = glucose_val

    # Summary stats
    vals = [g for _, g, _, _ in results]
    print(f"\nReadings: {len(vals)}  Mean: {sum(vals)/len(vals):.1f}  Range: {min(vals):.1f}–{max(vals):.1f}")
    high = sum(1 for v in vals if v > 180)
    low = sum(1 for v in vals if v < 70)
    if high:
        print(f"Above 180 mg/dL: {high}")
    if low:
        print(f"Below 70 mg/dL: {low}")


if __name__ == "__main__":
    main()