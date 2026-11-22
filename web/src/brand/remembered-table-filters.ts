/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useAuthStore } from '@/stores/auth-store'

/**
 * UNIFYAPI-BRAND: a table remembers the filter chips its viewer last set.
 *
 * Operator, 2026-11-22: a filter such as "Status: Enabled" on the Users list
 * is configured once and should stay, not be set again on every visit. The
 * table hook keeps filters in the URL only, so opening the page from the
 * sidebar (a link with no search params) dropped them. Upstream had already
 * given the Channels status filter a localStorage memory; this gives every
 * table the same thing in one place.
 *
 * What is remembered: the faceted (array) filters of a table, per signed-in
 * user and per page path. What is not:
 *   - free-text filters and the search box, which are one-off lookups;
 *   - the page number;
 *   - a filter with its own `deserialize`, because that table has already
 *     decided how an absent URL value is read (Channels status, Usage Logs
 *     type).
 *
 * The URL still wins. A link that carries any of the table's filters is shown
 * exactly as written, with nothing recalled on top of it.
 */

type SearchRecord = Record<string, unknown>

type FilterConfig = {
  searchKey: string
  type?: 'string' | 'array'
  deserialize?: unknown
}

const STORAGE_PREFIX = 'unifyapi:table-filters'

function storageKey(): string {
  const userId = useAuthStore.getState().auth.user?.id ?? 0
  return `${STORAGE_PREFIX}:${userId}:${window.location.pathname}`
}

function isRemembered(config: FilterConfig): boolean {
  return config.type !== 'string' && config.deserialize === undefined
}

function isFilterValue(value: unknown): value is Array<string | number> {
  return (
    Array.isArray(value) &&
    value.length > 0 &&
    value.every((item) => typeof item === 'string' || typeof item === 'number')
  )
}

function isSet(value: unknown): boolean {
  if (Array.isArray(value)) return value.length > 0
  if (typeof value === 'string') return value.trim() !== ''
  return value !== undefined && value !== null
}

function readStored(): Record<string, Array<string | number>> {
  try {
    const parsed = JSON.parse(
      window.localStorage.getItem(storageKey()) ?? 'null'
    ) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return {}
    }
    const stored: Record<string, Array<string | number>> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (isFilterValue(value)) stored[key] = value
    }
    return stored
  } catch {
    return {}
  }
}

/**
 * The search params a table should read its filters from: the URL's own when
 * it names any of the table's filters, otherwise the URL's plus what this
 * viewer last set on this page.
 */
export function recallTableFilters(
  search: SearchRecord,
  configs: FilterConfig[]
): SearchRecord {
  if (typeof window === 'undefined') return search
  if (configs.some((config) => isSet(search[config.searchKey]))) return search

  const stored = readStored()
  const recalled: SearchRecord = {}
  for (const config of configs) {
    if (isRemembered(config) && stored[config.searchKey]) {
      recalled[config.searchKey] = stored[config.searchKey]
    }
  }
  return Object.keys(recalled).length > 0 ? { ...search, ...recalled } : search
}

/**
 * Records the filters a viewer just set. `patch` holds every filter of the
 * table by search key, with `undefined` for one that was cleared, so clearing
 * a filter (or pressing Reset) forgets it too.
 */
export function rememberTableFilters(
  patch: SearchRecord,
  configs: FilterConfig[]
): void {
  if (typeof window === 'undefined') return

  try {
    const stored = readStored()
    for (const config of configs) {
      if (!isRemembered(config)) continue
      const value = patch[config.searchKey]
      if (isFilterValue(value)) {
        stored[config.searchKey] = value
      } else {
        delete stored[config.searchKey]
      }
    }
    if (Object.keys(stored).length === 0) {
      window.localStorage.removeItem(storageKey())
      return
    }
    window.localStorage.setItem(storageKey(), JSON.stringify(stored))
  } catch {
    /* storage unavailable: the table still works, it just does not remember */
  }
}
