/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// Quota is stored as an integer unit; upstream renders it as currency at 500k
// units per unit of currency (common.QuotaPerUnit). Kept here so the dashboard
// and the detail tabs cannot drift apart on how money is displayed.
export const QUOTA_PER_UNIT = 500_000

export function money(quota: number) {
  return `$${(quota / QUOTA_PER_UNIT).toFixed(4)}`
}

export function compact(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

export function when(unix: number) {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleString()
}

export function day(unix: number) {
  if (!unix) return 'Open-ended'
  return new Date(unix * 1000).toLocaleDateString()
}

/** Whole days until a term expires. Negative once it has lapsed. */
export function daysUntil(unix: number) {
  if (!unix) return Infinity
  return Math.ceil((unix * 1000 - Date.now()) / 86_400_000)
}
