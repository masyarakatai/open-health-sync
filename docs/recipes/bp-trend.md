# Blood pressure trend

Weekly blood pressure from OMRON, Withings, or manual entries. Shows systolic/diastolic means per week and trend vs prior month.

```sh
python3 recipes/bp-trend.py           # the last 12 weeks
python3 recipes/bp-trend.py --weeks 26
```

```text
Week ending     Systolic    Diastolic
Mon 24 Aug          128          82
Mon 31 Aug          126          80
Mon 07 Sep          130          83
Mon 14 Sep          127          81
Mon 21 Sep          124          79
Mon 28 Sep          129          82
Mon 05 Oct          125          80
Mon 12 Oct          126          81
Mon 19 Oct          123          78
Mon 26 Oct          124          79
Mon 02 Nov          122          78
Mon 09 Nov          125          80

Sources: systolic from omron, diastolic from omron
Last week vs 4 weeks before: systolic -2 mmHg, diastolic -2 mmHg
```

A sustained systolic ≥130 or diastolic ≥80 (per ACC/AHA) warrants clinical review. This is a trend, not a diagnosis.

## How it works

Reads `systolic_bp` and `diastolic_bp` from `GET /v1/users/{id}/daily`, the daily summaries OMRON and Withings connectors send. If both apps report the same day, the one with more days wins (never mixes devices).

## Make it yours

- Flag weeks where systolic >130 or diastolic >80.
- Plot alongside `heart-trend.py` for cardio risk picture.
- Add `pulse_pressure` (systolic - diastolic) column.
- Export to CSV for your doctor visit.