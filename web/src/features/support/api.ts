/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import i18n from '@/i18n/config'
import { api, getFreshAuthHeaders } from '@/lib/api'

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
  /** Written by the AI support agent (it is also from_staff). */
  from_ai?: boolean
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

export type SupportUnread = { count: number; ticket_ids: number[] }

// Conversations with a message from the other side the caller has not opened.
export const getUnread = () =>
  call(api.get<Envelope<SupportUnread>>('/api/support/unread'))

// The AI first responder (server: controller/unifyapi_support_ai.go).
export const getAIStatus = () =>
  call(api.get<Envelope<{ enabled: boolean }>>('/api/support/ai'))

export type AIEvent =
  | { event: 'status' }
  | { event: 'tool'; name: string; ok: boolean }
  | { event: 'answer'; text: string }
  | { event: 'handoff'; reason: string }
  | { event: 'done' }
  | { event: 'error'; message: string }
  | { event: 'stored'; ok: boolean }

/**
 * Asks the agent to answer the latest customer message and reports its
 * progress. Resolves when the server has stored the reply (or given up and
 * passed the conversation to staff). A 409 means it was already answered.
 */
export async function streamAI(
  ticketId: number,
  onEvent: (e: AIEvent) => void,
  signal?: AbortSignal
): Promise<'stored' | 'failed' | 'skipped'> {
  const headers = await getFreshAuthHeaders()
  const res = await fetch(`/api/support/tickets/${ticketId}/ai`, {
    method: 'POST',
    headers,
    signal,
  })
  if (res.status === 409 || res.status === 404) return 'skipped'
  if (!res.ok || !res.body) return 'failed'
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let outcome: 'stored' | 'failed' = 'failed'
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let cut: number
    while ((cut = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, cut)
      buf = buf.slice(cut + 2)
      const event = /^event: (.*)$/m.exec(block)?.[1]
      const data = /^data: (.*)$/m.exec(block)?.[1]
      if (!event) continue
      let payload: Record<string, unknown> = {}
      try {
        payload = data ? JSON.parse(data) : {}
      } catch {
        payload = {}
      }
      const e = { event, ...payload } as AIEvent
      if (e.event === 'stored') outcome = e.ok ? 'stored' : 'failed'
      onEvent(e)
    }
  }
  return outcome
}
