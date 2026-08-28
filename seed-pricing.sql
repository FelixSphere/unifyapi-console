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
-- That is what happened here. Measured on production 2026-08-28, the live
-- `ModelRatio` row holds **2,877 keys** -- it is upstream's full dump, not a
-- table of the 59 models we sell. Of the 56 comparable models that are actually
-- served, 32 had drifted off the vendors' list prices, including
-- claude-opus-4-8 at 0.2125 where every other Opus reads 2.5 -- an 11.8x
-- underprice that no test could catch, because nothing in the codebase knew
-- what Anthropic charges. (The same row carries claude-opus-4-8-medium at 2.5:
-- one model, two prices, differing by a suffix and a decimal point.)
--
-- SO READ THE BLAST RADIUS CORRECTLY: this DELETE narrows the sellable set from
-- 2,877 models to 59, not from 59 to 59. Every model not in the catalog becomes
-- unsellable -- which is the intent, and is why step 2 of the runbook below is
-- not optional.
--
-- Deleting these four rows makes the code the single source of truth:
--
--   * a price change is a reviewed commit, not an untracked UPDATE
--   * scripts/pricing-drift can verify the baseline against models.dev
--   * a model absent from the catalog has no ratio, and GetModelRatio failing
--     makes the relay refuse it -- so the catalog is also the allow-list
--
-- Customer discounts are NOT affected and are NOT stored here. They live in
-- `ModelDiscount` (per model) and `GroupRatio` / `GroupGroupRatio` (per group),
-- none of which this file touches. See docs/PRICING-AND-DISCOUNTS.md.
--
-- Note that on production neither `ModelDiscount` nor `ChannelCostRatio` exists
-- yet, so "discounts survive the seed" is true but empty: there is nothing to
-- survive until someone writes them. A price-neutral rollout requires INSERTing
-- a `ModelDiscount` row, not merely preserving one.
--
-- BEFORE RUNNING THIS, capture the rows you are deleting WITH THEIR VALUES.
-- `SELECT key, length(value)` is a fingerprint, not a backup: once this runs,
-- 2,877 hand-tuned ratios exist nowhere else -- not in git, not in the catalog.
--
--   \copy (SELECT key, value FROM options WHERE key IN
--     ('ModelRatio','CompletionRatio','CacheRatio','CreateCacheRatio'))
--     TO '/tmp/options-pricing-backup.tsv'
--
-- Copy that file off the instance before continuing.
--
-- AND MIND THE WINDOW. This row is not "inert until you restart" -- it is inert
-- until the process restarts FOR ANY REASON. Upstream's compiled-in
-- defaultModelRatio has 237 entries and already contains claude-opus-4-8 at
-- 2.5, so if the OLD binary restarts after this seed (a crash, an OOM, a stray
-- `docker compose up`), it falls back to those defaults and applies the full
-- repricing immediately -- with no ModelDiscount layer in the old code to
-- soften it. Run the seed and the image swap back to back, in one session.
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
