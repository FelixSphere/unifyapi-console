-- Price-neutral ModelDiscount table.
--
-- Run this INSTEAD of letting the pricing release reprice customers. It makes
-- the switch to official-list-price baselines a no-op on every invoice: each
-- discount is the ratio that reproduces exactly what production was charging on
-- 2026-08-28.
--
--   discount = live_ratio / official_ratio
--
-- Derived from GET /api/pricing on the live instance, against the catalog in
-- setting/ratio_setting/unifyapi_catalog.go. 30 of the 58 catalogued
-- models deviate from list price; the other 28 are already sold at list and are
-- deliberately absent, because an absent model means "sell at the official
-- price" and a table of 1.0s is a table nobody can review.
--
-- WHY THIS IS THE SAFE ROLLOUT, AND ALSO THE HONEST ONE
--
-- Repricing on deploy would move 73.8% of requests. claude-opus-4-8 alone is
-- 54% of traffic at an 11.76x increase -- it sits at 0.085 of Anthropic's list
-- price, which is a slipped decimal point, not a commercial decision. Shipping
-- that as a surprise is a customer-facing event.
--
-- This table does NOT hide that. It instruments it. Once the release is in,
-- reconciliation costs every line at the vendor's real price, so the very first
-- report names claude-opus-4-8 as catastrophically loss-making with the dollar
-- figure attached -- which is the number needed to set a real price. And after
-- this row exists, repricing is an options edit in the admin UI, reversible,
-- with no deploy and no code review.
--
-- Group ratios are all 1.0 on production as of this snapshot (Standard /
-- Premium / Vip), so customer price == model ratio and this table reproduces
-- invoices exactly. If a group ratio is changed before the release, re-derive.
--
-- APPLY IN THE SAME SSM SESSION AS seed-pricing.sql, BEFORE IT.
-- This row is inert to the running old binary, so it is safe to insert early;
-- seed-pricing.sql is not, which is why it goes last. See that file's header.

BEGIN;

INSERT INTO options (key, value) VALUES
  ('ModelDiscount', '{"claude-fable-5":0.3,"claude-haiku-4-5-20251001":0.14,"claude-opus-4-8":0.08499999999999999,"claude-sonnet-4-6":0.3,"claude-sonnet-5":0.72,"deepseek-v4-flash":0.4464285714285714,"deepseek-v4-pro":0.7999999999999999,"gemini-2.5-flash":0.3,"gemini-2.5-flash-lite":0.8999999999999999,"gemini-2.5-pro":0.9,"gemini-3-flash-preview":0.14,"gemini-3.1-flash-lite-preview":2,"gemini-flash-latest":2,"gemini-flash-lite-latest":1.6666666666666667,"glm-5.1":0.32142857142857145,"glm-5.2":0.35714285714285715,"gpt-5-mini":0.16,"kimi-k2.5":0.5833333333333334,"kimi-k2.6":0.23157894736842105,"kimi-k3":0.6666666666666666,"nano-banana-pro-preview":0.125,"qwen3.5-27b":0.9000000000000001,"qwen3.5-35b-a3b":0.9,"qwen3.5-397b-a17b":0.5041666666666667,"qwen3.5-plus":0.5,"qwen3.6-max-preview":0.8,"qwen3.6-plus":0.56,"qwen3.7-max":0.5,"qwen3.7-plus":0.564,"qwen3.8-max":0.88872}')
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;

-- Reconciliation is on by default and runs every six hours. Until at least one
-- channel has a purchasing cost, every line is costed at list price and its
-- margin is unmeasured rather than zero; the alert engine already suppresses
-- per-line margin findings in that state and reports one explanation instead.
-- Nothing to disable -- this comment exists so the next reader does not go
-- looking for a switch.

COMMIT;

-- The derivation, for review. "live" is what production charged; "official" is
-- the vendor's published price from the catalog.
--
--   model                               live   official   discount
--   -----------------------------  ---------  ---------  ---------
--   claude-opus-4-8                   0.2125     2.5000     0.0850
--   nano-banana-pro-preview           0.1250     1.0000     0.1250
--   claude-haiku-4-5-20251001         0.0700     0.5000     0.1400
--   gemini-3-flash-preview            0.0350     0.2500     0.1400
--   gpt-5-mini                        0.0200     0.1250     0.1600
--   kimi-k2.6                         0.1100     0.4750     0.2316
--   claude-fable-5                    1.5000     5.0000     0.3000
--   claude-sonnet-4-6                 0.4500     1.5000     0.3000
--   gemini-2.5-flash                  0.0450     0.1500     0.3000
--   glm-5.1                           0.2250     0.7000     0.3214
--   glm-5.2                           0.2500     0.7000     0.3571
--   deepseek-v4-flash                 0.0312     0.0700     0.4464
--   qwen3.5-plus                      0.1000     0.2000     0.5000
--   qwen3.7-max                       0.6250     1.2500     0.5000
--   qwen3.5-397b-a17b                 0.1512     0.3000     0.5042
--   qwen3.6-plus                      0.1400     0.2500     0.5600
--   qwen3.7-plus                      0.1410     0.2500     0.5640
--   kimi-k2.5                         0.1750     0.3000     0.5833
--   kimi-k3                           1.0000     1.5000     0.6667
--   claude-sonnet-5                   0.7200     1.0000     0.7200
--   deepseek-v4-pro                   0.1740     0.2175     0.8000
--   qwen3.6-max-preview               0.5200     0.6500     0.8000
--   qwen3.8-max                       0.8887     1.0000     0.8887
--   gemini-2.5-flash-lite             0.0450     0.0500     0.9000
--   gemini-2.5-pro                    0.5625     0.6250     0.9000
--   qwen3.5-35b-a3b                   0.1125     0.1250     0.9000
--   qwen3.5-27b                       0.1350     0.1500     0.9000
--   gemini-flash-lite-latest          0.2500     0.1500     1.6667
--   gemini-3.1-flash-lite-preview     0.2500     0.1250     2.0000
--   gemini-flash-latest               0.7500     0.3750     2.0000
