/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { afterAll, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { Window } from 'happy-dom'

const domWindow = new Window()
// Expose the whole happy-dom window as the global environment: the column
// cells reach for customElements, matchMedia, HTMLElement subclasses and
// more through Base UI, and enumerating them by hand breaks on every new
// import. Anything happy-dom lacks gets an inert stand-in below.
{
  const seen = new Set<string>(['undefined', 'NaN', 'Infinity', 'globalThis'])
  let proto: object | null = domWindow
  while (proto && proto !== Object.prototype) {
    for (const key of Object.getOwnPropertyNames(proto)) {
      if (seen.has(key) || key in globalThis) continue
      seen.add(key)
      try {
        const value = (domWindow as unknown as Record<string, unknown>)[key]
        Object.defineProperty(globalThis, key, { configurable: true, value })
      } catch {
        // accessor that throws off-window; skip
      }
    }
    proto = Object.getPrototypeOf(proto)
  }
  for (const key of ['window', 'document', 'navigator'] as const) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: (domWindow as unknown as Record<string, unknown>)[key],
    })
  }
}
const inertMatchMedia = () => ({
  matches: false,
  media: '',
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: inertMatchMedia,
})
Object.defineProperty(domWindow, 'matchMedia', {
  configurable: true,
  value: inertMatchMedia,
})
if (!('ResizeObserver' in globalThis)) {
  Object.defineProperty(globalThis, 'ResizeObserver', {
    configurable: true,
    value: class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { AdminEmailContext } = await import('../compose-context')
const { UsernameEmailButton } = await import('../username-button')
type User = import('../../types').User
type ComposeRecipient = import('../compose-context').ComposeRecipient

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })
;(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true

const user = {
  id: 33,
  username: 'builder_7WGZcT0c9Q5w',
  display_name: 'Builder',
  email: 'founder@acme-robotics.example',
} as User

async function render(node: React.ReactNode) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<I18nextProvider i18n={i18n}>{node}</I18nextProvider>)
  })
  return {
    container,
    cleanup: async () => {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

// The username in the admin Users table opens "Send email" for that user.
// Outside the Users page (no provider) it must stay plain text, never a
// button that does nothing; and clicking it must not also act on the row.
describe('Users table username opens Send email', () => {
  afterAll(() => domWindow.close())

  test('without the provider the username is plain text', async () => {
    const { container, cleanup } = await render(
      <UsernameEmailButton user={user} />
    )
    assert.equal(container.querySelector('button'), null)
    assert.ok(container.textContent?.includes(user.username))
    await cleanup()
  })

  test('with the provider, clicking opens compose for that one user', async () => {
    const opened: ComposeRecipient[][] = []
    let rowClicks = 0
    const { container, cleanup } = await render(
      <AdminEmailContext.Provider
        value={{
          openCompose: (r) => opened.push(r),
          openTemplates: () => {},
        }}
      >
        <div onClick={() => rowClicks++}>
          <UsernameEmailButton user={user} />
        </div>
      </AdminEmailContext.Provider>
    )
    const button = container.querySelector<HTMLButtonElement>(
      '[data-admin-email-username]'
    )
    assert.ok(button, 'the username is a button')
    assert.ok(button.getAttribute('aria-label')?.includes(user.username))
    await act(async () => button.click())
    assert.equal(opened.length, 1)
    assert.deepEqual(
      opened[0].map((r) => r.id),
      [33]
    )
    assert.equal(rowClicks, 0, 'the click does not reach the row')
    await cleanup()
  })
})
