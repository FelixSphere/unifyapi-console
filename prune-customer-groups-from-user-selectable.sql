-- Remove every provisioned customer's pricing group from UserUsableGroups.
--
-- UserUsableGroups is upstream's list of groups ANY user may select. Until
-- 2026-09-22 provisioning added each customer's group to it, so:
--
--   * GET /api/pricing -- which needs no authentication -- published every
--     customer's NAME and their group ratio to anyone on the internet;
--   * an ordinary user could create an API token bound to another customer's
--     group, which is the group the relay prices the request with.
--
-- The code fix stops new provisioning from adding them and filters them out on
-- read, so this script is the tidy-up of rows already written, not the security
-- fix on its own.
--
-- Safe to re-run. A customer's OWN members are unaffected: service
-- GetUserUsableGroups always adds the caller's own group back.
--
-- Postgres only (production). Run inside a transaction and read the two SELECTs
-- before COMMIT.

BEGIN;

-- 1. Snapshot, so this is reversible by hand.
SELECT key, value AS value_before
FROM options
WHERE key = 'UserUsableGroups';

-- 2. Which keys this will remove, and which customers they belong to.
SELECT c.name AS customer, c."group" AS group_removed
FROM partnership_customers c
WHERE c."group" IN (
    SELECT jsonb_object_keys(value::jsonb)
    FROM options WHERE key = 'UserUsableGroups'
)
ORDER BY c.name;

-- 3. Remove them.
UPDATE options
SET value = (value::jsonb - ARRAY(
        SELECT DISTINCT "group" FROM partnership_customers WHERE "group" <> ''
    ))::text
WHERE key = 'UserUsableGroups';

-- 4. What remains. Every entry left is a group ANY user may select -- check by
--    eye that none of them is a customer name. Groups created by hand rather
--    than by provisioning are NOT in partnership_customers, so step 3 cannot
--    find them; remove those with a follow-up if any are customers.
SELECT jsonb_object_keys(value::jsonb) AS still_selectable
FROM options WHERE key = 'UserUsableGroups'
ORDER BY 1;

-- COMMIT;   -- uncomment once the output above looks right
ROLLBACK;
