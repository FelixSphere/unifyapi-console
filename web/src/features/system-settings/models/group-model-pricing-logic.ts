/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
export function parseCustomerMultiplier(value: string): number | null {
  if (value.trim() === '') return null
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 && parsed <= 10
    ? parsed
    : Number.NaN
}

export function fallbackCustomerMultiplier(
  model: string,
  group: string,
  modelDiscounts: Record<string, number>,
  groupRatios: Record<string, number>
): number {
  return (modelDiscounts[model] ?? 1) * (groupRatios[group] ?? 1)
}

export function visibleCustomerPricingGroups(
  savedGroups: string[],
  draftGroupRatios?: Record<string, number>
): string[] {
  const source = draftGroupRatios
    ? Object.keys(normalizeCustomerPricingGroupRatios(draftGroupRatios))
    : savedGroups
  return [...new Set(source.map((group) => group.trim()).filter(Boolean))].sort(
    (left, right) => left.localeCompare(right)
  )
}

export function normalizeCustomerPricingGroupRatios(
  groupRatios: Record<string, number>
): Record<string, number> {
  const normalized: Record<string, number> = {}
  for (const [rawGroup, ratio] of Object.entries(groupRatios)) {
    const group = rawGroup.trim()
    if (!group || !Number.isFinite(ratio)) continue
    // Group Pricing serializes rows in order after trimming each name. When
    // two draft names collapse to one key, the later row wins there and here.
    normalized[group] = ratio
  }
  return normalized
}

export function visibleCustomerModelNames(
  models: string[],
  filter: string
): string[] {
  const normalizedFilter = filter.trim().toLowerCase()
  return [...models]
    .sort((left, right) => left.localeCompare(right))
    .filter((model) => model.toLowerCase().includes(normalizedFilter))
}

export function effectiveCustomerMultiplier(
  model: string,
  group: string,
  overrides: Record<string, string>,
  modelDiscounts: Record<string, number>,
  groupRatios: Record<string, number>
): number | null {
  if (Object.hasOwn(overrides, model)) {
    return parseCustomerMultiplier(overrides[model] ?? '')
  }
  return fallbackCustomerMultiplier(model, group, modelDiscounts, groupRatios)
}

export function formatCustomerMultiplier(value: number): string {
  return String(Number(value.toPrecision(12)))
}

export function priceAtMultiplier(
  officialInput: number,
  officialOutput: number,
  multiplier: number
) {
  return {
    input: officialInput * multiplier,
    output: officialOutput * multiplier,
  }
}

export function invalidCustomerModelPrices(
  drafts: Record<string, string>
): string[] {
  return Object.entries(drafts)
    .filter(([, value]) => Number.isNaN(parseCustomerMultiplier(value)))
    .map(([model]) => model)
    .sort()
}

export function customerModelPricePayload(
  drafts: Record<string, string>
): Record<string, number> {
  const out: Record<string, number> = {}
  for (const [model, value] of Object.entries(drafts)) {
    const parsed = parseCustomerMultiplier(value)
    if (parsed !== null && !Number.isNaN(parsed)) out[model] = parsed
  }
  return out
}

export function mergeCustomerModelDrafts(
  server: Record<string, Record<string, number>>,
  current: Record<string, Record<string, string>>,
  dirtyGroups: ReadonlySet<string>
): Record<string, Record<string, string>> {
  const next: Record<string, Record<string, string>> = {}
  const groups = new Set([...Object.keys(server), ...Object.keys(current)])
  for (const group of groups) {
    if (dirtyGroups.has(group)) {
      next[group] = current[group] ?? {}
      continue
    }
    next[group] = Object.fromEntries(
      Object.entries(server[group] ?? {}).map(([model, ratio]) => [
        model,
        String(ratio),
      ])
    )
  }
  return next
}
