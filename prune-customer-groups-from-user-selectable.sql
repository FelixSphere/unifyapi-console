-- Reduce UserUsableGroups to the groups any user may genuinely select.
--
-- UserUsableGroups is upstream's list of groups ANY user may pick for a token
-- or in the playground, and GetPricing publishes it -- with each group's ratio
-- -- on /api/pricing, which needs no authentication. Until 2026-09-22
-- provisioning added every customer's group to it, so:
--
--   * anyone on the internet could enumerate the customer list and their
--     pricing ratios;
--   * an ordinary user could create an API token bound to another customer's
--     group, which is the group the relay prices the request with.
--
-- OPERATOR DECISION, 2026-09-22: every named pricing group on this deployment
-- is a customer -- Chinhin, GenAI, UnifyAI, Vip User included, not only the
-- ones provisioning created. `default` is the only group a user may select.
-- That is why this keeps an explicit allowlist rather than subtracting the
-- customer registry: a hand-created customer group is not in that registry and
-- would survive the subtraction.
--
-- Safe to re-run. A customer's OWN members are unaffected: service
-- GetUserUsableGroups always adds the caller's own group back, so nobody loses
-- access to the group they belong to.
--
-- Postgres (production). Read the three SELECTs, then swap ROLLBACK for COMMIT.
-- Options are cached in memory and resynced on a timer, so the running console
-- picks this up within about a minute; restart if you want it at once.

BEGIN;

-- 1. Snapshot, so this is reversible by hand.
SELECT key, value AS value_before
FROM options
WHERE key = 'UserUsableGroups';

-- 2. What this removes. Every one of these stops being selectable by users who
--    do not already belong to it.
SELECT jsonb_object_keys(value::jsonb) AS group_removed
FROM options
WHERE key = 'UserUsableGroups'
  AND jsonb_object_keys(value::jsonb) <> 'default'
ORDER BY 1;

-- 3. Keep only `default`, preserving whatever label it already carries.
UPDATE options
SET value = COALESCE(
        jsonb_build_object('default', value::jsonb -> 'default')::text,
        '{"default":""}'
    )
WHERE key = 'UserUsableGroups'
  AND value::jsonb ? 'default';

-- If `default` was somehow absent, put it back rather than leaving an empty
-- allowlist: filterPricingByUsableGroups returns NO models for an empty one.
UPDATE options
SET value = '{"default":""}'
WHERE key = 'UserUsableGroups'
  AND NOT (value::jsonb ? 'default');

-- 4. What remains. Expect exactly one row: default.
SELECT jsonb_object_keys(value::jsonb) AS still_selectable
FROM options WHERE key = 'UserUsableGroups'
ORDER BY 1;

-- COMMIT;   -- uncomment once the output above looks right
ROLLBACK;
