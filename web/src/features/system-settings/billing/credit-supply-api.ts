/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

export type CreditSupplierStatus =
  | 'pending'
  | 'active'
  | 'suspended'
  | 'rejected'

export type CreditSupplier = {
  id: number
  name: string
  code: string
  contact_email: string
  user_id: number
  status: CreditSupplierStatus
  status_reason: string
  attestation_version: string
  attested_at: number
  payout_terms: string
  note: string
  created_at: number
  updated_at: number
}

export type CreditSupplierInput = Omit<
  CreditSupplier,
  'id' | 'created_at' | 'updated_at' | 'attestation_version' | 'attested_at'
>

export type CreditLotStatus =
  | 'pending'
  | 'active'
  | 'suspended'
  | 'exhausted'
  | 'expired'
  | 'rejected'

export type CreditLot = {
  id: number
  supplier_id: number
  vendor: string
  channel_id: number
  face_value_usd: number
  acquisition_rate: number
  consumed_usd: number
  unpriced_requests: number
  low_water_usd: number
  low_water_notified_at: number
  expires_at: number
  status: CreditLotStatus
  source: 'admin' | 'supplier'
  note: string
  status_reason: string
  attestation_version: string
  attested_at: number
  attested_by: string
  approved_by: string
  approved_at: number
  retired_at: number
  created_at: number
  updated_at: number
}

export type CreditLotInput = {
  supplier_id: number
  vendor: string
  channel_id: number
  face_value_usd: number
  acquisition_rate: number
  low_water_usd: number
  expires_at: number
  status: 'pending' | 'active'
  note: string
}

export type CreditLotEvent = {
  id: number
  lot_id: number
  actor: string
  event_type: string
  from_status: string
  to_status: string
  message: string
  created_at: number
}

export type CreditLotUsage = {
  id: number
  lot_id: number
  day: string
  requests: number
  face_usd: number
}

export type CreditSupplyVendorTotals = {
  vendor: string
  lots: number
  face_usd: number
  consumed_usd: number
  remaining_usd: number
  payable_usd: number
}

export type CreditSupplyOverview = {
  suppliers: number
  lots_by_status: Partial<Record<CreditLotStatus, number>>
  face_usd: number
  consumed_usd: number
  remaining_usd: number
  payable_usd: number
  unpriced_lots: number
  by_vendor: CreditSupplyVendorTotals[]
  attention: CreditLot[]
}

type Envelope<T> = { success: boolean; message: string; data: T }

function unwrap<T>(response: { data: Envelope<T> }): T {
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function getCreditSupplyOverview() {
  return unwrap(
    await api.get<Envelope<CreditSupplyOverview>>('/api/credit-supply/overview')
  )
}

export async function getCreditSuppliers() {
  return unwrap(
    await api.get<Envelope<CreditSupplier[]>>('/api/credit-supply/suppliers')
  )
}

export async function saveCreditSupplier(input: {
  id?: number
  supplier: CreditSupplierInput
}) {
  const response = input.id
    ? await api.put<Envelope<CreditSupplier>>(
        `/api/credit-supply/suppliers/${input.id}`,
        input.supplier
      )
    : await api.post<Envelope<CreditSupplier>>(
        '/api/credit-supply/suppliers',
        input.supplier
      )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data
}

export async function getCreditLots(
  params: {
    supplier_id?: number
    status?: CreditLotStatus
  } = {}
) {
  return unwrap(
    await api.get<Envelope<CreditLot[]>>('/api/credit-supply/lots', { params })
  )
}

export async function saveCreditLot(input: {
  id?: number
  lot: CreditLotInput
}) {
  const response = input.id
    ? await api.put<Envelope<CreditLot>>(
        `/api/credit-supply/lots/${input.id}`,
        input.lot
      )
    : await api.post<Envelope<CreditLot>>('/api/credit-supply/lots', input.lot)
  if (!response.data.success) throw new Error(response.data.message)
  return response.data
}

// The server owns the lifecycle table. Approval must carry the operator's
// right-to-transfer confirmation; rejection and suspension must carry a reason
// the supplier will read.
export async function transitionCreditLot(input: {
  id: number
  to: 'active' | 'suspended' | 'rejected'
  reason?: string
  transfer_rights_confirmed?: boolean
}) {
  return unwrap(
    await api.post<Envelope<CreditLot>>(
      `/api/credit-supply/lots/${input.id}/transition`,
      {
        to: input.to,
        reason: input.reason ?? '',
        transfer_rights_confirmed: input.transfer_rights_confirmed ?? false,
      }
    )
  )
}

export async function getCreditLotEvents(lotId: number) {
  return unwrap(
    await api.get<Envelope<CreditLotEvent[]>>(
      `/api/credit-supply/lots/${lotId}/events`
    )
  )
}

export async function getCreditLotUsage(lotId: number, days = 30) {
  return unwrap(
    await api.get<Envelope<CreditLotUsage[]>>(
      `/api/credit-supply/lots/${lotId}/usage`,
      { params: { days } }
    )
  )
}
