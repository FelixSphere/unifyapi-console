/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// Operator, 2026-11-22: a filter chip such as "Status: Enabled" on the Users
// list is set once and stays. These tests mount the real table hook, the way a
// table does, and leave and re-enter the page through a link that carries no
// search params -- which is what the sidebar does and what used to drop it.

import { afterEach, beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import type { ColumnFiltersState, OnChangeFn } from '@tanstack/react-table'
import { Window as HappyDomWindow } from 'happy-dom'

const domWindow = new HappyDomWindow({
  url: 'https://console.unifyapi.ai/users',
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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { useTableUrlState } = await import('@/hooks/use-table-url-state')
const { useAuthStore } = await import('@/stores/auth-store')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const win = domWindow as unknown as Window

type Search = Record<string, unknown>
type FilterConfigs = NonNullable<
  Parameters<typeof useTableUrlState>[0]['columnFilters']
>

// The Users list's own filters.
const USERS_FILTERS: FilterConfigs = [
  { columnId: 'status', searchKey: 'status', type: 'array' },
  { columnId: 'role', searchKey: 'role', type: 'array' },
  { columnId: 'group', searchKey: 'group', type: 'string' },
]

type Page = {
  filters: () => ColumnFiltersState
  search: () => Search
  setFilters: (next: ColumnFiltersState) => void
  leave: () => void
}

let mounted: Array<() => void> = []

/** Opens a page holding one table, at the given URL search params. */
function openPage(search: Search, configs = USERS_FILTERS): Page {
  let current: ColumnFiltersState = []
  let currentSearch: Search = search
  let change: OnChangeFn<ColumnFiltersState> = () => {}

  function Table() {
    const [urlSearch, setUrlSearch] = useState<Search>(search)
    currentSearch = urlSearch
    const state = useTableUrlState({
      search: urlSearch,
      navigate: ({ search: next }) => {
        if (typeof next === 'function') setUrlSearch((prev) => next(prev))
      },
      columnFilters: configs,
    })
    current = state.columnFilters
    change = state.onColumnFiltersChange
    return null
  }

  const container = win.document.createElement('div')
  win.document.body.appendChild(container)
  const root = createRoot(container)
  act(() => root.render(<Table />))

  const leave = () => {
    act(() => root.unmount())
    container.remove()
  }
  mounted.push(leave)

  return {
    filters: () => current,
    search: () => currentSearch,
    setFilters: (next) => act(() => change(next)),
    leave,
  }
}

function signIn(id: number) {
  useAuthStore.setState((state) => ({
    auth: {
      ...state.auth,
      user: { id, username: `user-${id}`, role: 100 },
    },
  }))
}

function goTo(path: string) {
  win.history.replaceState(null, '', path)
}

beforeEach(() => {
  win.localStorage.clear()
  goTo('/users')
  signIn(1)
})

afterEach(() => {
  for (const leave of mounted) {
    try {
      leave()
    } catch {
      /* already left */
    }
  }
  mounted = []
})

describe('a table remembers the filter chips its viewer set', () => {
  test('a status filter is still there after leaving and coming back', () => {
    const first = openPage({})
    assert.deepEqual(first.filters(), [])
    first.setFilters([{ id: 'status', value: ['1'] }])
    assert.deepEqual(first.search().status, ['1'])
    first.leave()

    const back = openPage({})
    assert.deepEqual(back.filters(), [{ id: 'status', value: ['1'] }])
  })

  test('two chips are both kept, and removing one keeps the other', () => {
    const first = openPage({})
    first.setFilters([
      { id: 'status', value: ['1'] },
      { id: 'role', value: ['10'] },
    ])
    first.leave()

    const second = openPage({})
    assert.deepEqual(second.filters(), [
      { id: 'status', value: ['1'] },
      { id: 'role', value: ['10'] },
    ])
    second.setFilters([{ id: 'role', value: ['10'] }])
    second.leave()

    assert.deepEqual(openPage({}).filters(), [{ id: 'role', value: ['10'] }])
  })

  test('a recalled chip is written to the URL with the next change', () => {
    const first = openPage({})
    first.setFilters([{ id: 'status', value: ['1'] }])
    first.leave()

    const back = openPage({})
    back.setFilters([...back.filters(), { id: 'role', value: ['100'] }])
    assert.deepEqual(back.search().status, ['1'])
    assert.deepEqual(back.search().role, ['100'])
  })

  test('Reset forgets it: a cleared filter does not come back', () => {
    const first = openPage({})
    first.setFilters([{ id: 'status', value: ['2'] }])
    first.setFilters([])
    assert.deepEqual(first.filters(), [])
    first.leave()

    assert.deepEqual(openPage({}).filters(), [])
    assert.equal(win.localStorage.length, 0)
  })

  test('a link that names a filter is shown as written, nothing added', () => {
    const first = openPage({})
    first.setFilters([{ id: 'status', value: ['1'] }])
    first.leave()

    const linked = openPage({ group: 'Kingdee' })
    assert.deepEqual(linked.filters(), [{ id: 'group', value: 'Kingdee' }])
    linked.leave()

    const other = openPage({ status: ['2'] })
    assert.deepEqual(other.filters(), [{ id: 'status', value: ['2'] }])
  })

  test('free-text filters are one-off lookups and are not remembered', () => {
    const first = openPage({})
    first.setFilters([{ id: 'group', value: 'Kingdee' }])
    assert.equal(first.search().group, 'Kingdee')
    first.leave()

    assert.deepEqual(openPage({}).filters(), [])
  })

  test('a filter that reads its own default is left to the table', () => {
    // Channels status and Usage Logs type bring their own `deserialize`.
    const configs: FilterConfigs = [
      {
        columnId: 'status',
        searchKey: 'status',
        type: 'array',
        deserialize: (value) => value ?? ['enabled'],
      },
      { columnId: 'type', searchKey: 'type', type: 'array' },
    ]
    const first = openPage({}, configs)
    assert.deepEqual(first.filters(), [{ id: 'status', value: ['enabled'] }])
    first.setFilters([
      { id: 'status', value: ['disabled'] },
      { id: 'type', value: ['14'] },
    ])
    first.leave()

    assert.deepEqual(openPage({}, configs).filters(), [
      { id: 'status', value: ['enabled'] },
      { id: 'type', value: ['14'] },
    ])
  })

  test('it is per page and per signed-in user', () => {
    const first = openPage({})
    first.setFilters([{ id: 'status', value: ['1'] }])
    first.leave()

    goTo('/redemption-codes')
    const elsewhere = openPage({})
    assert.deepEqual(elsewhere.filters(), [])
    elsewhere.leave()

    goTo('/users')
    signIn(2)
    const someoneElse = openPage({})
    assert.deepEqual(someoneElse.filters(), [])
    someoneElse.leave()

    signIn(1)
    assert.deepEqual(openPage({}).filters(), [{ id: 'status', value: ['1'] }])
  })

  test('damaged storage is ignored, and the table still works', () => {
    for (const damaged of [
      'not json',
      '[]',
      '{"status":"1"}',
      '{"status":[]}',
      '{"status":[{"x":1}]}',
    ]) {
      win.localStorage.setItem('unifyapi:table-filters:1:/users', damaged)
      const page = openPage({})
      assert.deepEqual(page.filters(), [], damaged)
      page.setFilters([{ id: 'role', value: ['1'] }])
      assert.deepEqual(page.filters(), [{ id: 'role', value: ['1'] }])
      page.leave()
      win.localStorage.clear()
    }
  })
})
