-- SPDX-License-Identifier: AGPL-3.0-only
--
-- Codes of the self report types declared in internal/model: a person's own ratings, whole
-- numbers on a fixed scale stored in the unit "score" (UCUM {score}, an annotation of 1).
-- Pain on a 0 to 10 numeric rating scale has a LOINC code; the other scales have none that
-- fits, so they carry the Open Health Sync code alone.
INSERT INTO ohs_data.quantity_codes
    (canonical_code, loinc_code, snomed_code, ucum_unit, storage_unit, value_type, hot_table, promoted)
VALUES
    ('reported_feeling', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_mood', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_stress', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_energy', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_sleep_quality', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_muscle_soreness', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_exertion', NULL, NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true),
    ('reported_pain', '72514-3', NULL, '{score}', 'score', 'num', 'ohs_data.quantity_samples', true)
ON CONFLICT (canonical_code) DO UPDATE SET
    loinc_code   = EXCLUDED.loinc_code,
    ucum_unit    = EXCLUDED.ucum_unit,
    storage_unit = EXCLUDED.storage_unit,
    value_type   = EXCLUDED.value_type,
    hot_table    = EXCLUDED.hot_table,
    promoted     = EXCLUDED.promoted,
    updated_at   = now();
