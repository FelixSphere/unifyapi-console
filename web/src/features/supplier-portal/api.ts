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
    payable_usd: number
  }
  vendors: SupplierVendorPreset[]
}

export type SupplierDailyUsage = {
  day: string
  requests: number
  face_usd: number
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

export type SupplierApplication = {
  name: string
  contact_email: string
  note: string
  attested: boolean
}

export type SupplierLotSubmission = {
  vendor: string
  face_value_usd: number
  acquisition_rate: number
  expires_at: number
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

export async function applyForSupplier(application: SupplierApplication) {
  return unwrap(
    await api.post<Envelope<{ id: number; code: string; status: string }>>(
      '/api/supplier/apply',
      application
    )
  )
}

export async function submitSupplierLot(submission: SupplierLotSubmission) {
  return unwrap(
    await api.post<
      Envelope<{ lot_id: number; channel_id: number; status: CreditLotStatus }>
    >('/api/supplier/lots', submission)
  )
}
