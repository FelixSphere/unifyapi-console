-- Reduce UserUsableGroups to the groups that ANY user is meant to select.
--
-- UserUsableGroups is upstream's list of groups any user may pick. Until
-- 2026-09-22 provisioning added each customer's group to it, so:
--
--   * GET /api/pricing -- which needs no authentication -- published every
--     customer's NAME and their group ratio to anyone on the internet;
--   * an ordinary user could create an API token bound to another customer's
--     group, which is the group the relay prices the request with;
--   * the playground's Model Group picker listed every customer by name.
--
-- WHY THIS IS AN ALLOWLIST AND NOT "DELETE THE CUSTOMERS".
--
-- The obvious script removes the groups found in partnership_customers. On
-- this production database that is not enough, and the difference is visible
-- from outside: provisioning writes the label equal to the group name, so the
-- public payload shows which entries it wrote.
--
--   "Builder_hub_2026_Sep_Batch_UnifyAPI-2": "Builder_hub_2026_Sep_Batch_UnifyAPI-2"   <- provisioned
--   "Kingdee": "Kingdee"                                                               <- provisioned
--   "Chinhin": ""   "GenAI": ""   "UnifyAI": ""                                        <- added BY HAND
--
-- Three of the five customer names carry no registry row, so a registry-driven
-- delete leaves them published, and so does the code filter, which reads the
-- same registry. Listing what stays is the only formulation that cannot miss a
-- customer nobody registered.
--
-- Operator rule, 2026-09-22: customer data is isolated unless there is a
-- public necessity. So: name the public ones, remove the rest.
--
-- A customer's OWN members are unaffected either way -- service
-- GetUserUsableGroups always adds the caller's own group back.
--
-- Safe to re-run. Postgres (production). Read the three SELECTs before COMMIT.

BEGIN;

-- 1. Snapshot, so this is reversible by hand.
SELECT key, value AS value_before
FROM options
WHERE key = 'UserUsableGroups';

-- 2. What this removes. Every row here stops being publicly selectable.
--    `is_registered_customer` shows which ones a registry-driven delete would
--    have caught -- the false rows are the reason this is an allowlist.
SELECT k AS group_removed,
       EXISTS (SELECT 1 FROM partnership_customers c WHERE c."group" = k) AS is_registered_customer
FROM options,
     LATERAL jsonb_object_keys(value::jsonb) AS k
WHERE key = 'UserUsableGroups'
  AND k NOT IN ('default', 'Vip User')
ORDER BY 1;

-- 3. Keep only the groups that are meant to be public.
--    EDIT THIS LIST if a tier is missing from it; anything not named here
--    stops being selectable by users who do not already belong to it.
UPDATE options
SET value = (
        SELECT COALESCE(jsonb_object_agg(k, v), '{}'::jsonb)::text
        FROM jsonb_each(value::jsonb) AS e(k, v)
        WHERE k IN ('default', 'Vip User')
    )
WHERE key = 'UserUsableGroups';

-- 3b. `default` must survive even if it was missing to begin with. An empty
--     UserUsableGroups is not merely strict: GetPricing filters the MODEL LIST
--     by the caller's usable groups, so an anonymous visitor with none would
--     be served an empty Model Square. Being too private here breaks the
--     public catalogue, which is why this runs as its own statement rather
--     than being assumed.
UPDATE options
SET value = COALESCE(
        (SELECT jsonb_insert(value::jsonb, '{default}', '""'::jsonb)::text),
        '{"default":""}'
    )
WHERE key = 'UserUsableGroups'
  AND NOT (value::jsonb ? 'default');

-- 4. What remains. Check by eye that no entry is a customer name.
SELECT jsonb_object_keys(value::jsonb) AS still_selectable
FROM options WHERE key = 'UserUsableGroups'
ORDER BY 1;

-- Options are read into memory once at boot, so the console must be restarted
-- (or the option re-saved through the admin UI) for this to take effect.

-- COMMIT;   -- uncomment once the output above looks right
ROLLBACK;
