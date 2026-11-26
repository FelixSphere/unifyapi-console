/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { parseGroupsList } from './channel-utils'

// UNIFYAPI-FORK: every pricing group can route through every channel. The
// backend applies the rule where routing is decided (a channel serves its own
// group list UNION every Group Pricing key -- see
// model/unifyapi_pricing_group_channel_access.go) and never rewrites the
// channel's stored `group` column. So what the
// operator typed and what actually has access differ, and the screens must
// show both: the channel's own groups, then the ones it inherits.
export type ChannelGroupSplit = {
  own: string[]
  inherited: string[]
}

// The one exception is the channel's `excluded_groups` setting: a customer
// group listed there is not served by that channel at all, so it is shown
// neither as own nor as inherited.
export function parseExcludedGroups(
  settings: string | null | undefined
): string[] {
  if (!settings) return []
  try {
    const excluded = JSON.parse(settings)?.excluded_groups
    if (!Array.isArray(excluded)) return []
    return excluded
      .filter((group): group is string => typeof group === 'string')
      .map((group) => group.trim())
      .filter(Boolean)
  } catch {
    return []
  }
}

export function splitChannelGroups(
  explicit: string | string[] | null | undefined,
  pricingGroups: readonly string[] | null | undefined,
  excluded: readonly string[] = []
): ChannelGroupSplit {
  const rawOwn = Array.isArray(explicit)
    ? explicit
    : parseGroupsList(explicit ?? '')
  const own: string[] = []
  const seen = new Set<string>(excluded.map((group) => group.trim()))
  for (const group of rawOwn) {
    const trimmed = group.trim()
    if (!trimmed || seen.has(trimmed)) continue
    seen.add(trimmed)
    own.push(trimmed)
  }
  const inherited = [...new Set((pricingGroups ?? []).map((g) => g.trim()))]
    .filter((group) => group && !seen.has(group))
    .sort((a, b) => a.localeCompare(b))
  return { own, inherited }
}

export function effectiveChannelGroups(
  explicit: string | string[] | null | undefined,
  pricingGroups: readonly string[] | null | undefined,
  excluded: readonly string[] = []
): string[] {
  const { own, inherited } = splitChannelGroups(
    explicit,
    pricingGroups,
    excluded
  )
  return [...own, ...inherited]
}
