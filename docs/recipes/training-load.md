# Training load

Weekly training load from your workouts. A simple score (energy in kcal + duration in minutes ÷ 10) per workout, summed per week.

```sh
python3 recipes/training-load.py           # the last 12 weeks
python3 recipes/training-load.py --weeks 26
```

```text
Week ending     Load
Mon 24 Aug        1240
Mon 31 Aug        1380
Mon 07 Sep        1100
Mon 14 Sep        1520
Mon 21 Sep        1290
Mon 28 Sep        1450
Mon 05 Oct        1310
Mon 12 Oct        1270
Mon 19 Oct        1390
Mon 26 Oct        1200
Mon 02 Nov        1180
Mon 09 Nov        1250

Last week vs 4 weeks before: +50 (+4%)
```

A rising training load with a rising resting heart rate or falling HRV (see `heart-trend.py`) often goes with a hard training block or incomplete recovery. It is a trend, not a diagnosis.

## How it works

It reads `GET /v1/users/{id}/workouts`, takes each workout's `total_energy_burned` (kcal) and `duration_seconds`, and sums `energy + duration_min / 10` per 7-day window. Workouts without energy or duration are ignored.

## Make it yours

- Plot it next to `heart-trend.py` output for a training/recovery dashboard.
- Break down by `workout_activity_type` (run, ride, swim, strength).
- Add TSS/TRIMP if you have power/pace data.