# Spec: self reports

Status: draft for review (2026-10-14). Guide: [self-reports.md](../self-reports.md).

## Problem

Sensors say what the body did. They do not say how the person felt. Coaches ask athletes for a
morning check-in and a rating of each session; studies collect a daily wellbeing item next to
the wearable data; anyone who builds a model on top of wearable data needs these answers as
labels. Today an app that collects them has nowhere standard to put them: it keeps its own
table, joins it by hand with the synced data, and ships no FHIR for it.

Open Health Sync already stores what the Health app calls State of Mind and the mood an Oura
user attaches to a moment. What is missing is a way for any app, on any platform, to record a
person's own ratings next to their synced data, with the same provenance, consent, erasure and
standard outputs.

## Scope

1. Eight quantity types, each a whole number on a fixed scale, stored like any other sample:

   | Type | Scale | Lowest | Highest | Consent category |
   |---|---|---|---|---|
   | `reported_feeling` | 1 to 5 | very bad | very good | mental |
   | `reported_mood` | 1 to 5 | very low | very good | mental |
   | `reported_stress` | 1 to 5 | none | very high | mental |
   | `reported_energy` | 1 to 5 | exhausted | full of energy | mental |
   | `reported_sleep_quality` | 1 to 5 | very poor | very good | sleep |
   | `reported_muscle_soreness` | 1 to 5 | none | very sore | activity |
   | `reported_exertion` | 0 to 10 | rest | maximal | activity |
   | `reported_pain` | 0 to 10 | no pain | worst imaginable | other |

   The unit is `score` (UCUM `{score}`). `reported_pain` carries LOINC 72514-3 (Pain severity,
   0-10 verbal numeric rating, reported). The others have no LOINC code that matches a generic
   scale, so they carry the Open Health Sync code alone. No licensed questionnaire wording is
   used: the scales are plain numbers with the two anchors above.
2. A device connector `self_report`. Its records are quantity samples with provider and source
   `self_report`, `was_user_entered` true, and the report id as `external_id`.
3. `POST /v1/users/{id}/self-reports`: a JSON body of 1 to 100 reports, each with `kind`,
   `value`, `start_at`, an optional `end_at` (a rated session) and an optional client `id`
   (UUID; generated when absent). It accepts a tenant API key, or a user token with the `upload`
   scope for its own user, honours `Idempotency-Key`, and answers 202 with the stored reports
   and their ids. The body goes to the raw store as one NDJSON part like any upload, and the
   ETL maps it, so consent, the merged view, erasure, retention and re-processing behave as for
   every other source.
4. Corrections: a report sent again with the same `id` replaces the stored value (latest wins),
   and a moved `start_at` replaces the old sample.
5. Reads need nothing new: `/samples?type=reported_feeling`, `/samples/stats` (daily mean of a
   rating), FHIR Observations with category `survey`, CSV and Parquet exports, the user archive,
   and the MCP tools `list_quantity_types`, `get_samples` and `get_sample_stats`.

## Out of scope

- Deleting one report. A report can be corrected; erasing every report of a user is
  `DELETE /v1/users/{id}/data/self_report`. A per-record delete is a separate change for every
  source.
- Free-text notes on a report. Time tags carry context in words.
- Writing reports over MCP. The MCP server stays read-only; an agent that asks the person how
  they feel posts through the REST API with its own credentials.
- Open mHealth output. No IEEE 1752 / Open mHealth schema describes a generic rating scale.
- Licensed instruments (PROMIS, Borg scales and their wording). An app can still store the
  number such an instrument produces under the matching kind if the scale is the same.

## Standards

- FHIR R4: an Observation per report, `category` `survey` (HL7 observation-category), the
  Open Health Sync code, LOINC 72514-3 for pain, `valueQuantity` with UCUM `{score}`,
  `effectiveDateTime` or `effectivePeriod`, and a Provenance whose agent is the person.
- UCUM: `{score}` is an annotation, a dimensionless unit of 1.

## Decisions to confirm

- Kinds and scales above. Adding a kind later is cheap (a migration row, a range rule, a
  consent rule); changing a scale is a breaking change.
- `reported_pain` filed under the `other` consent category, since no category describes pain.
