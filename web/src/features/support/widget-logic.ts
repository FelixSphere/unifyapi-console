/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
// Pure decisions behind the floating support chat (./widget.tsx), kept out of
// the component so they can be tested without a DOM.

import type { SupportConversation, SupportMessage, SupportTicket } from './api'

/** How often an open, visible chat re-reads its conversation for replies. */
export const WIDGET_POLL_MS = 10_000

/**
 * Narrowest viewport on which the chat starts open. Below it the open panel
 * covers the whole page, so it starts as the edge tab instead.
 */
export const WIDGET_AUTO_OPEN_MIN_WIDTH = 768

/** localStorage key set when someone closes the chat in this browser. */
export const WIDGET_CLOSED_KEY = 'unifyapi.support-widget.closed'

/** A subject is short in the widget: the inbox row and the email Subject. */
export const WIDGET_SUBJECT_MAX = 60

/**
 * The stored body of a customer's "Talk to a human" request. Must match
 * SupportHandoffBody in controller/unifyapi_support_handoff.go word for word.
 */
export const HANDOFF_BODY = '[handoff] The customer asked to talk to a person.'

/**
 * The subject for a conversation started from the widget: the first non-empty
 * line of the message, whitespace collapsed, at most 60 characters (counted as
 * the server counts them, by code point), with an ellipsis when cut.
 */
export function deriveSubject(body: string): string {
  const line =
    body
      .split(/\r\n|\r|\n/)
      .map((l) => l.replaceAll(/\s+/g, ' ').trim())
      .find((l) => l !== '') ?? ''
  const chars = [...line]
  if (chars.length <= WIDGET_SUBJECT_MAX) {
    return line
  }
  return `${chars
    .slice(0, WIDGET_SUBJECT_MAX - 1)
    .join('')
    .trimEnd()}…`
}

/**
 * Everyone signed in gets the chat, staff included (operator 2026-11-19: staff
 * need it too, as a different party), except on the page that already is
 * support.
 */
export function showWidget(pathname: string, signedIn: boolean): boolean {
  if (!signedIn) return false
  return !(pathname === '/support' || pathname.startsWith('/support/'))
}

/**
 * The chat starts open (operator 2026-11-19) unless this browser closed it
 * before, so closing it is not undone by the next page, and only where the
 * open panel leaves the page usable.
 */
export function initialOpen(
  closedBefore: boolean,
  viewportWidth: number
): boolean {
  return !closedBefore && viewportWidth >= WIDGET_AUTO_OPEN_MIN_WIDTH
}

/** Poll only while someone can see the replies arrive. */
export function shouldPoll(state: {
  open: boolean
  visible: boolean
  ticketId: number | null | undefined
}): boolean {
  return state.open && state.visible && !!state.ticketId
}

/**
 * Which conversation the widget shows: the most recently active one that is
 * not closed. `null` means an empty chat whose first message starts a new one.
 */
export function pickConversation(
  tickets: SupportTicket[] | undefined
): number | null {
  const live = (tickets ?? []).filter((t) => t.status !== 'closed')
  if (live.length === 0) {
    return null
  }
  return live.reduce((a, b) => (b.updated_at > a.updated_at ? b : a)).id
}

export type WidgetMode = 'ai' | 'staff'

/**
 * 'ai': the AI assistant answers first. 'staff': the message waits for a
 * person (AI not configured, or the customer asked for a person and no person
 * has answered since).
 */
export function widgetMode(
  aiEnabled: boolean | undefined,
  messages: SupportMessage[] = []
): WidgetMode {
  if (!aiEnabled) return 'staff'
  return awaitingHuman(messages) ? 'staff' : 'ai'
}

export function isHandoff(m: Pick<SupportMessage, 'from_staff' | 'body'>) {
  return !m.from_staff && m.body === HANDOFF_BODY
}

/** The customer asked for a person, and no person has replied since. */
export function awaitingHuman(messages: SupportMessage[]): boolean {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i]
    if (m.from_staff && !m.from_ai) return false
    if (isHandoff(m)) return true
  }
  return false
}

/**
 * The conversation is a person's and the customer is the one waiting: show
 * that we are still here. True while the newest message is not a person's
 * reply -- the customer's own, the handoff request, or the AI saying it handed
 * over. A conversation that has no message yet has nobody waiting.
 */
export function waitingForPerson(
  mode: WidgetMode,
  messages: SupportMessage[]
): boolean {
  const last = messages[messages.length - 1]
  if (mode !== 'staff' || !last) return false
  return !(last.from_staff && !last.from_ai)
}

/**
 * The customer message the AI should answer now, if any. Same rule as the
 * Support page (useAIAnswer in ./index.tsx): the newest message is the
 * customer's and is fresh -- an older unanswered one has already gone to staff
 * through the two-minute sweep. Never a handoff request, and never while the
 * customer is waiting for a person.
 */
export function messageForAI(
  conversation: SupportConversation | undefined,
  aiEnabled: boolean | undefined,
  nowSeconds: number
): SupportMessage | null {
  if (!conversation || widgetMode(aiEnabled, conversation.messages) !== 'ai') {
    return null
  }
  const last = conversation.messages.at(-1)
  if (!last || last.from_staff || isHandoff(last)) {
    return null
  }
  return nowSeconds - last.created_at < 120 ? last : null
}

/** "Talk to a human" makes sense once there is a conversation to hand over. */
export function canHandOff(conversation: SupportConversation | undefined) {
  if (!conversation || conversation.messages.length === 0) {
    return false
  }
  return !awaitingHuman(conversation.messages)
}
