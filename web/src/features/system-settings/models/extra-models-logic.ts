/*
UNIFYAPI-FORK: pure logic for the extra-models tab.

This is the screen that replaced the raw-ratio editor, and it exists because
that editor's save REPLACED the whole code catalog. So the rules it enforces are
not cosmetic — they are the difference between the two — and they are tested
directly rather than through the DOM.

Prices are USD per 1M tokens, matching the catalog. Never ratios: "2.5" is
unreadable without knowing the quota convention, and a decimal typed one place
off in that form billed a model at 8.5% of cost on this deployment for weeks.
*/
import type { ExtraModelDraft } from '../types'

/** Matches maxExtraPriceUSD in setting/ratio_setting/unifyapi_extra_models.go.
 *  Past this a number is a misplaced decimal or a per-1K price in a per-1M
 *  field, not a quote — the priciest catalogued model is $10/1M. */
export const MAX_PRICE_USD = 1000

export type FieldError = { field: keyof ExtraModelDraft; message: string }

/**
 * validateDraft mirrors ValidateExtraModels on the server.
 *
 * Duplicated deliberately, not shared: the server must reject bad input whoever
 * sends it, and the form must say so before a save round-trip. What must not
 * drift is the RULES, so each one names the same condition in both places and
 * the thresholds are pinned by test on this side.
 */
export function validateDraft(
  draft: ExtraModelDraft,
  cataloguedModels: string[],
  existingModels: string[]
): FieldError[] {
  const errors: FieldError[] = []
  const name = draft.model

  if (!name.trim()) {
    errors.push({ field: 'model', message: 'Model name is required' })
  } else if (name !== name.trim()) {
    errors.push({
      field: 'model',
      message: 'Model name has leading or trailing spaces, so API calls will not match it',
    })
  } else if (cataloguedModels.includes(name)) {
    // The rule that keeps this table from becoming the one it replaced.
    errors.push({
      field: 'model',
      message:
        'This model already has an official price in the catalog. Change what customers pay for it under 官方报价与折扣 instead.',
    })
  } else if (existingModels.includes(name)) {
    errors.push({ field: 'model', message: 'Already in this table' })
  }

  const input = Number.parseFloat(draft.input_usd)
  const output = Number.parseFloat(draft.output_usd)

  if (!Number.isFinite(input) || input <= 0) {
    errors.push({ field: 'input_usd', message: 'Input price must be greater than 0' })
  } else if (input > MAX_PRICE_USD) {
    errors.push({ field: 'input_usd', message: 'Price looks like a misplaced decimal' })
  }

  if (!Number.isFinite(output) || output <= 0) {
    errors.push({ field: 'output_usd', message: 'Output price must be greater than 0' })
  } else if (output > MAX_PRICE_USD) {
    errors.push({ field: 'output_usd', message: 'Price looks like a misplaced decimal' })
  }

  if (draft.cache_read_usd.trim()) {
    const cacheRead = Number.parseFloat(draft.cache_read_usd)
    if (!Number.isFinite(cacheRead) || cacheRead < 0) {
      errors.push({ field: 'cache_read_usd', message: 'Cache read price cannot be negative' })
    } else if (Number.isFinite(input) && cacheRead > input) {
      // Backwards everywhere it is published, and it overstates cost in
      // reconciliation rather than under-charging, so it hides rather than
      // announces itself.
      errors.push({
        field: 'cache_read_usd',
        message: 'Cache read costs more than fresh input — the two are the wrong way round',
      })
    }
  }

  return errors
}

/** emptyDraft is a blank row. Cache fields start empty rather than at 0, so
 *  "the vendor publishes no cache price" stays distinct from "cache is free". */
export function emptyDraft(): ExtraModelDraft {
  return {
    model: '',
    input_usd: '',
    output_usd: '',
    cache_read_usd: '',
    cache_write_usd: '',
    vendor: '',
    note: '',
  }
}

/** draftToPayload converts a validated draft into the wire shape. Blank optional
 *  fields are omitted rather than sent as 0. */
export function draftToPayload(draft: ExtraModelDraft) {
  const payload: Record<string, number | string> = {
    input_usd: Number.parseFloat(draft.input_usd),
    output_usd: Number.parseFloat(draft.output_usd),
  }
  const cacheRead = Number.parseFloat(draft.cache_read_usd)
  if (draft.cache_read_usd.trim() && Number.isFinite(cacheRead)) {
    payload.cache_read_usd = cacheRead
  }
  const cacheWrite = Number.parseFloat(draft.cache_write_usd)
  if (draft.cache_write_usd.trim() && Number.isFinite(cacheWrite)) {
    payload.cache_write_usd = cacheWrite
  }
  if (draft.vendor.trim()) payload.vendor = draft.vendor.trim()
  if (draft.note.trim()) payload.note = draft.note.trim()
  return payload
}

/**
 * ratioFromUSD converts a USD-per-1M price into the billing ratio, so the form
 * can show what the relay will actually use.
 *
 * Shown read-only and derived, never typed. The whole reason this fork stores
 * dollars is that the ratio is the unreadable form; letting someone edit it here
 * would reintroduce exactly the mistake the dollars are there to prevent.
 */
export const USD_PER_MILLION_PER_RATIO_UNIT = 2

export function ratioFromUSD(inputUSD: number): number {
  return inputUSD / USD_PER_MILLION_PER_RATIO_UNIT
}

/** completionRatioFromUSD is the output multiplier over input. */
export function completionRatioFromUSD(inputUSD: number, outputUSD: number): number {
  if (!inputUSD) return 1
  return outputUSD / inputUSD
}

/** formatUSDPrice renders a per-1M price at a precision that keeps cheap models
 *  distinguishable — several catalogued models cost cents per 1M. */
export function formatUSDPrice(value: number): string {
  if (value === 0) return '$0'
  if (value < 0.01) return `$${value.toFixed(4)}`
  if (value < 1) return `$${value.toFixed(3)}`
  return `$${value.toFixed(2)}`
}
