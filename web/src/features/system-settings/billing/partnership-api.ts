/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

export type PartnershipProgram = {
  id: number
  name: string
  code: string
  group: string
  grant_quota: number
  grant_limit: number
  claimed_count: number
  enabled: boolean
  starts_at: number
  ends_at: number
  created_at: number
  updated_at: number
}

export type PartnershipProgramInput = Omit<
  PartnershipProgram,
  'id' | 'claimed_count' | 'created_at' | 'updated_at'
>

type Envelope<T> = { success: boolean; message: string; data: T }

export async function getPartnershipPrograms() {
  const response = await api.get<
    Envelope<{
      programs: PartnershipProgram[]
      group_ratios: Record<string, number>
      groups: Record<string, number>
    }>
  >('/api/partnership/')
  if (!response.data.success) throw new Error(response.data.message)
  return response.data.data
}

export async function savePartnershipProgram(input: {
  id?: number
  program: PartnershipProgramInput
}) {
  const response = input.id
    ? await api.put<Envelope<PartnershipProgram>>(
        `/api/partnership/${input.id}`,
        input.program
      )
    : await api.post<Envelope<PartnershipProgram>>(
        '/api/partnership/',
        input.program
      )
  if (!response.data.success) throw new Error(response.data.message)
  return response.data
}
