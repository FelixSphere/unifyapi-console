/*
UNIFYAPI-FORK: pure logic for the official-price & discount tab.

Split out of the component so it can be tested without mounting React. What is
in here is the part that decides money -- how a typed field becomes a discount,
and which discounts get persisted -- so it is worth testing directly rather
than through the DOM.
*/

/**
 * parseDiscount interprets the discount field.
 *
 * Three outcomes, deliberately distinct:
 *   null  -- the field is empty, meaning "sell at the vendor's official price"
 *   NaN   -- the field holds something unusable; the row is flagged and saving
 *            is blocked, rather than silently coercing to 1 and quietly
 *            un-discounting a model
 *   n     -- a valid multiplier
 *
 * Zero and negatives are rejected here as well as server-side. A zero discount
 * would make a model free, which is a thing an operator might mean but must
 * never reach by mistyping.
 */
export function parseDiscount(raw: string): number | null {
  const trimmed = raw.trim()
  if (trimmed === '') return null
  const value = Number(trimmed)
  if (!Number.isFinite(value) || value <= 0) return Number.NaN
  return value
}

/**
 * formatUSD renders a per-1M-token price.
 *
 * Precision scales with magnitude: the cheapest catalogued model costs $0.0625
 * per 1M input tokens, and a fixed two decimals would print it -- and several
 * of its neighbours -- as "$0.06", indistinguishable from each other and from
 * a mistake.
 */
export function formatUSD(value: number): string {
  if (value === 0) return '—'
  // The cutoff is 0.1, not 0.01: at three decimals $0.0625 renders as "$0.063"
  // and collapses into its neighbours, which is the exact failure this function
  // exists to prevent. Cached-read prices go as low as $0.0028, so four
  // decimals is the floor that keeps them all distinct.
  if (value < 0.1) return `$${value.toFixed(4)}`
  if (value < 1) return `$${value.toFixed(3)}`
  return `$${value.toFixed(2)}`
}

/**
 * DiscountLabel describes the pricing position in a language-neutral way, so
 * the component can localise it rather than the helper baking in English.
 */
export type DiscountLabel =
  | { kind: 'list' }
  | { kind: 'discount'; percent: number }
  | { kind: 'markup'; percent: number }

/** discountLabel classifies a discount multiplier against the official price. */
export function discountLabel(discount: number): DiscountLabel {
  if (discount === 1) return { kind: 'list' }
  if (discount > 1) return { kind: 'markup', percent: (discount - 1) * 100 }
  return { kind: 'discount', percent: (1 - discount) * 100 }
}

/** invalidDiscountModels lists rows whose field cannot be saved. */
export function invalidDiscountModels(drafts: Record<string, string>): string[] {
  return Object.entries(drafts)
    .filter(([, raw]) => Number.isNaN(parseDiscount(raw)))
    .map(([model]) => model)
}

/**
 * discountPayload builds what gets persisted.
 *
 * Only deviations from the official price are sent. A model at list price is
 * represented by its absence, so the stored table reads as "the exceptions" --
 * which is the list a reviewer needs to check, and it means removing a discount
 * actually removes it instead of pinning 1.0 forever.
 */
export function discountPayload(
  drafts: Record<string, string>
): Record<string, number> {
  const payload: Record<string, number> = {}
  for (const [model, raw] of Object.entries(drafts)) {
    const parsed = parseDiscount(raw)
    if (parsed !== null && !Number.isNaN(parsed) && parsed !== 1) {
      payload[model] = parsed
    }
  }
  return payload
}
