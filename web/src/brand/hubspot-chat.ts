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

export function shouldShowHubSpotChat(win: Window, pathname: string): boolean {
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
 */
export function syncHubSpotChat(win: Window, pathname: string): void {
  const injected = win.document.querySelector(`#${HUBSPOT_SCRIPT_ID}`) !== null

  if (!shouldShowHubSpotChat(win, pathname)) {
    if (injected) win.location.reload()
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
