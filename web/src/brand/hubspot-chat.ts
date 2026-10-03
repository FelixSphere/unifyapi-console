/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * HubSpot live chat, shared with the marketing site (same portal), so a
 * visitor who started a conversation on www.unifyapi.ai can continue it here.
 *
 * Loaded from the bundle rather than written into index.html so that it can
 * stay off the routes listed below, and so that it is one fork-owned file
 * instead of another edit to an upstream one.
 */
import type { AuthBootstrapState } from '@/stores/auth-store'

export const HUBSPOT_PORTAL_ID = '46127314'
export const HUBSPOT_SCRIPT_ID = 'hs-script-loader'
export const HUBSPOT_SCRIPT_SRC = `https://js.hs-scripts.com/${HUBSPOT_PORTAL_ID}.js`

/**
 * Routes where a floating chat bubble does not belong:
 * - `/oauth/*` runs inside the account-bind popup and closes itself.
 * - `/chat/*` is a full-viewport iframe of a third-party chat client; the
 *   bubble would sit on top of that client's own composer.
 * - `/chat2link` only redirects into `/chat/*`.
 * - `/setup` is the one-time operator bootstrap, not a customer surface.
 * - Sign-in, sign-up and password-reset pages take credentials and carry
 *   one-time tokens in the URL; HubSpot's loader also brings in its
 *   collected-forms script, which records form submissions.
 */
const HIDDEN_ROUTE_PREFIXES = [
  '/oauth',
  '/chat',
  '/chat2link',
  '/setup',
  '/sign-in',
  '/sign-up',
  '/register',
  '/forgot-password',
  '/reset',
  '/user/reset',
]

interface HubSpotConversationsWidget {
  refresh: () => void
  status: () => { loaded: boolean }
}

declare global {
  interface Window {
    HubSpotConversations?: { widget?: HubSpotConversationsWidget }
  }
}

/**
 * Who is looking, as the auth store knows it. Chat and the cookie notice are
 * for signed-out visitors only (operator decision, 2026-10-02). `unknown` is
 * the moment before the session refresh answers: nothing loads yet, so a
 * signed-in user never sees either flash in.
 */
export type ChatVisitor = 'signed-in' | 'signed-out' | 'unknown'

/** The slice of the auth store (`stores/auth-store.ts`) that decides it. */
export interface ChatVisitorAuth {
  user: unknown
  bootstrapState: AuthBootstrapState
}

/**
 * The same test the app uses everywhere (`features/home`, the
 * `/_authenticated` guard): a user in the auth store means signed in. A user
 * left in place while a refresh retries still counts as signed in.
 */
export function chatVisitor(auth: ChatVisitorAuth): ChatVisitor {
  if (auth.user) return 'signed-in'
  return auth.bootstrapState === 'complete' ? 'signed-out' : 'unknown'
}

/** The slice of the zustand auth store (`useAuthStore`) read here. */
export interface ChatAuthStore {
  getState: () => { auth: ChatVisitorAuth }
  subscribe: (
    listener: (
      state: { auth: ChatVisitorAuth },
      previous: { auth: ChatVisitorAuth }
    ) => void
  ) => () => void
}

/** The slice of the TanStack router read here. */
export interface ChatRouter {
  subscribe: (
    event: 'onResolved',
    listener: (event: {
      pathChanged: boolean
      toLocation: { pathname: string }
    }) => void
  ) => () => void
}

/**
 * `visitor` defaults to `signed-out`, the audience the route rules below were
 * written for; the app always passes the auth store's answer.
 */
export function shouldShowHubSpotChat(
  win: Window,
  pathname: string,
  visitor: ChatVisitor = 'signed-out'
): boolean {
  if (visitor !== 'signed-out') return false

  const hiddenRoute = HIDDEN_ROUTE_PREFIXES.some(
    (prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`)
  )
  if (hiddenRoute) return false

  // Never inside someone else's frame; a cross-origin parent makes `top` throw.
  try {
    return win.self === win.top
  } catch {
    return false
  }
}

/**
 * Bring the page in line with the route the SPA has just resolved: inject the
 * loader once, and refresh the widget on later route changes (as HubSpot asks
 * single-page apps to do).
 *
 * Removing the widget is not enough on a hidden route. The loader also starts
 * HubSpot's analytics and collected-forms scripts, which stay live for the
 * rest of the document and would record the sign-in or setup form. So a
 * client-side move onto a hidden route, once the loader is in this document,
 * reloads the page: the fresh document starts on a hidden route and never
 * injects it.
 *
 * Signing in works the same way: the loader is already in the document, so
 * the page reloads, and the fresh document starts signed in and never injects
 * it. Signing out injects nothing until the next eligible route resolves.
 */
export function syncHubSpotChat(
  win: Window,
  pathname: string,
  visitor: ChatVisitor = 'signed-out'
): void {
  const injected = win.document.querySelector(`#${HUBSPOT_SCRIPT_ID}`) !== null

  if (!shouldShowHubSpotChat(win, pathname, visitor)) {
    // While auth is still being answered on an otherwise eligible page, wait:
    // the next sync acts on the answer. A hidden route reloads regardless.
    const waitingForAuth =
      visitor === 'unknown' && shouldShowHubSpotChat(win, pathname)
    if (injected && !waitingForAuth) win.location.reload()
    return
  }

  if (!injected) {
    const script = win.document.createElement('script')
    script.id = HUBSPOT_SCRIPT_ID
    script.src = HUBSPOT_SCRIPT_SRC
    script.async = true
    script.defer = true
    win.document.body.appendChild(script)
    return
  }

  // Script requested but not ready yet: it loads the widget on its own.
  const widget = win.HubSpotConversations?.widget
  if (widget?.status().loaded) widget.refresh()
}

/**
 * Keep chat in line with the app for the life of the document.
 *
 * Nothing is injected at boot: the first sync is the router's first resolve,
 * which comes after the root route has awaited the auth bootstrap, so the
 * auth store already knows who is looking. After that, every resolved path
 * change re-syncs, and so does a move into signed in (a sign-in on this page
 * or another tab), which reloads a page that already has the loader. A move
 * to signed out waits for the next resolved route, so signing out from a
 * product page never injects the loader just before the redirect to sign-in.
 */
export function watchHubSpotChat(
  win: Window,
  router: ChatRouter,
  authStore: ChatAuthStore
): void {
  router.subscribe('onResolved', (event) => {
    if (!event.pathChanged) return
    const visitor = chatVisitor(authStore.getState().auth)
    syncHubSpotChat(win, event.toLocation.pathname, visitor)
  })
  authStore.subscribe((state, previous) => {
    if (chatVisitor(state.auth) !== 'signed-in') return
    if (chatVisitor(previous.auth) === 'signed-in') return
    syncHubSpotChat(win, win.location.pathname, 'signed-in')
  })
}
