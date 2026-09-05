/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// Pure helpers behind the Credit Supply screen. Kept free of React so the
// arithmetic and the lifecycle table can be tested directly.

import type { CreditLot, CreditLotStatus } from './credit-supply-api'

export const SECONDS_PER_DAY = 86400
export const EXPIRY_ATTENTION_DAYS = 7

export const LOT_STATUS_LABELS: Record<CreditLotStatus, string> = {
  pending: 'Pending approval',
  active: 'Active',
  suspended: 'Suspended',
  exhausted: 'Exhausted',
  expired: 'Expired',
  rejected: 'Rejected',
}

export type LotTransition = {
  to: 'active' | 'suspended' | 'rejected'
  labelKey: string
  destructive?: boolean
  // blockedKey explains why the button is disabled; undefined means enabled.
  blockedKey?: string
}

export type LotHealth =
  | 'pending'
  | 'healthy'
  | 'low'
  | 'expiring'
  | 'suspended'
  | 'retired'
  | 'rejected'

export function remainingUSD(
  lot: Pick<CreditLot, 'face_value_usd' | 'consumed_usd'>
) {
  return Math.max(lot.face_value_usd - lot.consumed_usd, 0)
}

export function payableUSD(
  lot: Pick<CreditLot, 'consumed_usd' | 'acquisition_rate'>
) {
  return lot.consumed_usd * lot.acquisition_rate
}

export function consumedPct(
  lot: Pick<CreditLot, 'face_value_usd' | 'consumed_usd'>
) {
  if (lot.face_value_usd <= 0) return 0
  return Math.min(
    100,
    Math.max(0, (lot.consumed_usd / lot.face_value_usd) * 100)
  )
}

export function formatUSD(value: number) {
  const abs = Math.abs(value)
  const digits = abs > 0 && abs < 0.01 ? 4 : 2
  return `${value < 0 ? '-' : ''}$${abs.toLocaleString('en-US', {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  })}`
}

// formatRate renders an acquisition rate as cents on the dollar, which is how
// a credit resale is quoted in conversation ("45 cents on the dollar").
export function formatRate(rate: number) {
  const cents = Math.round(rate * 100 * 10) / 10
  return `${cents}¢ / $1`
}

export function lotHealth(lot: CreditLot, nowSeconds: number): LotHealth {
  switch (lot.status) {
    case 'pending':
      return 'pending'
    case 'rejected':
      return 'rejected'
    case 'suspended':
      return 'suspended'
    case 'exhausted':
    case 'expired':
      return 'retired'
    case 'active':
      break
  }
  if (lot.low_water_usd > 0 && remainingUSD(lot) <= lot.low_water_usd)
    return 'low'
  if (
    lot.expires_at !== 0 &&
    lot.expires_at - nowSeconds <= EXPIRY_ATTENTION_DAYS * SECONDS_PER_DAY
  ) {
    return 'expiring'
  }
  return 'healthy'
}

// availableTransitions mirrors model.TransitionCreditLot so the screen never
// offers a button the server will refuse, and explains the ones it greys out.
export function availableTransitions(
  lot: CreditLot,
  nowSeconds: number
): LotTransition[] {
  switch (lot.status) {
    case 'pending':
      return [
        {
          to: 'active',
          labelKey: 'Approve',
          blockedKey:
            lot.channel_id === 0
              ? 'Bind the lot to a channel before approving it'
              : undefined,
        },
        { to: 'rejected', labelKey: 'Reject', destructive: true },
      ]
    case 'active':
      return [{ to: 'suspended', labelKey: 'Suspend', destructive: true }]
    case 'suspended':
      return [{ to: 'active', labelKey: 'Reactivate' }]
    case 'exhausted':
      return [
        {
          to: 'active',
          labelKey: 'Reactivate',
          blockedKey:
            remainingUSD(lot) > 0
              ? undefined
              : 'Raise the face value first; nothing is left to draw',
        },
      ]
    case 'expired':
      return [
        {
          to: 'active',
          labelKey: 'Reactivate',
          blockedKey:
            lot.expires_at === 0 || lot.expires_at > nowSeconds
              ? undefined
              : 'Move or clear the expiry first',
        },
      ]
    case 'rejected':
      return []
  }
}

export function dateTimeInputValue(timestamp: number) {
  if (!timestamp) return ''
  const date = new Date(timestamp * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

export function timestampFromInput(value: string) {
  if (!value) return 0
  const ms = new Date(value).getTime()
  return Number.isFinite(ms) ? Math.floor(ms / 1000) : 0
}

export function daysUntil(timestamp: number, nowSeconds: number) {
  return Math.ceil((timestamp - nowSeconds) / SECONDS_PER_DAY)
}
