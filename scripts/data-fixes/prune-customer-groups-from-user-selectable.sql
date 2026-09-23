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
--   "Vip User": ""                                                                     <- a tier we sell, also not public
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
  AND k <> 'default'
ORDER BY 1;

-- 2b. MANDATORY PRE-FLIGHT. Read this before COMMIT.
--
-- middleware/auth.go re-checks a token's group against GetUserUsableGroups on
-- EVERY request, not just when the token is created. That is what makes this
-- fix retroactive -- a token already bound to another customer's group stops
-- working the moment this lands, with no token cleanup needed.
--
-- It is also the blast radius. Any token bound to a group removed below starts
-- returning 403 "无权访问 X 分组" on its next request. Whether that is the
-- vulnerability closing or an outage depends on one thing: does the token's
-- owner belong to that group?
--
--   owner_in_group = true   -> keeps working. GetUserUsableGroups always adds
--                             the caller's own group back.
--   owner_in_group = false  -> WILL 403. Either this is the escalation being
--                             revoked (intended), or it is a multi-group
--                             arrangement somebody set up on purpose (an
--                             outage). Decide per row BEFORE committing.
--
-- An empty result means no token is affected and the prune is inert for
-- traffic.
SELECT t."group"                AS token_group,
       u."group"                AS owner_group,
       (t."group" = u."group")  AS owner_in_group,
       count(*)                 AS tokens,
       min(u.username)          AS example_owner
FROM tokens t
JOIN users u ON u.id = t.user_id
WHERE t."group" IS NOT NULL
  AND t."group" NOT IN ('', 'auto', 'default')
  AND t.status = 1
GROUP BY t."group", u."group"
ORDER BY owner_in_group, tokens DESC;

-- 3. Keep only the groups that are meant to be public.
--
--    Operator, 2026-09-22: `default` is the ONLY public group. `Vip User` is
--    not one -- it is a tier we sell, so an unrelated user must not be able to
--    pick it any more than they may pick a customer's.
--
--    A group removed here is not deleted and nobody loses access to their own:
--    GroupRatio still prices it, the admin editor still lists it, and
--    service.GetUserUsableGroups always adds the caller's own group back. What
--    goes is the blanket offer of it to everybody else.
UPDATE options
SET value = (
        SELECT COALESCE(jsonb_object_agg(k, v), '{}'::jsonb)::text
        FROM jsonb_each(value::jsonb) AS e(k, v)
        WHERE k = 'default'
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
