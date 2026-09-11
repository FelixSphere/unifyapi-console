/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import type { CreditLotStatus } from '@/features/system-settings/billing/credit-supply-api'
import { api } from '@/lib/api'

export type SupplierVendorPreset = {
  key: string
  label: string
  channel_type: number
  base_url: string
  models: string[]
}

// Two ways to hand us a key: sell the credits outright, or contribute the key
// and keep a share of what it earns. Mirrors model.CreditLotDeal*.
export type SupplierDeal = 'purchase' | 'revenue_share'

export type SupplierLot = {
  id: number
  vendor: string
  channel_id: number
  channel_name: string
  face_value_usd: number
  acquisition_rate: number
  consumed_usd: number
  remaining_usd: number
  payable_usd: number
  unpriced_requests: number
  expires_at: number
  status: CreditLotStatus
  status_reason: string
  source: 'admin' | 'supplier'
  retired_at: number
  created_at: number
  verified_at: number
  payout_method: 'platform_credit' | 'external' | ''
  payout_usd: number
  paid_usd: number
  paid_at: number
  payout_reference: string
  // The dividend side of a contributed key; zeroes on a sale.
  deal_type: SupplierDeal
  revenue_share_pct: number
  share_revenue_usd: number
  share_earned_usd: number
  share_paid_usd: number
  share_unpaid_usd: number
}

// The posted terms: what we pay per dollar of each vendor's credit.
export type SupplierTerms = {
  buy_rates: Record<string, number>
  min_face_usd: number
  platform_credit_bonus: number
  // The other offer: keep the key, keep a share of what it earns.
  revenue_share_rates: Record<string, number>
  revenue_share_basis: 'revenue' | 'margin'
  min_share_payout_usd: number
}

// One dividend payment, as the contributor sees it.
export type SupplierSharePayout = {
  id: number
  lot_id: number
  batch: string
  amount_usd: number
  earned_to_date_usd: number
  revenue_to_date_usd: number
  share_pct: number
  method: 'platform_credit' | 'external'
  reference: string
  created_at: number
}

export type SupplierPortalData = {
  supplier: {
    id: number
    name: string
    code: string
    contact_email: string
    status: 'pending' | 'active' | 'suspended' | 'rejected'
    status_reason: string
    counterparty: string
  }
  lots: SupplierLot[]
  totals: {
    face_usd: number
    consumed_usd: number
    remaining_usd: number
    awaiting_payment_usd: number
    paid_usd: number
    share_revenue_usd: number
    share_earned_usd: number
    share_paid_usd: number
    share_unpaid_usd: number
  }
  share_payouts: SupplierSharePayout[]
  vendors: SupplierVendorPreset[]
  terms: SupplierTerms
}

export type SupplierDailyUsage = {
  day: string
  requests: number
  face_usd: number
  // What customers paid for that day's traffic, which is what a contributed
  // key's share is calculated from.
  revenue_usd: number
}

export type SupplierStatementLine = {
  model: string
  channel_id?: number
  channel_name?: string
  requests: number
  prompt_tokens: number
  cached_tokens: number
  completion_tokens: number
  amount_usd: number
  unpriced?: boolean
}

export type SupplierStatement = {
  id: number
  period_start: string
  period_end: string
  amount_usd: number
  status: 'issued' | 'settled' | 'void'
  created_at: number
  requests: number
  lines?: SupplierStatementLine[]
}

export async function getSupplierTerms() {
  return unwrap(
    await api.get<
      Envelope<SupplierTerms & { vendors: SupplierVendorPreset[] }>
    >('/api/supplier/terms')
  )
}

export type SupplierLotSubmission = {
  vendor: string
  deal_type: SupplierDeal
  face_value_usd: number
  expires_at: number
  payout_method: 'platform_credit' | 'external'
  payout_account: string
  note: string
  upstream_key: string
  models: string[]
  transfer_rights_confirmed: boolean
}

type Envelope<T> = { success: boolean; message: string; data: T }

function unwrap<T>(response: { data: Envelope<T> }): T {
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

// A 404 here is the normal answer for a login that is not a supplier, so the
// global error toast is suppressed and the caller decides what to show.
export async function getSupplierPortal() {
  return unwrap(
    await api.get<Envelope<SupplierPortalData>>('/api/supplier/me', {
      skipErrorHandler: true,
      skipBusinessError: true,
    })
  )
}

export async function isSupplierLogin(): Promise<boolean> {
  try {
    await getSupplierPortal()
    return true
  } catch {
    return false
  }
}

export async function getSupplierUsage(days = 30) {
  return unwrap(
    await api.get<Envelope<SupplierDailyUsage[]>>('/api/supplier/usage', {
      params: { days },
    })
  )
}

export async function getSupplierStatements() {
  return unwrap(
    await api.get<Envelope<SupplierStatement[]>>('/api/supplier/statements')
  )
}

export async function submitSupplierLot(submission: SupplierLotSubmission) {
  return unwrap(
    await api.post<
      Envelope<{
        lot_id: number
        channel_id: number
        status: CreditLotStatus
        deal_type: SupplierDeal
        revenue_share_pct: number
        payout_usd?: number
      }>
    >('/api/supplier/lots', submission)
  )
}

// payoutPreview is what a seller will receive for a sale under the posted
// terms, mirrored from the server's PayoutUSD so the form can show it live.
export function payoutPreview(
  terms: SupplierTerms,
  vendor: string,
  faceUSD: number,
  method: 'platform_credit' | 'external'
) {
  const rate = terms.buy_rates[vendor] ?? 0
  const bonus =
    method === 'platform_credit' ? 1 + (terms.platform_credit_bonus || 0) : 1
  return { rate, amount: faceUSD > 0 ? faceUSD * rate * bonus : 0 }
}

// sharePreview is what a contributor keeps, mirrored from the server's posted
// terms so the form can name the deal before anything is submitted.
export function sharePreview(terms: SupplierTerms, vendor: string) {
  return terms.revenue_share_rates?.[vendor] ?? 0
}

// contributableVendors are the ones we currently take keys from on
// revenue-share terms. An empty list means the offer is not open.
export function contributableVendors(
  terms: SupplierTerms,
  vendors: SupplierVendorPreset[]
) {
  return vendors.filter((vendor) => sharePreview(terms, vendor.key) > 0)
}
