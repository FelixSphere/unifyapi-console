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
  load: () => void
  refresh: () => void
  remove: () => void
  status: () => { loaded: boolean }
}

declare global {
  interface Window {
    HubSpotConversations?: { widget?: HubSpotConversationsWidget }
    hsConversationsOnReady?: Array<() => void>
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
 * Bring the widget in line with the route the SPA has just resolved: inject
 * the loader once, refresh the widget on later route changes (as HubSpot asks
 * single-page apps to do), and remove it on routes where it is hidden.
 */
export function syncHubSpotChat(win: Window, pathname: string): void {
  const widget = win.HubSpotConversations?.widget

  if (!shouldShowHubSpotChat(win, pathname)) {
    if (widget?.status().loaded) widget.remove()
    return
  }

  if (!win.document.querySelector(`#${HUBSPOT_SCRIPT_ID}`)) {
    // The widget loads itself when the script arrives. If the user has moved
    // to a hidden route by then, take it straight back down.
    win.hsConversationsOnReady = [
      ...(win.hsConversationsOnReady ?? []),
      () => {
        if (shouldShowHubSpotChat(win, win.location.pathname)) return
        win.HubSpotConversations?.widget?.remove()
      },
    ]
    const script = win.document.createElement('script')
    script.id = HUBSPOT_SCRIPT_ID
    script.src = HUBSPOT_SCRIPT_SRC
    script.async = true
    script.defer = true
    win.document.body.appendChild(script)
    return
  }

  // Script requested but not ready yet: it will load the widget on its own.
  if (!widget) return

  if (widget.status().loaded) {
    widget.refresh()
  } else {
    widget.load()
  }
}
