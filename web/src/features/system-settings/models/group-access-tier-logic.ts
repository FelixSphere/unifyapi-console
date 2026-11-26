/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: marking a pricing group as an access tier from Group Pricing.

A pricing group is a customer, and its logins share one wallet. That is wrong
for a group that only grants something, such as access to a model: its members
are strangers. A group marked as an access tier keeps a wallet per login, as
`default` does. The mark is one server operation
(POST /api/pricing/group/access-tier) and is not part of the form's save: it
checks what the change would do to the wallets the group already has.
*/

/**
 * The marked groups are read under the settings key on purpose: whatever reads
 * the stored settings again (a rename, a save) reads the marks again too.
 */
export const ACCESS_TIER_GROUPS_QUERY_KEY = [
  'system-options',
  'access-tier-groups',
] as const

/**
 * Why a row cannot be marked or un-marked, as a translation key, or null when
 * it can. The server checks again; this is so the box explains itself.
 */
export function accessTierBlocker(row: {
  name: string
  draft: boolean
}): string | null {
  if (row.name.trim() === 'default') {
    return 'Logins in default already keep their own wallets'
  }
  if (row.draft) {
    return 'Save the group before marking it as an access tier'
  }
  return null
}
