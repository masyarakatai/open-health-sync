# Glucose around meals

Shows glucose readings aligned with meal times and carbohydrate intake. Useful for
seeing how meals affect your glucose, especially with CGM data (Dexcom, FreeStyle Libre).

```sh
python3 recipes/glucose-meals.py           # the last 14 days
python3 recipes/glucose-meals.py --days 30
```

```text
Meal time        Glucose    Carbs Δ from prev
10-01 08:00       92 mg/dL    45g
10-01 13:00      142 mg/dL    65g          +50.0
10-01 19:00      128 mg/dL    55g          -14.0
...

Readings: 42  Mean: 118.3  Range: 82.0–165.0
Above 180 mg/dL: 2
Below 70 mg/dL: 0
```

Requires CGM (Dexcom, FreeStyle Libre) for glucose and nutrition logging for meals.
Uses standard meal windows (8:00, 13:00, 19:00) ± 2 hours.

## How it works

Reads `glucose` samples from `GET /v1/users/{id}/samples?type=glucose` and
`nutrition` records from `GET /v1/users/{id}/nutrition`. For each glucose reading,
finds the nearest preceding meal window (08:00, 13:00, 19:00 ± 2h) with logged carbs
and shows the glucose value, carb grams, and change from previous meal.

## Make it yours

- Add post-prandial targets (e.g., <140 mg/dL at 1h, <120 mg/dL at 2h post-meal).
- Plot glucose curves for each meal type (breakfast/lunch/dinner).
- Export to CSV for your endocrinologist.
- Add insulin dose correlation if you log insulin.