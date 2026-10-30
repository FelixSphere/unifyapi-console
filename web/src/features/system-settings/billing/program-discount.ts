/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * A Partnership Program's discount is a multiplier over the official price,
 * the same shape the Group Pricing and Customer model prices editors use, so
 * an operator types one kind of number everywhere.
 *
 * Empty means the program sets no discount. That is a real state, not a
 * missing value: the server stores it as 0 and applying it removes the rows
 * the program owns. So an empty box must parse to 0 rather than fail
 * validation, and 0 typed by hand means the same thing.
 *
 * The range is (0, 1] plus 0. Above 1 is a surcharge, and every screen that
 * renders this reads it as a discount -- 1.2 would show as "120% of the
 * official price" beside the word discount. Below 0 is not a price.
 */
export type ParsedProgramDiscount =
  | { ok: true; value: number }
  | { ok: false; reason: 'not-a-number' | 'out-of-range' }

export function parseProgramDiscount(raw: string): ParsedProgramDiscount {
  const trimmed = raw.trim()
  if (trimmed === '') return { ok: true, value: 0 }

  const value = Number(trimmed)
  if (!Number.isFinite(value)) return { ok: false, reason: 'not-a-number' }
  if (value < 0 || value > 1) return { ok: false, reason: 'out-of-range' }
  return { ok: true, value }
}

/**
 * The percentage of the official price a customer in this program pays, for
 * the hint under the input. Returns null when there is no discount to
 * describe, so the caller shows the explanatory text instead of "0%".
 */
export function programDiscountPercent(raw: string): number | null {
  const parsed = parseProgramDiscount(raw)
  if (!parsed.ok || parsed.value === 0) return null
  return +(parsed.value * 100).toFixed(2)
}
