/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

export type TenantOverview = {
  tenant_id: number
  name: string
  slug: string
  status: number
  group: string
  created_at: number
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
