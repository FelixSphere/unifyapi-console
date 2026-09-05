/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

export const QUOTA_PER_USD = 500_000

export type ContributionStatus =
  | 'submitted'
  | 'needs_credentials'
  | 'verifying'
  | 'active'
  | 'exhausted'
  | 'expired'
  | 'revoked'
  | 'rejected'
  | 'cancelled'

export type ContributionEvent = {
  id: number
  event_type: string
  from_status: string
  to_status: string
  message: string
  created_at: number
}

export type ContributionPayout = {
  id: number
  amount_quota: number
  status: 'draft' | 'approved' | 'paid' | 'void'
  external_reference: string
  note: string
  approved_at: number
  paid_at: number
  created_at: number
}

export type CreditContribution = {
  id: number
  tenant_id: number
  provider: 'openai' | 'anthropic' | 'other'
  account_label: string
  models: string
  requested_quota: number
  requested_acquisition_ratio: number
  approved_quota: number
  acquisition_ratio: number
  pool_id: number
  channel_id: number
  cycle: number
  expires_at: number
  status: ContributionStatus
  effective_status: ContributionStatus
  supplier_notes: string
  admin_notes?: string
  rejection_reason?: string
  last_verified_at: number
  created_at: number
  inventory_remaining: number
  consumed_quota: number
  lifetime_payable_quota: number
  committed_payout_quota: number
  available_payout_quota: number
  events: ContributionEvent[]
  payouts: ContributionPayout[]
}

type Envelope<T> = { success: boolean; message: string; data: T }

function unwrap<T>(response: { data: Envelope<T> }, fallback: T): T {
  if (!response.data.success) {
    throw new Error(
      response.data.message || 'Credit contribution request failed'
    )
  }
  return response.data.data ?? fallback
}

export function usdToQuota(value: number) {
  return Math.round(value * QUOTA_PER_USD)
}

export function quotaToUsd(value: number) {
  return value / QUOTA_PER_USD
}

export function formatCreditUsd(value: number) {
  return `$${quotaToUsd(value).toFixed(2)}`
}

export async function getMyCreditContributions() {
  return unwrap(
    await api.get<Envelope<CreditContribution[]>>(
      '/api/credit-contribution/self/'
    ),
    []
  )
}

export async function submitCreditContribution(input: {
  provider: string
  account_label: string
  models: string
  requested_quota: number
  requested_acquisition_ratio: number
  supplier_notes: string
  attested: boolean
}) {
  return unwrap(
    await api.post<Envelope<CreditContribution>>(
      '/api/credit-contribution/self/',
      input
    ),
    null as never
  )
}

export async function cancelCreditContribution(id: number) {
  unwrap(
    await api.post<Envelope<null>>(
      `/api/credit-contribution/self/${id}/cancel`,
      {}
    ),
    null
  )
}

export async function getCreditContributions() {
  return unwrap(
    await api.get<Envelope<CreditContribution[]>>('/api/credit-contribution/'),
    []
  )
}

export async function reviewCreditContribution(
  id: number,
  input: { status: string; message: string; admin_notes: string }
) {
  unwrap(
    await api.post<Envelope<null>>(
      `/api/credit-contribution/${id}/review`,
      input
    ),
    null
  )
}

export async function activateCreditContribution(
  id: number,
  input: {
    pool_id: number
    channel_id: number
    approved_quota: number
    acquisition_ratio: number
    expires_at: number
    admin_notes: string
  }
) {
  return unwrap(
    await api.post<Envelope<CreditContribution>>(
      `/api/credit-contribution/${id}/activate`,
      input
    ),
    null as never
  )
}

export async function resetCreditContribution(
  id: number,
  input: { verified_quota: number; expires_at: number; reason: string }
) {
  return unwrap(
    await api.post<Envelope<CreditContribution>>(
      `/api/credit-contribution/${id}/reset`,
      input
    ),
    null as never
  )
}

export async function revokeCreditContribution(id: number, reason: string) {
  unwrap(
    await api.post<Envelope<null>>(`/api/credit-contribution/${id}/revoke`, {
      reason,
    }),
    null
  )
}

export async function createContributionPayout(
  id: number,
  input: { amount_quota: number; note: string }
) {
  return unwrap(
    await api.post<Envelope<ContributionPayout>>(
      `/api/credit-contribution/${id}/payouts`,
      input
    ),
    null as never
  )
}

export async function updateContributionPayout(
  payoutId: number,
  input: { status: string; external_reference?: string }
) {
  unwrap(
    await api.post<Envelope<null>>(
      `/api/credit-contribution/payouts/${payoutId}`,
      input
    ),
    null
  )
}
