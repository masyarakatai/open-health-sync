-- SPDX-License-Identifier: AGPL-3.0-only
DELETE FROM ohs_data.quantity_codes WHERE canonical_code IN (
    'reported_feeling',
    'reported_mood',
    'reported_stress',
    'reported_energy',
    'reported_sleep_quality',
    'reported_muscle_soreness',
    'reported_exertion',
    'reported_pain'
);
