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

function fakeWindow(options: { framed?: boolean } = {}) {
  const appended: FakeScript[] = []
  const calls: string[] = []
  const win = {
    self: {},
    top: {},
    location: { reload: () => calls.push('reload') },
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
    refresh: () => calls.push('refresh'),
    status: () => ({ loaded: true }),
  }
  const scriptArrives = () => {
    win.HubSpotConversations = { widget }
  }
  return { win: win as unknown as Window, appended, calls, scriptArrives }
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

  test('reloads on a client-side move to a hidden route once the loader is in the page', () => {
    // HubSpot's analytics and collected-forms scripts outlive widget.remove(),
    // so only a fresh document keeps them off the sign-in and setup forms.
    for (const hidden of ['/sign-in', '/setup', '/chat/1']) {
      const fake = fakeWindow()
      syncHubSpotChat(fake.win, '/pricing')
      syncHubSpotChat(fake.win, hidden)
      assert.deepEqual(fake.calls, ['reload'], hidden)
    }
  })

  test('reloads even if the loader has not finished arriving', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/dashboard')
    syncHubSpotChat(fake.win, '/sign-in')
    assert.deepEqual(fake.calls, ['reload'])
  })

  test('never loads when the console itself is framed by another page', () => {
    const fake = fakeWindow({ framed: true })
    syncHubSpotChat(fake.win, '/dashboard')
    assert.equal(fake.appended.length, 0)
  })
})
