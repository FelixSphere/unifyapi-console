/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// Operator decision, 2026-10-02: a signed-in user gets neither HubSpot chat
// nor the cookie notice. These drive the real auth store, so a change to what
// "signed in" means in the app shows up here.

import { beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { useAuthStore, type AuthBundle } from '@/stores/auth-store'

import {
  HUBSPOT_SCRIPT_ID,
  chatVisitor,
  shouldShowHubSpotChat,
  syncHubSpotChat,
  watchHubSpotChat,
  type ChatRouter,
} from '../hubspot-chat'

type FakeScript = { id: string; src: string }

function fakeWindow(pathname = '/pricing') {
  const appended: FakeScript[] = []
  const calls: string[] = []
  const win = {
    self: {},
    top: {},
    location: { pathname, reload: () => calls.push('reload') },
    HubSpotConversations: undefined as unknown,
    document: {
      querySelector: (selector: string) =>
        appended.find((script) => `#${script.id}` === selector) ?? null,
      createElement: () => ({ id: '', src: '' }),
      body: { appendChild: (script: FakeScript) => appended.push(script) },
    },
  }
  win.top = win.self
  return { win: win as unknown as Window, appended, calls }
}

function fakeRouter() {
  type Listener = Parameters<ChatRouter['subscribe']>[1]
  const listeners: Listener[] = []
  let current: string | undefined
  const router: ChatRouter = {
    subscribe: (_event, listener) => {
      listeners.push(listener)
      return () => undefined
    },
  }
  const resolve = (pathname: string) => {
    const pathChanged = pathname !== current
    current = pathname
    for (const listener of listeners) {
      listener({ pathChanged, toLocation: { pathname } })
    }
  }
  return { router, resolve }
}

const bundle: AuthBundle = {
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

const auth = () => useAuthStore.getState().auth

beforeEach(() => {
  auth().reset('idle')
})

describe('who counts as signed in', () => {
  test('a user in the auth store is signed in, even while a refresh retries', () => {
    auth().setBundle(bundle)
    assert.equal(chatVisitor(auth()), 'signed-in')
    auth().setBootstrapState('idle')
    assert.equal(chatVisitor(auth()), 'signed-in')
  })

  test('no user is signed out only once the bootstrap has answered', () => {
    assert.equal(chatVisitor(auth()), 'unknown')
    auth().setBootstrapState('checking')
    assert.equal(chatVisitor(auth()), 'unknown')
    auth().reset('complete')
    assert.equal(chatVisitor(auth()), 'signed-out')
  })
})

describe('HubSpot chat for signed-in users', () => {
  test('never injects the loader for a signed-in user, on any route', () => {
    for (const path of ['/', '/pricing', '/dashboard', '/keys', '/wallet']) {
      const fake = fakeWindow()
      assert.equal(shouldShowHubSpotChat(fake.win, path, 'signed-in'), false)
      syncHubSpotChat(fake.win, path, 'signed-in')
      assert.equal(fake.appended.length, 0, path)
      assert.deepEqual(fake.calls, [], path)
    }
  })

  test('injects nothing until auth has answered, then follows the answer', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/pricing', 'unknown')
    assert.equal(fake.appended.length, 0)
    syncHubSpotChat(fake.win, '/pricing', 'signed-out')
    assert.equal(fake.appended.length, 1)
    assert.equal(fake.appended[0].id, HUBSPOT_SCRIPT_ID)
  })

  test('an unanswered auth state does not reload an eligible page, a hidden route still does', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/pricing', 'signed-out')
    syncHubSpotChat(fake.win, '/dashboard', 'unknown')
    assert.deepEqual(fake.calls, [])
    syncHubSpotChat(fake.win, '/sign-in', 'unknown')
    assert.deepEqual(fake.calls, ['reload'])
  })

  test('signing in reloads a page that already has the loader', () => {
    const fake = fakeWindow()
    syncHubSpotChat(fake.win, '/pricing', 'signed-out')
    syncHubSpotChat(fake.win, '/pricing', 'signed-in')
    assert.deepEqual(fake.calls, ['reload'])
  })
})

describe('watchHubSpotChat, wired to the real auth store', () => {
  test('a signed-out visitor gets the loader on the first resolve, not before', () => {
    const fake = fakeWindow()
    const { router, resolve } = fakeRouter()
    watchHubSpotChat(fake.win, router, useAuthStore)
    assert.equal(fake.appended.length, 0)

    auth().reset('complete')
    assert.equal(fake.appended.length, 0)
    resolve('/pricing')
    assert.equal(fake.appended.length, 1)
  })

  test('a signed-in visitor never gets the loader', () => {
    const fake = fakeWindow()
    const { router, resolve } = fakeRouter()
    watchHubSpotChat(fake.win, router, useAuthStore)
    auth().setBundle(bundle)
    for (const path of ['/dashboard', '/keys', '/pricing', '/']) resolve(path)
    assert.equal(fake.appended.length, 0)
    assert.deepEqual(fake.calls, [])
  })

  test('signing in on a page that has the loader reloads it', () => {
    const fake = fakeWindow('/pricing')
    const { router, resolve } = fakeRouter()
    watchHubSpotChat(fake.win, router, useAuthStore)
    auth().reset('complete')
    resolve('/pricing')
    assert.equal(fake.appended.length, 1)

    auth().setBundle(bundle)
    assert.deepEqual(fake.calls, ['reload'])
  })

  test('signing in without the loader in the page reloads nothing', () => {
    const fake = fakeWindow('/sign-in')
    const { router, resolve } = fakeRouter()
    watchHubSpotChat(fake.win, router, useAuthStore)
    auth().reset('complete')
    resolve('/sign-in')
    auth().setBundle(bundle)
    resolve('/dashboard')
    assert.equal(fake.appended.length, 0)
    assert.deepEqual(fake.calls, [])
  })

  test('signing out injects on the next eligible route, without a reload', () => {
    const fake = fakeWindow('/dashboard')
    const { router, resolve } = fakeRouter()
    watchHubSpotChat(fake.win, router, useAuthStore)
    auth().setBundle(bundle)
    resolve('/dashboard')

    auth().reset('complete')
    assert.equal(fake.appended.length, 0)
    resolve('/sign-in')
    assert.equal(fake.appended.length, 0)
    resolve('/pricing')
    assert.equal(fake.appended.length, 1)
    assert.deepEqual(fake.calls, [])
  })
})
