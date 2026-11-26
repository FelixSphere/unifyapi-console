/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import type { AdminEmailContent, AdminEmailTemplate } from './api'

export const BLANK_CONTENT: AdminEmailContent = {
  template_id: 0,
  subject: '',
  headline: '',
  body: '',
  cta_label: '',
  cta_url: '',
}

export function contentFromTemplate(
  template: AdminEmailTemplate | undefined
): AdminEmailContent {
  if (!template) return { ...BLANK_CONTENT }
  return {
    template_id: template.id,
    subject: template.subject,
    headline: template.headline,
    body: template.body,
    cta_label: template.cta_label,
    cta_url: template.cta_url,
  }
}

// A message that asks the customer to write back is useless while replies
// go to the noreply sender. The warning is louder when the text says so.
export type ReplyToWarning = 'none' | 'notice' | 'blocking'

export function replyToWarning(
  replyTo: string,
  content: Pick<AdminEmailContent, 'subject' | 'body'>
): ReplyToWarning {
  if (replyTo.trim()) return 'none'
  const text = `${content.subject}\n${content.body}`.toLowerCase()
  return /\breply\b|\brespond\b|write (back|to us)/.test(text)
    ? 'blocking'
    : 'notice'
}

export function hasContent(content: AdminEmailContent): boolean {
  return content.subject.trim() !== '' && content.body.trim() !== ''
}
