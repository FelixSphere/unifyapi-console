-- UnifyAPI per-channel upstream quota limits.
--
-- RUN THIS ONLY AFTER a release that contains the rate_limit_rpm /
-- rate_limit_tpm columns. They are created by GORM AutoMigrate when the new
-- binary starts, so seeding first fails with "column does not exist".
--
--   psql "$SQL_DSN" -f seed-channel-rate-limits.sql
--
-- No restart is needed. Unlike the options table, channel rows are read through
-- the channel cache, which reloads on its own.
--
-- WHAT THESE NUMBERS ARE, HONESTLY.
--
-- A channel limit is only truly correct when it mirrors the quota the PROVIDER
-- actually grants us on that account. We do not have those contract numbers
-- recorded anywhere yet, so these are placeholders chosen to be safe in both
-- directions: high enough that they cannot degrade routing at the scale we are
-- sizing for, low enough that a runaway loop still hits something.
--
-- Replace them per channel as the real quotas become known. That is a one-line
-- UPDATE per channel, or the admin API (rate_limit_rpm / rate_limit_tpm are
-- ordinary channel fields).
--
-- THE SIZING, for one 500-person company on this gateway:
--
--   500 seats, 30% active in a peak minute     = 150 concurrent
--   x 6 requests/min each                      = 900 req/min company-wide
--   if half of that lands on one channel       = 450 req/min
--   x 3 burst                                  = 1,350 req/min
--   -> 4,000 rpm is ~3x that
--
--   450 req/min at the measured p95 of 38,680  = 17.4M tokens/min
--   -> 60,000,000 tpm is ~3.4x that
--
-- For reference, real peaks today across 98 channels that have served traffic:
-- the busiest single channel reached 601 req/min once (a batch job) and every
-- other channel peaked at 8-16. These limits are far above normal operation and
-- are meant to catch the pathological case, not to shape ordinary load.
--
-- 0 or NULL means unlimited. Selection drops a channel only while it is over a
-- limit, and if EVERY candidate at a priority is over, selection deliberately
-- keeps them all rather than failing the request.

UPDATE channels
   SET rate_limit_rpm = 4000,
       rate_limit_tpm = 60000000
 WHERE status = 1
   AND (rate_limit_rpm IS NULL OR rate_limit_rpm = 0)
   AND (rate_limit_tpm IS NULL OR rate_limit_tpm = 0);

-- Deliberately only fills in channels that have no limit yet, so re-running
-- this never overwrites a real provider quota someone has since entered.

SELECT count(*) AS channels_limited
  FROM channels
 WHERE status = 1 AND rate_limit_rpm > 0;
