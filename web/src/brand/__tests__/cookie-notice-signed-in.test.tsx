/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// Operator decision, 2026-10-02: a signed-in user never sees the cookie
// notice. Rendered for real against the real auth store, as main.tsx mounts it.

import { afterEach, beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { Window as HappyDomWindow } from 'happy-dom'

const domWindow = new HappyDomWindow({
  url: 'https://console.unifyapi.ai/dashboard',
})
for (const key of [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'localStorage',
] as const) {
  try {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      writable: true,
      value: (domWindow as unknown as Record<string, unknown>)[key],
    })
  } catch {
    /* already provided by the runtime */
  }
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { CookieNotice } = await import('../cookie-notice')
const { useAuthStore } = await import('@/stores/auth-store')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const win = domWindow as unknown as Window
const auth = () => useAuthStore.getState().auth

const signedIn = {
  access_token: 'test-access-token',
  token_type: 'Bearer',
  access_expires_at: Math.floor(Date.now() / 1000) + 3600,
  user: { id: 7, username: 'visitor', role: 1 },
  session: {
    sid: 'test-sid',
    current: true,
    login_method: 'password',
    ip: '127.0.0.1',
    user_agent: 'test',
    created_at: 0,
    last_active_at: 0,
    expires_at: 0,
  },
}

const router = {
  state: { location: { pathname: '/dashboard' } },
  subscribe: () => () => undefined,
}

let unmount: (() => void) | null = null

function render() {
  const container = win.document.createElement('div')
  win.document.body.appendChild(container)
  const root = createRoot(container as unknown as HTMLElement)
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <CookieNotice router={router} auth={useAuthStore} />
      </I18nextProvider>
    )
  })
  unmount = () => act(() => root.unmount())
}

function dialog() {
  return win.document.querySelector('[role="dialog"]')
}

beforeEach(() => {
  win.document.body.innerHTML = ''
  win.document.cookie =
    'localConsent=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'
  win.localStorage.clear()
  act(() => auth().reset('idle'))
})

afterEach(() => {
  unmount?.()
  unmount = null
})

describe('cookie notice for signed-in users', () => {
  test('a signed-in user never sees it', () => {
    act(() => auth().setBundle(signedIn))
    render()
    assert.equal(dialog(), null)
  })

  test('nothing shows until auth has answered', () => {
    render()
    assert.equal(dialog(), null)
    act(() => auth().reset('complete'))
    assert.ok(dialog())
  })

  test('a signed-out visitor sees it as before', () => {
    act(() => auth().reset('complete'))
    render()
    assert.ok(dialog())
  })

  test('it goes away when the visitor signs in, and may return on sign-out', () => {
    act(() => auth().reset('complete'))
    render()
    assert.ok(dialog())
    act(() => auth().setBundle(signedIn))
    assert.equal(dialog(), null)
    act(() => auth().reset('complete'))
    assert.ok(dialog())
  })
})
