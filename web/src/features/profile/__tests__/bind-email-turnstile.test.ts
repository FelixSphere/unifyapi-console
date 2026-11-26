/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// Reported from production on 2026-11-26: a customer could not change their
// email. GET /api/verification sits behind TurnstileCheck, and the Bind Email
// dialog asked for the code without a token, so with Turnstile on every Send
// answered "Turnstile token 为空" and no code was ever mailed.
//
// Source position is render position here for the same reason given in
// features/auth/__tests__/turnstile-precedes-gated-controls.test.ts: the
// widget and the Send button are siblings in one column with no ordering CSS.

const source = readFileSync(
  join(import.meta.dir, '..', 'components', 'dialogs', 'email-bind-dialog.tsx'),
  'utf8'
)

describe('Bind Email dialog and Turnstile', () => {
  test('the code request carries the Turnstile token', () => {
    expect(source).toContain('sendEmailVerification(email, turnstileToken)')
    expect(source).not.toMatch(/sendEmailVerification\(email\)/)
  })

  test('the challenge renders above the Send button it unlocks', () => {
    const jsx = source.indexOf('return (')
    const widget = source.indexOf('<Turnstile', jsx)
    const gated = source.indexOf('!turnstileReady', jsx)
    expect(widget).toBeGreaterThan(-1)
    expect(gated).toBeGreaterThan(-1)
    expect(widget).toBeLessThan(gated)
  })

  test('a used token is dropped so a resend gets a fresh challenge', () => {
    expect(source).toContain('setTurnstileWidgetKey((key) => key + 1)')
    expect(source).toContain('key={turnstileWidgetKey}')
  })
})
