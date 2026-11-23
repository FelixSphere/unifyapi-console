/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

// Admin email to customers. Every route is AdminAuth on the server; the
// Reply-To setting is root only. See controller/unifyapi_admin_email.go.

export type AdminEmailTemplate = {
  id: number
  builtin_key: string
  name: string
  subject: string
  headline: string
  body: string
  cta_label: string
  cta_url: string
  updated_at: number
}

export type AdminEmailTemplateInput = Pick<
  AdminEmailTemplate,
  'name' | 'subject' | 'headline' | 'body' | 'cta_label' | 'cta_url'
>

export type AdminEmailContent = Omit<AdminEmailTemplateInput, 'name'> & {
  template_id: number
}

export type AdminEmailRecipient = {
  user_id: number
  username: string
  email: string
  status: 'queued' | 'skipped'
  reason?: string
}

export type AdminEmailPreview = {
  subject: string
  html: string
  text: string
  preview_user_id: number
  bulk: boolean
  will_send: number
  skipped: number
  recipients: AdminEmailRecipient[]
  reply_to: string
  smtp_ready: boolean
  unsubscribe_link: boolean
}

export type AdminEmailSendResult = {
  batch_id: string
  queued: number
  skipped: number
  recipients: AdminEmailRecipient[]
}

export type AdminEmailSendRow = {
  id: number
  batch_id: string
  sender_id: number
  template_id: number
  subject: string
  user_id: number
  email: string
  status: 'queued' | 'sent' | 'failed' | 'skipped' | 'interrupted'
  error: string
  created_at: number
  sent_at: number
}

export type AdminEmailTemplatesState = {
  templates: AdminEmailTemplate[]
  variables: string[]
  reply_to: string
  smtp_ready: boolean
  max: number
}

type Envelope<T> = { success: boolean; message?: string; data?: T }

async function unwrap<T>(request: Promise<{ data: Envelope<T> }>): Promise<T> {
  const res = await request
  if (!res.data.success) {
    throw new Error(res.data.message || 'Request failed')
  }
  return res.data.data as T
}

export const getAdminEmailTemplates = () =>
  unwrap<AdminEmailTemplatesState>(api.get('/api/admin-email/templates'))

export const createAdminEmailTemplate = (input: AdminEmailTemplateInput) =>
  unwrap<AdminEmailTemplate>(api.post('/api/admin-email/templates', input))

export const updateAdminEmailTemplate = (
  id: number,
  input: AdminEmailTemplateInput
) =>
  unwrap<AdminEmailTemplate>(api.put(`/api/admin-email/templates/${id}`, input))

export const deleteAdminEmailTemplate = (id: number) =>
  unwrap<null>(api.delete(`/api/admin-email/templates/${id}`))

export const previewAdminEmail = (
  content: AdminEmailContent,
  userIds: number[]
) =>
  unwrap<AdminEmailPreview>(
    api.post(
      '/api/admin-email/preview',
      { ...content, user_ids: userIds },
      { skipErrorHandler: true }
    )
  )

export const sendAdminEmail = (content: AdminEmailContent, userIds: number[]) =>
  unwrap<AdminEmailSendResult>(
    api.post('/api/admin-email/send', { ...content, user_ids: userIds })
  )

export const getAdminEmailSends = (params: {
  user_id?: number
  batch_id?: string
  limit?: number
}) => unwrap<AdminEmailSendRow[]>(api.get('/api/admin-email/sends', { params }))

export const updateAdminEmailReplyTo = (replyTo: string) =>
  unwrap<{ reply_to: string }>(
    api.put('/api/admin-email/settings', { reply_to: replyTo })
  )
