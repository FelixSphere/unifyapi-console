/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// The notice is rendered for real: what matters is what a visitor sees and
// what Accept/Reject leave behind in cookies, localStorage and HubSpot's queue.

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
const { PRIVACY_POLICY_URL, REJECT_QUIET_PERIOD_MS, REJECT_TIMESTAMP_KEY } =
  await import('../cookie-consent')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const win = domWindow as unknown as Window

function fakeRouter(pathname: string) {
  const listeners = new Set<() => void>()
  const router = {
    state: { location: { pathname } },
    subscribe: (_event: 'onResolved', listener: () => void) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    navigate: (to: string) => {
      router.state = { location: { pathname: to } }
      act(() => listeners.forEach((listener) => listener()))
    },
  }
  return router
}

let unmount: (() => void) | null = null

function render(pathname = '/dashboard') {
  const router = fakeRouter(pathname)
  const container = win.document.createElement('div')
  win.document.body.appendChild(container)
  const root = createRoot(container as unknown as HTMLElement)
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <CookieNotice router={router} />
      </I18nextProvider>
    )
  })
  unmount = () => act(() => root.unmount())
  return router
}

function dialog() {
  return win.document.querySelector('[role="dialog"]')
}

function button(label: string) {
  const match = [...win.document.querySelectorAll('button')].find(
    (element) => element.textContent === label
  )
  assert.ok(match, `no button "${label}"`)
  return match as unknown as HTMLButtonElement
}

// Every test starts from a first visit, whatever ran before it. The cookie is
// expired with a date in the past, not `max-age=0`: happy-dom keeps a
// `max-age=0` cookie readable until the clock ticks over to the next
// millisecond, so on a fast runner the next test saw `localConsent=` and the
// notice stayed hidden.
beforeEach(async () => {
  win.document.body.innerHTML = ''
  win.document.cookie =
    'localConsent=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/'
  assert.equal(win.document.cookie, '')
  win.localStorage.clear()
  delete win._hsq
  await i18n.changeLanguage('en')
})

afterEach(() => {
  unmount?.()
  unmount = null
})

describe('cookie notice', () => {
  test('a first visit sees the FelixSphere notice with a link to the privacy policy', () => {
    render()
    const notice = dialog()
    assert.ok(notice)
    assert.equal(notice.getAttribute('aria-label'), 'Cookie notice')
    assert.equal(
      notice.textContent,
      'Cookie noticeWe use required, functional, and advertising cookies. ' +
        'Reject to keep only essential cookies. See our Privacy Policy.' +
        'Reject cookiesAccept all'
    )
    const link = notice.querySelector('a')
    assert.equal(link?.getAttribute('href'), PRIVACY_POLICY_URL)
    assert.equal(PRIVACY_POLICY_URL, 'https://www.unifyapi.ai/privacy')
  })

  test('Accept sets the one-year consent cookie, clears a reject, opts HubSpot back in', () => {
    win.localStorage.setItem(
      REJECT_TIMESTAMP_KEY,
      String(Date.now() - REJECT_QUIET_PERIOD_MS - 1000)
    )
    render()
    act(() => button('Accept all').click())

    assert.equal(dialog(), null)
    assert.match(win.document.cookie, /(^|; )localConsent=true/)
    assert.equal(win.localStorage.getItem(REJECT_TIMESTAMP_KEY), null)
    assert.deepEqual(win._hsq, [['doNotTrack', { track: true }]])
  })

  test('Reject records the time and tells HubSpot not to track', () => {
    render()
    const before = Date.now()
    act(() => button('Reject cookies').click())

    assert.equal(dialog(), null)
    assert.doesNotMatch(win.document.cookie, /localConsent/)
    const stored = Number(win.localStorage.getItem(REJECT_TIMESTAMP_KEY))
    assert.ok(stored >= before && stored <= Date.now())
    assert.deepEqual(win._hsq, [['doNotTrack']])
  })

  test('Reject does not drop calls already queued for HubSpot', () => {
    win._hsq = [['setPath', '/dashboard']]
    render()
    act(() => button('Reject cookies').click())
    assert.deepEqual(win._hsq, [['setPath', '/dashboard'], ['doNotTrack']])
  })

  test('stays hidden once consent is given', () => {
    win.document.cookie = 'localConsent=true; path=/'
    render()
    assert.equal(dialog(), null)
  })

  test('stays hidden for 12 hours after Reject, then asks again', () => {
    win.localStorage.setItem(
      REJECT_TIMESTAMP_KEY,
      String(Date.now() - REJECT_QUIET_PERIOD_MS + 60_000)
    )
    render()
    assert.equal(dialog(), null)
    unmount?.()

    win.localStorage.setItem(
      REJECT_TIMESTAMP_KEY,
      String(Date.now() - REJECT_QUIET_PERIOD_MS - 60_000)
    )
    render()
    assert.ok(dialog())
  })

  test('never on the routes HubSpot chat stays off, and follows navigation', () => {
    for (const path of [
      '/sign-in',
      '/sign-up',
      '/reset',
      '/setup',
      '/oauth/github',
      '/chat/0',
    ]) {
      render(path)
      assert.equal(dialog(), null, path)
      unmount?.()
    }

    const router = render('/sign-in')
    assert.equal(dialog(), null)
    router.navigate('/dashboard')
    assert.ok(dialog())
    router.navigate('/sign-in')
    assert.equal(dialog(), null)
  })
})
