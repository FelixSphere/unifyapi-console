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
const { QueryClient, QueryClientProvider } = await import('@tanstack/react-query')
const { useUsersColumns } = await import('../users-columns')
type User = import('../../types').User
type ColumnDef = import('@tanstack/react-table').ColumnDef<User>

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: { Email: 'Email', Username: 'Username' } } },
})
;(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true

// The columns hook asks the API which addresses the mail server last
// refused. That lookup is not what this file is about: give it a client
// that never fetches, so the column definitions render in isolation.
const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false, enabled: false } },
})

const Providers = ({ children }: { children: React.ReactNode }) => (
  <QueryClientProvider client={queryClient}>
    <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
  </QueryClientProvider>
)

const user = (over: Partial<User> = {}): User =>
  ({
    id: 33,
    username: 'builder_7WGZcT0c9Q5w',
    display_name: 'Builder',
    role: 1,
    status: 1,
    group: 'default',
    quota: 0,
    used_quota: 0,
    request_count: 0,
    aff_code: '',
    aff_count: 0,
    aff_quota: 0,
    aff_history_quota: 0,
    inviter_id: 0,
    ...over,
  }) as User

// Renders one column's cell for one user, the way the table would, and
// returns what a super admin reading that cell sees.
async function renderCell(column: ColumnDef, u: User) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const row = {
    original: u,
    getValue: (key: string) => (u as unknown as Record<string, unknown>)[key],
  }
  const cell = column.cell
  assert.equal(typeof cell, 'function', 'the column renders a cell')
  const Cell = () =>
    (cell as (props: unknown) => React.ReactNode)({ row, column, table: {} })
  await act(async () => {
    root.render(
      <Providers>
        <Cell />
      </Providers>
    )
  })
  const text = container.textContent ?? ''
  await act(async () => root.unmount())
  container.remove()
  return text
}

async function columns(): Promise<ColumnDef[]> {
  let captured: ColumnDef[] = []
  const Probe = () => {
    captured = useUsersColumns()
    return null
  }
  const container = document.createElement('div')
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <Providers>
        <Probe />
      </Providers>
    )
  })
  await act(async () => root.unmount())
  return captured
}

const keyOf = (c: ColumnDef) =>
  (c as { accessorKey?: string }).accessorKey ?? c.id

async function emailColumn(): Promise<ColumnDef> {
  const column = (await columns()).find((c) => keyOf(c) === 'email')
  assert.ok(column, 'the users table has an email column')
  return column
}

// The admin Users table is where a super admin identifies who an account
// belongs to. The API already returns each user's email to admins; the
// table must show it, right beside the username, and say plainly when an
// account has none instead of leaving a blank the eye reads as "hidden".
describe('Users table email column', () => {
  afterAll(() => domWindow.close())

  test('an Email column sits directly after Username', async () => {
    const keys = (await columns()).map(keyOf)
    const username = keys.indexOf('username')
    assert.ok(username >= 0)
    assert.equal(keys[username + 1], 'email')
  })

  test("shows the user's email verbatim", async () => {
    const email = await emailColumn()
    const text = await renderCell(
      email,
      user({ email: 'founder@acme-robotics.example' })
    )
    assert.ok(text.includes('founder@acme-robotics.example'), text)
  })

  test('an account without an email shows a dash, not an empty cell', async () => {
    const email = await emailColumn()
    assert.equal((await renderCell(email, user({ email: '' }))).trim(), '—')
    assert.equal(
      (await renderCell(email, user({ email: undefined }))).trim(),
      '—'
    )
  })
})
