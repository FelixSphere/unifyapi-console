/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  HUBSPOT_SCRIPT_ID,
  HUBSPOT_SCRIPT_SRC,
  shouldShowHubSpotChat,
  syncHubSpotChat,
} from '../hubspot-chat'

type FakeScript = { id: string; src: string; async: boolean; defer: boolean }

function fakeWindow(options: { framed?: boolean; pathname?: string } = {}) {
  const appended: FakeScript[] = []
  const calls: string[] = []
  let loaded = false
  const win = {
    self: {},
    top: {},
    location: { pathname: options.pathname ?? '/dashboard' },
    hsConversationsOnReady: undefined as Array<() => void> | undefined,
    HubSpotConversations: undefined as unknown,
    document: {
      querySelector: (selector: string) =>
        appended.find((script) => `#${script.id}` === selector) ?? null,
      createElement: () => ({ id: '', src: '', async: false, defer: false }),
      body: { appendChild: (script: FakeScript) => appended.push(script) },
    },
  }
  if (!options.framed) win.top = win.self
  const widget = {
    load: () => {
      calls.push('load')
      loaded = true
    },
    refresh: () => calls.push('refresh'),
    remove: () => {
      calls.push('remove')
      loaded = false
    },
    status: () => ({ loaded }),
  }
  const scriptArrives = () => {
    win.HubSpotConversations = { widget }
    loaded = true
    for (const callback of win.hsConversationsOnReady ?? []) callback()
  }
  return {
    win: win as unknown as Window,
    raw: win,
    appended,
    calls,
    scriptArrives,
  }
}

describe('HubSpot live chat in the console', () => {
  test('loads the FelixSphere portal loader exactly once, async and deferred', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/dashboard')
    syncHubSpotChat(fake.win, '/keys')
    assert.equal(fake.appended.length, 1)
    assert.deepEqual(fake.appended[0], {
      id: HUBSPOT_SCRIPT_ID,
      src: 'https://js.hs-scripts.com/46127314.js',
      async: true,
      defer: true,
    })
    assert.equal(HUBSPOT_SCRIPT_SRC, 'https://js.hs-scripts.com/46127314.js')
    assert.equal(HUBSPOT_SCRIPT_ID, 'hs-script-loader')
  })

  test('refreshes the widget on a later route change instead of reloading it', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/pricing')
    fake.scriptArrives()
    syncHubSpotChat(fake.win, '/dashboard')
    assert.deepEqual(fake.calls, ['refresh'])
  })

  test('shows on public pages and on the logged-in product', () => {
    const fake = fakeWindow()
    for (const path of [
      '/',
      '/pricing',
      '/dashboard',
      '/keys',
      '/wallet',
      '/ops',
    ]) {
      assert.equal(shouldShowHubSpotChat(fake.win, path), true, path)
    }
  })

  test('stays off the OAuth popup, embedded chat clients, setup and auth pages', () => {
    const fake = fakeWindow()
    for (const path of [
      '/oauth/github',
      '/chat/0',
      '/chat2link',
      '/setup',
      '/sign-in',
      '/sign-up',
      '/register',
      '/forgot-password',
      '/reset',
      '/user/reset',
    ]) {
      assert.equal(shouldShowHubSpotChat(fake.win, path), false, path)
    }
    // Segment-bounded: a route merely starting with the same letters still shows.
    assert.equal(shouldShowHubSpotChat(fake.win, '/chatter'), true)
  })

  test('never injects when the first page is a hidden route', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/oauth/github')
    assert.equal(fake.appended.length, 0)
  })

  test('removes the widget on a hidden route and brings it back afterwards', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/dashboard')
    fake.scriptArrives()
    syncHubSpotChat(fake.win, '/chat/1')
    syncHubSpotChat(fake.win, '/dashboard')
    assert.deepEqual(fake.calls, ['remove', 'load'])
  })

  test('takes the widget down if the user reached a hidden route before it loaded', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/dashboard')
    fake.raw.location.pathname = '/chat/1'
    syncHubSpotChat(fake.win, '/chat/1')
    fake.scriptArrives()
    assert.deepEqual(fake.calls, ['remove'])
  })

  test('never loads when the console itself is framed by another page', () => {
    const fake = fakeWindow({ framed: true })
    syncHubSpotChat(fake.win, '/dashboard')
    assert.equal(fake.appended.length, 0)
  })
})
