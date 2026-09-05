/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

export type TenantPayment = {
  id: number
  user_id: number
  username: string
  amount: number
  money: number
  trade_no: string
  payment_method: string
  payment_provider: string
  status: string
  create_time: number
}

export type TenantAuditEntry = {
  id: number
  user_id: number
  username: string
  type: number
  content: string
  ip: string
  created_at: number
  other: string
}

export type TenantOverview = {
  tenant_id: number
  name: string
  slug: string
  status: number
  group: string
  created_at: number
  expires_at: number
  suspended_at: number
  suspend_reason?: string
  member_count: number
  quota: number
  used_quota: number
  period_quota: number
  period_requests: number
  period_prompt_tokens: number
  period_completion_tokens: number
  last_activity_at: number
}

export type TenantModelUsage = {
  model_name: string
  requests: number
  quota: number
  prompt_tokens: number
  completion_tokens: number
}

export type CreditPoolSummary = {
  id: number
  name: string
  routing_group: string
  models: string
  status: number
  original_quota: number
  remaining_quota: number
  granted_quota: number
  grant_remaining_quota: number
  accrued_payable_quota: number
  lots: number
  grants: number
}

export type CreditPoolLot = {
  id: number
  pool_id: number
  channel_id: number
  source_type: 'free' | 'purchased' | 'contributed'
  contributor_tenant_id: number
  label: string
  original_quota: number
  remaining_quota: number
  acquisition_ratio: number
  accrued_payable_quota: number
  expires_at: number
  status: number
}

export type TenantCreditGrant = {
  id: number
  tenant_id: number
  pool_id: number
  name: string
  original_quota: number
  remaining_quota: number
  starts_at: number
  expires_at: number
  priority: number
  status: number
}

type Envelope<T> = { success: boolean; message: string; data: T }

export async function getTenantOverviews(params: {
  startAt?: number
  endAt?: number
  pageSize?: number
  offset?: number
}) {
  const search = new URLSearchParams()
  if (params.startAt) search.set('start_at', String(params.startAt))
  if (params.endAt) search.set('end_at', String(params.endAt))
  if (params.pageSize) search.set('page_size', String(params.pageSize))
  if (params.offset) search.set('offset', String(params.offset))

  const res = await api.get<
    Envelope<{ items: TenantOverview[]; total: number }>
  >(`/api/tenant/?${search.toString()}`)
  if (!res.data.success) {
    throw new Error(res.data.message || 'failed to load tenants')
  }
  return res.data.data
}

export async function getTenantUsage(
  tenantId: number,
  params: { startAt?: number; endAt?: number } = {}
) {
  const search = new URLSearchParams()
  if (params.startAt) search.set('start_at', String(params.startAt))
  if (params.endAt) search.set('end_at', String(params.endAt))

  const res = await api.get<
    Envelope<{
      tenant: TenantOverview & { remark?: string }
      models: TenantModelUsage[]
      members: {
        id: number
        username: string
        display_name: string
        email: string
        status: number
        role: number
        last_login_at: number
      }[]
    }>
  >(`/api/tenant/${tenantId}/usage?${search.toString()}`)
  if (!res.data.success) {
    throw new Error(res.data.message || 'failed to load tenant usage')
  }
  return res.data.data
}

export async function getTenantPayments(tenantId: number, limit = 100) {
  const res = await api.get<Envelope<TenantPayment[]>>(
    `/api/tenant/${tenantId}/payments?limit=${limit}`
  )
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data ?? []
}

export async function getTenantAudits(tenantId: number, limit = 100) {
  const res = await api.get<Envelope<TenantAuditEntry[]>>(
    `/api/tenant/${tenantId}/audits?limit=${limit}`
  )
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data ?? []
}

export async function suspendTenant(tenantId: number, reason: string) {
  const res = await api.post<Envelope<null>>(
    `/api/tenant/${tenantId}/suspend`,
    {
      reason,
    }
  )
  if (!res.data.success) throw new Error(res.data.message)
}

export async function resumeTenant(tenantId: number) {
  const res = await api.post<Envelope<null>>(
    `/api/tenant/${tenantId}/resume`,
    {}
  )
  if (!res.data.success) throw new Error(res.data.message)
}

export async function extendTenantTerm(tenantId: number, days: number) {
  const res = await api.post<Envelope<{ expires_at: number }>>(
    `/api/tenant/${tenantId}/extend`,
    { days }
  )
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}

export async function getCreditPools() {
  const res = await api.get<Envelope<CreditPoolSummary[]>>('/api/credit-pool/')
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data ?? []
}

export async function getCreditPool(poolId: number) {
  const res = await api.get<
    Envelope<{ lots: CreditPoolLot[]; grants: TenantCreditGrant[] }>
  >(`/api/credit-pool/${poolId}`)
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}

export async function createCreditPool(input: {
  name: string
  routing_group: string
  models: string
}) {
  const res = await api.post<Envelope<CreditPoolSummary>>(
    '/api/credit-pool/',
    input
  )
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}

export async function addCreditPoolLot(
  poolId: number,
  input: Omit<
    CreditPoolLot,
    'id' | 'pool_id' | 'remaining_quota' | 'status' | 'accrued_payable_quota'
  >
) {
  const res = await api.post<Envelope<CreditPoolLot>>(
    `/api/credit-pool/${poolId}/lots`,
    input
  )
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}

export async function addTenantCreditGrant(
  poolId: number,
  input: Omit<
    TenantCreditGrant,
    'id' | 'pool_id' | 'remaining_quota' | 'status'
  >
) {
  const res = await api.post<Envelope<TenantCreditGrant>>(
    `/api/credit-pool/${poolId}/grants`,
    input
  )
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}

export async function getMyPromotionalCredits() {
  const res = await api.get<
    Envelope<{
      original_quota: number
      remaining_quota: number
      grants: TenantCreditGrant[]
    }>
  >('/api/credit-pool/self')
  if (!res.data.success) throw new Error(res.data.message)
  return res.data.data
}
