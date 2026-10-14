/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import i18n from '@/i18n/config'
import { api } from '@/lib/api'

// Customer questions and messages. Server side: controller/unifyapi_support.go.

export type SupportStatus = 'open' | 'answered' | 'closed'

export type SupportTicket = {
  id: number
  user_id: number
  subject: string
  status: SupportStatus
  created_at: number
  updated_at: number
}

export type SupportTicketRow = SupportTicket & {
  username: string
  email: string
}

export type SupportMessage = {
  id: number
  ticket_id: number
  user_id: number
  from_staff: boolean
  body: string
  created_at: number
}

export type SupportConversation = {
  ticket: SupportTicket
  messages: SupportMessage[]
  customer?: { id: number; username: string; email: string }
}

type Envelope<T> = { success: boolean; message: string; data: T }

// The API answers 4xx with a JSON message; surface that text, not axios's.
async function call<T>(p: Promise<{ data: Envelope<T> }>): Promise<T> {
  try {
    const res = await p
    if (!res.data.success) throw new Error(i18n.t(res.data.message))
    return res.data.data
  } catch (err) {
    const msg = (err as { response?: { data?: { message?: string } } })
      ?.response?.data?.message
    // Server messages are fixed English sentences; the Support tables in
    // ./i18n translate the ones a customer can actually hit.
    throw new Error(i18n.t(msg || (err as Error).message))
  }
}

export const listMyTickets = () =>
  call(api.get<Envelope<SupportTicket[]>>('/api/support/tickets')).then(
    (d) => d ?? []
  )

export const createTicket = (subject: string, body: string) =>
  call(
    api.post<Envelope<SupportTicket>>('/api/support/tickets', {
      subject,
      body,
    })
  )

export const getMyTicket = (id: number) =>
  call(api.get<Envelope<SupportConversation>>(`/api/support/tickets/${id}`))

export const addMyMessage = (id: number, body: string) =>
  call(
    api.post<Envelope<SupportConversation>>(
      `/api/support/tickets/${id}/messages`,
      { body }
    )
  )

export const closeMyTicket = (id: number) =>
  call(
    api.post<Envelope<SupportConversation>>(`/api/support/tickets/${id}/close`)
  )

export const listInbox = (status: SupportStatus | '') =>
  call(
    api.get<Envelope<SupportTicketRow[]>>(
      `/api/support/admin/tickets${status ? `?status=${status}` : ''}`
    )
  ).then((d) => d ?? [])

export const getInboxTicket = (id: number) =>
  call(
    api.get<Envelope<SupportConversation>>(`/api/support/admin/tickets/${id}`)
  )

export const replyInbox = (id: number, body: string) =>
  call(
    api.post<Envelope<SupportConversation>>(
      `/api/support/admin/tickets/${id}/messages`,
      { body }
    )
  )

export const setInboxStatus = (id: number, status: SupportStatus) =>
  call(
    api.post<Envelope<SupportConversation>>(
      `/api/support/admin/tickets/${id}/status`,
      { status }
    )
  )
