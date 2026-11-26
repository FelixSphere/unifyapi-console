/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: renaming a pricing group from Group Pricing.

A group has no id; its name is what logins, API keys, wallets, per-model prices
and channel rules store. Typing a new name over a saved one and saving the form
only replaced the key in the three maps the form owns, which stranded every
other reference on a group that no longer had a price. So the name of a saved
group is not an editable field: it is changed by a rename the server performs
everywhere at once (POST /api/pricing/group/rename).
*/
import { normalizeJsonString } from './utils'

/** The names the server refers to by name, and so will not take. */
const RESERVED_GROUP_NAMES = new Set(['default', 'auto'])

/**
 * Why a new name cannot be used, as a translation key, or null when it can.
 * The server checks all of this again; this is only so the operator is told
 * before the request rather than after.
 */
export function groupRenameProblem(
  from: string,
  to: string,
  existingNames: readonly string[]
): string | null {
  const name = to.trim()
  if (name === '') return 'Enter the new group name'
  if (name === from) return 'The new name is the same as the current one'
  if (RESERVED_GROUP_NAMES.has(name)) return 'This group name is reserved'
  if (name.includes(',')) return 'A group name cannot contain a comma'
  if (existingNames.includes(name)) {
    return 'A group with this name already exists'
  }
  return null
}

/** The group names in a saved GroupRatio option. */
export function savedGroupNames(savedGroupRatio: string): string[] {
  try {
    const parsed: unknown = JSON.parse(savedGroupRatio)
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return Object.keys(parsed)
    }
  } catch {
    /* an unreadable saved value has no saved groups */
  }
  return []
}

/**
 * Whether the form holds anything that has not been saved.
 *
 * A rename reloads the saved settings, which resets the form. It is only
 * offered while there is nothing typed that the reload would throw away.
 */
export function hasUnsavedGroupChanges(
  current: Record<string, unknown>,
  saved: Record<string, unknown>
): boolean {
  return Object.keys(saved).some((key) => {
    const before = saved[key]
    const now = current[key]
    if (typeof before === 'string' && typeof now === 'string') {
      return normalizeJsonString(now) !== normalizeJsonString(before)
    }
    return String(now) !== String(before)
  })
}
