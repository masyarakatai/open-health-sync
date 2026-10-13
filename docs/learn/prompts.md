# Prompt library

Questions to paste into an AI assistant connected to your instance (see
[project 4](ask-an-ai.md)). Each one is written so the assistant reads your real data first,
shows the numbers it used, and says when there is not enough of it. Change the words in
brackets, keep the rest.

They work with any source: the assistant reads the same lists whether a ring, a band, a watch or
the Health app recorded them. None of them is medical advice, and the assistant should not give
any: a trend worth worrying about is a trend to show a doctor.

The same prompts are built into the MCP server, so an assistant that supports MCP prompts lists
them by name and asks you only for the blanks (see [MCP prompts](../mcp.md#prompts)). They also
ship as an [agent skill](../../skills/open-health-sync/SKILL.md).

## What every prompt asks of the assistant

The MCP server adds these rules to each prompt. When you paste a prompt by hand, paste them too
the first time:

```text
- Read my data with the Open Health Sync tools before you answer, and show the numbers you used.
- Compare me with my own history, not with population norms, and say how many days that history covers.
- Keep to one source per figure: never mix two devices in one column or one average, and name the source you used.
- When a figure is missing or a reading was rejected, say so. Never put a zero, an average or a guess in its place.
- Check my time tags (illness, travel, a race) before reading too much into an unusual day.
- This is for wellness, not medicine: do not diagnose or suggest treatment. If something keeps looking off, suggest I show it to a clinician.
```

## Every day

**Morning check.** Before you decide how hard to train. MCP prompt: `morning_check`.

```text
Read my last night of sleep, my resting heart rate and my HRV for this morning, and the same
figures for the 28 days before. Tell me, in three lines, how this morning compares with my own
usual range (not population norms), which figure is furthest from it, and how many days the
range is based on. If a figure is missing today, say so.
```

**Why do I feel flat?** When a day feels off and you want to see if the data agrees. MCP prompt: `why_flat`.

```text
Look at my last 7 days against the 4 weeks before: sleep hours, bedtime, resting heart rate,
HRV and training minutes. List what changed the most, with the numbers, and what stayed the
same. Do not guess at causes the data cannot show.
```

## Every week

**Weekly training review.** Your week of training against your usual week. MCP prompt: `weekly_training_review`.

```text
Summarize my last week of training: sessions by sport, total time, distance where there is
one, and the longest session. Then compare with my average week over the last 8 weeks, and
show my sleep on the nights after my two hardest days.
```

**Load and recovery.** Whether training load and recovery figures move together. MCP prompt: `load_and_recovery`.

```text
For each of the last 8 weeks, give me training minutes, average sleep, average resting heart
rate and average HRV in one table. Mark the weeks where training went up while HRV went down.
Use one HRV measure only (say which), and do not mix devices in one column.
```

**Sleep habits.** What an earlier bedtime does to your nights. MCP prompt: `sleep_habits`.

```text
Over the last 30 nights, what is my average sleep, my earliest and latest bedtime, and how
much more do I sleep on nights when I go to bed before [23:00]? Count the nights in each group.
```

## Before an event

**Taper check.** In the two weeks before a race, a long ride or a fitness competition. MCP prompt: `taper_check`.

```text
My event is on [date]. Compare the last 14 days with the 6 weeks before: training minutes per
week, sleep per night, resting heart rate and HRV. Tell me whether my load has come down and
whether my sleep and heart figures moved with it, with the numbers.
```

**Race week sleep.** Night by night, the week before an event. MCP prompt: `race_week_sleep`.

```text
Show my sleep for each of the last 7 nights: hours, bedtime, wake time, and deep and REM sleep
when my device records them. Then tell me which nights were under my 30 day average, and by
how much.
```

**After the event.** How long your figures took to come back. MCP prompt: `after_event`.

```text
My event was on [date]. Show my resting heart rate, HRV and sleep for each day from 3 days
before to today, next to my 30 day average before the event. How many days did each figure take
to come back within my usual range?
```

## Coming back

**After a break or an illness.** Your heart figures since you started again. MCP prompt: `after_break`.

```text
I stopped training from [date] to [date]. Compare my resting heart rate and HRV now with the 4
weeks before the break, week by week since I started again. Keep to what the data shows.
```

**A new habit.** Did the change you made show up? MCP prompt: `new_habit`.

```text
I started [stopping caffeine after noon / training in the morning / a new bedtime] on [date]. Compare the 3 weeks before and the 3 weeks after for sleep hours,
bedtime and HRV. Show both averages, the number of nights in each, and tell me if the difference
is small next to how much these figures vary from night to night.
```

## Your data itself

**What do you have on me?** A good first question. MCP prompt: `what_data`.

```text
What health data can you see for me? List each source, each kind of data, and the first and last
date it covers. Point out any gap of more than 3 days.
```

**Two devices side by side.** When two devices measure the same thing. MCP prompt: `compare_sources`.

```text
I have [sleep] from two sources. For the last 30 days, put both side by side, day by day, and
tell me the average difference and the days where they disagree the most.
```

## Add yours

A prompt that gave you a useful answer helps the next person. Post it in
[Show and tell](https://github.com/soma-intelligence/open-health-sync/discussions/categories/show-and-tell)
with what you used it for, or open a pull request that adds it to `internal/prompts/prompts.go`:
this page is generated from that file, with `go run ./internal/prompts/cmd/genprompts`.
