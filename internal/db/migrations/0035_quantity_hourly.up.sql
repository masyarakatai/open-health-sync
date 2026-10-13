-- SPDX-License-Identifier: AGPL-3.0-only
-- Hourly statistics of quantity samples per user, type, provider, source and
-- unit (source as the ETL's daily sums name it: a phone and a watch both
-- counting steps stay apart, so nothing is counted twice), with the rank
-- keys of the merged view (the source slug, and whether a watch recorded it), a
-- TimescaleDB continuous aggregate behind GET /v1/users/{id}/samples/stats.
-- A day in the caller's time zone is a sum of hours, so one aggregate serves
-- hourly and daily ranges in any zone with a whole-hour offset; the API reads
-- the raw samples otherwise, and on builds without continuous aggregates.
--
-- Real time: hours the policy has not materialized yet are computed from the
-- samples at query time. Late data (an imported history, a provider deletion)
-- invalidates the hours it touches, and the next policy run, every 5 minutes,
-- recomputes only those: the policy covers the whole history (start_offset
-- NULL) so an import of last year is counted too.
DO $$
BEGIN
    IF current_setting('timescaledb.license', true) = 'timescale' THEN
        CREATE MATERIALIZED VIEW ohs_data.quantity_hourly
        WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
        SELECT tenant_id, user_id, quantity_type, provider,
               COALESCE(NULLIF(source_bundle_id, ''), NULLIF(source_name, ''), NULLIF(device_id, ''), '') AS source,
               coalesce(source_slug, provider) AS slug,
               CASE WHEN (coalesce(product_type, '') || ' ' || coalesce(device_model, '') || ' ' ||
                          coalesce(source_name, '')) ILIKE '%watch%' THEN 0 ELSE 1 END AS worn,
               unit,
               time_bucket(INTERVAL '1 hour', start_date) AS bucket,
               count(*) AS n, sum(value) AS total, min(value) AS low, max(value) AS high
        FROM ohs_data.quantity_samples
        GROUP BY tenant_id, user_id, quantity_type, provider, source, slug, worn, unit, bucket
        WITH NO DATA;
        CREATE INDEX quantity_hourly_user_type
            ON ohs_data.quantity_hourly (tenant_id, user_id, quantity_type, bucket);
        PERFORM add_continuous_aggregate_policy('ohs_data.quantity_hourly',
            start_offset => NULL, end_offset => INTERVAL '1 hour',
            schedule_interval => INTERVAL '5 minutes');
    END IF;
END $$;
