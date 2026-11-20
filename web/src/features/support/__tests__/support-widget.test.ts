/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * The floating support chat's decisions: what a new conversation is called,
 * who sees the widget, when it polls, which conversation it opens, and when
 * the AI assistant answers. Plus the same translation guard the Support page
 * has, applied to widget.tsx.
 */
import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import type { SupportMessage, SupportTicket } from '../api'
import { SUPPORT_KEYS } from '../i18n'
import {
  awaitingHuman,
  canHandOff,
  deriveSubject,
  HANDOFF_BODY,
  messageForAI,
  pickConversation,
  shouldPoll,
  initialOpen,
  showWidget,
  WIDGET_AUTO_OPEN_MIN_WIDTH,
  WIDGET_POLL_MS,
  widgetMode,
} from '../widget-logic'

let nextId = 1
function msg(p: Partial<SupportMessage>): SupportMessage {
  return {
    id: nextId++,
    ticket_id: 1,
    user_id: 5,
    from_staff: false,
    body: 'hi',
    created_at: 1000,
    ...p,
  }
}
const customer = (body = 'hi', at = 1000) => msg({ body, created_at: at })
const ai = (at = 1000) =>
  msg({ from_staff: true, from_ai: true, created_at: at })
const staff = (at = 1000) => msg({ from_staff: true, created_at: at })
const handoff = (at = 1000) => msg({ body: HANDOFF_BODY, created_at: at })
const conv = (messages: SupportMessage[]) => ({
  ticket: {
    id: 1,
    user_id: 5,
    subject: 's',
    status: 'open' as const,
    created_at: 0,
    updated_at: 0,
  },
  messages,
})

describe('deriveSubject', () => {
  test('the first line, trimmed', () => {
    expect(deriveSubject('  My key returns 401  \nmore detail')).toBe(
      'My key returns 401'
    )
  })
  test('skips blank leading lines and collapses whitespace', () => {
    expect(deriveSubject('\n\n  a\t\tb   c\r\nrest')).toBe('a b c')
  })
  test('at most 60 characters, with an ellipsis when cut', () => {
    const long = 'x'.repeat(100)
    const s = deriveSubject(long)
    expect([...s].length).toBe(60)
    expect(s.endsWith('…')).toBe(true)
    expect(deriveSubject('y'.repeat(60))).toBe('y'.repeat(60))
  })
  test('counts code points like the server, not UTF-16 units', () => {
    const s = deriveSubject('😀'.repeat(70))
    expect([...s].length).toBe(60)
    expect(s).not.toContain('�')
  })
  test('never contains a newline', () => {
    expect(deriveSubject('a\r\nb')).toBe('a')
  })
})

describe('showWidget', () => {
  test('everyone signed in sees it on every page but /support', () => {
    expect(showWidget('/dashboard/overview', true)).toBe(true)
    expect(showWidget('/support', false)).toBe(false)
    expect(showWidget('/support', true)).toBe(false)
    expect(showWidget('/support/', true)).toBe(false)
    expect(showWidget('/supported-models', true)).toBe(true)
  })
  test('signed out, nobody sees it', () => {
    expect(showWidget('/dashboard/overview', false)).toBe(false)
  })
})

describe('initialOpen', () => {
  test('starts open on a wide screen that never closed it', () => {
    expect(initialOpen(false, WIDGET_AUTO_OPEN_MIN_WIDTH)).toBe(true)
    expect(initialOpen(false, 1440)).toBe(true)
  })
  test('stays closed once this browser closed it', () => {
    expect(initialOpen(true, 1440)).toBe(false)
  })
  test('starts as the tab where the panel would cover the page', () => {
    expect(initialOpen(false, WIDGET_AUTO_OPEN_MIN_WIDTH - 1)).toBe(false)
    expect(initialOpen(false, 375)).toBe(false)
  })
})

describe('shouldPoll', () => {
  test('only while open, visible and showing a conversation', () => {
    expect(shouldPoll({ open: true, visible: true, ticketId: 3 })).toBe(true)
    expect(shouldPoll({ open: false, visible: true, ticketId: 3 })).toBe(false)
    expect(shouldPoll({ open: true, visible: false, ticketId: 3 })).toBe(false)
    expect(shouldPoll({ open: true, visible: true, ticketId: null })).toBe(
      false
    )
  })
  test('every 10 seconds, not faster', () => {
    expect(WIDGET_POLL_MS).toBe(10_000)
  })
})

describe('pickConversation', () => {
  const t = (id: number, status: SupportTicket['status'], updated: number) =>
    ({ id, status, updated_at: updated }) as SupportTicket
  test('the most recently active unclosed one', () => {
    expect(
      pickConversation([
        t(1, 'answered', 5),
        t(2, 'open', 9),
        t(3, 'closed', 20),
      ])
    ).toBe(2)
  })
  test('none when everything is closed or there is nothing', () => {
    expect(pickConversation([t(1, 'closed', 5)])).toBeNull()
    expect(pickConversation(undefined)).toBeNull()
  })
})

describe('mode and the AI assistant', () => {
  test('AI off: always a chat with staff', () => {
    expect(widgetMode(false, [])).toBe('staff')
    expect(widgetMode(undefined, [])).toBe('staff')
    expect(messageForAI(conv([customer('q', 1000)]), false, 1001)).toBeNull()
  })
  test('AI on: answers a fresh customer message, once there is one', () => {
    expect(widgetMode(true, [])).toBe('ai')
    const last = customer('q', 1000)
    expect(messageForAI(conv([last]), true, 1010)?.id).toBe(last.id)
  })
  test('not a stale message: that one already went to staff', () => {
    expect(messageForAI(conv([customer('q', 1000)]), true, 1120)).toBeNull()
  })
  test('not when the newest message is a reply', () => {
    expect(messageForAI(conv([customer(), ai()]), true, 1001)).toBeNull()
  })
  test('a handoff hands the conversation to people until one replies', () => {
    const asked = [customer(), ai(), handoff(), customer('still there?', 1000)]
    expect(awaitingHuman(asked)).toBe(true)
    expect(widgetMode(true, asked)).toBe('staff')
    expect(messageForAI(conv(asked), true, 1001)).toBeNull()
    expect(messageForAI(conv([customer(), handoff()]), true, 1001)).toBeNull()
    const answered = [...asked, staff(), customer('thanks', 1000)]
    expect(widgetMode(true, answered)).toBe('ai')
    expect(messageForAI(conv(answered), true, 1001)).not.toBeNull()
  })
  test('an AI reply does not count as a person answering', () => {
    expect(awaitingHuman([handoff(), ai()])).toBe(true)
  })
  test('"Talk to a human" is offered once there is something to hand over', () => {
    expect(canHandOff(undefined)).toBe(false)
    expect(canHandOff(conv([]))).toBe(false)
    expect(canHandOff(conv([customer()]))).toBe(true)
    expect(canHandOff(conv([customer(), handoff()]))).toBe(false)
  })
})

const repo = join(import.meta.dir, '..', '..', '..', '..', '..')

test('the handoff marker matches the Go source word for word', () => {
  const go = readFileSync(
    join(repo, 'controller/unifyapi_support_handoff.go'),
    'utf8'
  )
  expect(go).toContain(`SupportHandoffBody = "${HANDOFF_BODY}"`)
})

test('every string the widget translates has a table entry', () => {
  const src = readFileSync(join(import.meta.dir, '..', 'widget.tsx'), 'utf8')
  const found = new Set<string>()
  for (const m of src.matchAll(/\bt\(\s*'((?:[^'\\]|\\.)*)'/g)) {
    found.add(m[1])
  }
  // Progress labels are passed to t() by variable.
  for (const m of src.matchAll(/^\s*\w+: '((?:[^'\\]|\\.)*…)',?$/gm)) {
    found.add(m[1])
  }
  expect(found.size).toBeGreaterThan(15)
  const missing = [...found].filter(
    (s) => !(SUPPORT_KEYS as readonly string[]).includes(s)
  )
  expect(missing).toEqual([])
})
