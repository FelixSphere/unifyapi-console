/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: pure logic for the upstream purchasing-cost tab.

Same reason as baseline-pricing-logic.ts: this decides a number that ends up in
a reconciliation report finance reads, so it is tested directly rather than
through the DOM.

One asymmetry with the customer discount is worth stating, because it drives the
validation: a customer discount above 1 is unusual but legitimate (we may resell
at a premium). A purchasing cost above 1 means we pay MORE than the vendor's
public price, which is almost never a real contract -- so it is warned about
loudly rather than treated as ordinary.
*/

/** Cost ratios above this are refused server-side too; mirrored for fast feedback. */
export const MAX_CHANNEL_COST_RATIO = 5

/**
 * parseCostRatio interprets the cost field.
 *
 *   null -- empty, meaning "we pay this vendor's list price"
 *   NaN  -- unusable; the row is flagged and saving is blocked
 *   n    -- a valid multiplier on list price
 */
export function parseCostRatio(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return null
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value <= 0) return Number.NaN
  if (value > MAX_CHANNEL_COST_RATIO) return Number.NaN
  return value
}

/**
 * CostLabel describes the purchasing position in a language-neutral way.
 *
 * Structured rather than a formatted string so the component can localise it --
 * a pure function returning "30% off list" forces English into a Chinese UI,
 * which is how the first cut of this column shipped.
 */
export type CostLabel =
  | { kind: 'list' }
  | { kind: 'discount'; percent: number }
  | { kind: 'above-list'; percent: number }

/** costLabel classifies a cost ratio against the vendor list price. */
export function costLabel(ratio: number): CostLabel {
  if (ratio === 1) return { kind: 'list' }
  if (ratio > 1) return { kind: 'above-list', percent: (ratio - 1) * 100 }
  return { kind: 'discount', percent: (1 - ratio) * 100 }
}

/**
 * maxSafeDiscount is the deepest customer discount a channel's purchasing cost
 * can support before the model loses money.
 *
 * It equals the cost ratio exactly, and that is the whole point: selling at
 * official list price while buying at official list price yields precisely zero
 * margin, so every cent of margin comes from buying below list. A customer
 * discount deeper than the purchasing discount is a loss on every request.
 * Surfacing it next to the input is what stops that being discovered a month
 * later in a reconciliation report.
 */
export function maxSafeDiscount(costRatio: number): number {
  return costRatio
}

export function invalidCostChannels(drafts: Record<string, string>): string[] {
  return Object.entries(drafts)
    .filter(([, raw]) => Number.isNaN(parseCostRatio(raw)))
    .map(([id]) => id)
}

/**
 * costPayload builds what gets persisted: only channels that deviate from list
 * price. A channel at list is represented by its absence, so clearing a field
 * genuinely removes the override instead of pinning 1.0 forever.
 */
export function costPayload(
  drafts: Record<string, string>
): Record<string, number> {
  const payload: Record<string, number> = {}
  for (const [id, raw] of Object.entries(drafts)) {
    const parsed = parseCostRatio(raw)
    if (parsed !== null && !Number.isNaN(parsed) && parsed !== 1) {
      payload[id] = parsed
    }
  }
  return payload
}
