-- UnifyAPI pricing seed -- deletes, on purpose.
--
-- The pricing baseline lives in Go, in setting/ratio_setting/unifyapi_catalog.go:
-- one row per model, carrying the vendor's official list price in USD per 1M
-- tokens. Every billing ratio is derived from it at boot by InitRatioSettings.
--
-- So the database does NOT need to store the baseline, and must not. new-api
-- loads options in this order (main.go):
--
--   1. ratio_setting.InitRatioSettings()   -- seeds the maps from the catalog
--   2. model.InitOptionMap()               -- snapshots them for the admin UI
--   3. loadOptionsFromDatabase()           -- OVERWRITES from the options table
--
-- and step 3 goes through types.LoadFromJsonString, which REPLACES the whole
-- map rather than merging into it. One `ModelRatio` row therefore discards the
-- entire code baseline -- all of it, not just the models it mentions.
--
-- That is what happened here. The row on production carried 59 models with
-- hand-typed ratios, 32 of which had drifted off the vendors' list prices,
-- including claude-opus-4-8 at 0.2125 where every other Opus was 2.5 -- an
-- 11.8x underprice that no test could catch, because nothing in the codebase
-- knew what Anthropic charges.
--
-- Deleting these four rows makes the code the single source of truth:
--
--   * a price change is a reviewed commit, not an untracked UPDATE
--   * scripts/pricing-drift can verify the baseline against models.dev
--   * a model absent from the catalog has no ratio, and GetModelRatio failing
--     makes the relay refuse it -- so the catalog is also the allow-list
--
-- Customer discounts are NOT affected and are NOT stored here. They stay in
-- `GroupRatio` / `GroupGroupRatio`, which are business config and should change
-- without a deploy. See docs/PRICING-AND-DISCOUNTS.md.
--
-- Usage:
--   psql "$SQL_DSN" -f seed-pricing.sql
--   sqlite3 /data/one-api.db < seed-pricing.sql
--
-- IMPORTANT: options are cached in process memory at boot, so restart the
-- container afterwards. Until you do, the old rows are still billing.
--
-- To check what you are about to delete, run this first:
--   SELECT key, length(value) FROM options
--    WHERE key IN ('ModelRatio','CompletionRatio','CacheRatio','CreateCacheRatio');

BEGIN;

DELETE FROM options
 WHERE key IN (
   'ModelRatio',        -- input-token ratio; also the sellable-model allow-list
   'CompletionRatio',   -- output multiplier over input
   'CacheRatio',        -- cached-read multiplier
   'CreateCacheRatio'   -- cache-write multiplier
 );

COMMIT;

-- Deliberately NOT deleted:
--
--   GroupRatio, GroupGroupRatio  -- customer discounts, business config
--   UserUsableGroups, AutoGroups -- which groups a customer may buy
--   ChannelCostRatio             -- per-channel upstream cost, reconciliation only
--   ModelPrice                   -- per-call task pseudo-models (mj_*, suno_*,
--                                   video). A different namespace from the
--                                   token-priced catalog, and harmless: a model
--                                   with no ModelRatio entry is refused anyway.
--   ImageRatio, AudioRatio, AudioCompletionRatio
--                                -- token-type modifiers. Same reasoning: they
--                                   only apply to a model that already has a
--                                   catalog ratio, so a stale entry cannot
--                                   produce a charge on its own.
