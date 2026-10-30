-- UnifyAPI per-customer request rate limits.
--
-- new-api reads these from the `options` table only; there is no environment
-- variable path. Checked in for the same reason as seed-branding.sql: branding
-- was once configured by hand in the admin UI, drifted, and silently dropped an
-- AGPL-required string. Change this file and reseed; do not hand-edit options.
--
--   psql "$SQL_DSN" -f seed-rate-limits.sql
--
-- Options are cached in process memory at boot (InitOptionMap), so RESTART the
-- container afterwards or nothing changes:
--   docker compose -f /opt/unifyapi/docker-compose.yml restart console
--
-- WHAT THIS IS FOR, and what it is not. This caps the blast radius of one
-- runaway API key. It is deliberately NOT the fix for noisy neighbours: with
-- limits this generous, a customer can still saturate a shared upstream key
-- long before hitting them. Fairness between customers needs channel-level
-- quota awareness, which is separate work.
--
-- HOW THE TWO COUNTERS DIFFER (middleware/model-rate-limit.go):
--   ModelRequestRateLimitCount        total requests, failures included.
--                                     0 disables this check.
--   ModelRequestRateLimitSuccessCount successful requests only (status < 400).
--                                     0 disables this check too, same as above.
--
-- The trap is not that 0 fails to disable it -- it does. The trap is that the
-- CODE DEFAULT IS 1000, not 0. Switching the feature on without setting this
-- value caps every customer at 1000 successful requests a minute, which is
-- below traffic we have already served. Pinned by
-- TestZeroSuccessCountMeansUnlimited and
-- TestSeededLimitsAdmitTheBusiestMinuteEverObserved.
--
-- HOW THESE NUMBERS WERE CHOSEN. Measured against production traffic on
-- 2026-09-24, 47,707 relay calls from 9 users since 2026-08-04:
--
--   per-user requests/minute   peak 2003   p99 155   p95 28   p50 4
--
-- The success limit is set to 10,000/min -- roughly 5x the highest minute any
-- real user has ever produced. The intent is that no legitimate customer ever
-- sees a 429 from this; only something genuinely runaway does. Re-measure
-- before tightening, with the query in the PR that added this file.

INSERT INTO options (key, value) VALUES
  ('ModelRequestRateLimitEnabled', 'true')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

INSERT INTO options (key, value) VALUES
  ('ModelRequestRateLimitDurationMinutes', '1')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

INSERT INTO options (key, value) VALUES
  ('ModelRequestRateLimitSuccessCount', '10000')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

INSERT INTO options (key, value) VALUES
  ('ModelRequestRateLimitCount', '20000')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Per-group overrides. The array is [totalCount, successCount] -- that order,
-- not the other one (setting.GetGroupRateLimit returns limits[0], limits[1]).
--
-- Named customer groups get triple the default. A group that is NOT listed here
-- falls back to the generous global default above, so onboarding a customer
-- never throttles them by omission -- which is the failure mode worth avoiding.
--
-- Note this limiter keys on USER id, not tenant: a customer with several logins
-- gets this allowance per login.
INSERT INTO options (key, value) VALUES
  ('ModelRequestRateLimitGroup',
   '{"Vip User":[60000,30000],"Kingdee":[60000,30000],"GenAI":[60000,30000],"Chinhin":[60000,30000],"UnifyAI":[60000,30000],"Builder_hub_2026_Sep_Batch_UnifyAPI-2":[60000,30000]}')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- ---------------------------------------------------------------------------
-- Token allowance (TPM)
-- ---------------------------------------------------------------------------
--
-- Request counts are the wrong shape for LLM load: one call carrying a 200k
-- context and one carrying "hello" are identical to the counters above, while
-- costing wildly different amounts of upstream capacity and of our money. This
-- adds the dimension that tracks spend.
--
-- Same window as the request limiter (ModelRequestRateLimitDurationMinutes), so
-- an operator reasons about one window rather than two. 0 means unlimited.
--
-- Enforcement is "check the window so far, then admit", because neither the
-- prompt nor the completion length is known before the upstream answers. A
-- customer can therefore exceed the allowance by the single request that
-- crosses the line, and is refused from the next one. That is inherent to token
-- limiting, not a shortcut.
--
-- Measured against production on 2026-09-24, same 47,707 calls:
--   per-user tokens/minute   peak 2,950,198   p99 1,034,708   p95 426,414
--
-- SIZED FOR ONE KEY SERVING A 500-PERSON COMPANY, which is the shape that
-- matters: a customer gets an API key and puts it in their internal tooling, so
-- one key fronts the whole company. (The limiter keys on users.id, so a customer
-- with several logins gets this allowance per login -- a separate wrinkle.)
--
--   500 seats, 30% active in a peak minute      = 150 concurrent
--   x 6 requests/min each (agentic tooling)     = 900 req/min steady peak
--   x 900 at the MEASURED p95 of 38,680 tokens  = 34.8M tokens/min
--
-- So 70,000,000/min, about 2x that. The earlier 15,000,000 was set against
-- today's 9-user traffic and WOULD HAVE BOUND at 500 seats on a heavy-context
-- workload -- the arithmetic, not the round number, is what changed it.
--
-- Using the measured average of 6,349 tokens instead of p95 gives 5.7M/min, so
-- this is ~12x for ordinary traffic and ~2x for the worst realistic mix.

INSERT INTO options (key, value) VALUES
  ('ModelRequestTokenLimitCount', '70000000')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Per-group overrides: group name -> tokens per window. A flat int, not a pair,
-- because there is only one token counter. An unlisted group inherits the
-- generous global default above.
INSERT INTO options (key, value) VALUES
  ('ModelRequestTokenLimitGroup',
   '{"Vip User":140000000,"Kingdee":140000000,"GenAI":140000000,"Chinhin":140000000,"UnifyAI":140000000,"Builder_hub_2026_Sep_Batch_UnifyAPI-2":140000000}')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;
