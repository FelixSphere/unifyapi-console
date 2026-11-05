/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

export type PlatformEvent = {
  id: number
  created_at: number
  source: string
  kind: string
  severity: string
  client_ip: string
  key_prefix: string
  method: string
  path: string
  status: number
  user_agent: string
  tls_protocol: string
  tls_cipher: string
  request_id: string
  detail: string
}

export type PlatformEventFilters = {
  source?: string
  kind?: string
  clientIp?: string
  keyPrefix?: string
  status?: number
  startTimestamp?: number
  endTimestamp?: number
  page?: number
  pageSize?: number
}

export type IPOwner = {
  user_id: number
  username: string
  last_seen: number
}

type Envelope<T> = { success: boolean; message: string; data: T }
type Page = {
  items: PlatformEvent[] | null
  total: number
  ip_owners: Record<string, IPOwner> | null
}

export async function getPlatformEvents(filters: PlatformEventFilters = {}) {
  const search = new URLSearchParams()
  if (filters.source) search.set('source', filters.source)
  if (filters.kind) search.set('kind', filters.kind)
  if (filters.clientIp) search.set('client_ip', filters.clientIp)
  if (filters.keyPrefix) search.set('key_prefix', filters.keyPrefix)
  if (filters.status) search.set('status', String(filters.status))
  if (filters.startTimestamp) {
    search.set('start_timestamp', String(filters.startTimestamp))
  }
  if (filters.endTimestamp) {
    search.set('end_timestamp', String(filters.endTimestamp))
  }
  search.set('p', String(filters.page ?? 1))
  search.set('page_size', String(filters.pageSize ?? 50))

  const res = await api.get<Envelope<Page>>(
    `/api/platform-log/?${search.toString()}`
  )
  if (!res.data.success) {
    throw new Error(res.data.message || 'failed to load the platform log')
  }
  // An empty page serialises items as null rather than [], and every caller
  // here maps over it.
  return {
    items: res.data.data.items ?? [],
    total: res.data.data.total,
    ipOwners: res.data.data.ip_owners ?? {},
  }
}
