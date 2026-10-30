/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * Consent storage for the cookie notice, identical to FelixSphere's:
 * Accept -> cookie `localConsent=true` for a year; Reject -> localStorage
 * `rejectTimestamp`, asked again after 12 hours. Each answer is also queued to
 * HubSpot (`_hsq`), which is the one deliberate difference: Reject stops
 * HubSpot's tracking cookies (`__hs_do_not_track`), chat keeps working.
 */
export const PRIVACY_POLICY_URL = 'https://www.unifyapi.ai/privacy'
const CONSENT_COOKIE = 'localConsent'
export const REJECT_TIMESTAMP_KEY = 'rejectTimestamp'
const CONSENT_MAX_AGE_SECONDS = 365 * 24 * 60 * 60
export const REJECT_QUIET_PERIOD_MS = 12 * 60 * 60 * 1000

declare global {
  interface Window {
    _hsq?: unknown[]
  }
}

function hasConsentCookie(win: Window): boolean {
  return win.document.cookie
    .split(';')
    .some((part) => part.trim().startsWith(`${CONSENT_COOKIE}=`))
}

/** Hidden after Accept; hidden for 12 hours after Reject, then asked again. */
export function shouldShowCookieNotice(win: Window, now: number): boolean {
  if (hasConsentCookie(win)) return false

  let rejectTimestamp: string | null = null
  try {
    rejectTimestamp = win.localStorage.getItem(REJECT_TIMESTAMP_KEY)
  } catch {
    /* storage blocked: treat as never answered */
  }
  if (!rejectTimestamp) return true
  return now - Number.parseInt(rejectTimestamp, 10) > REJECT_QUIET_PERIOD_MS
}

export function acceptCookies(win: Window): void {
  win.document.cookie = `${CONSENT_COOKIE}=true; max-age=${CONSENT_MAX_AGE_SECONDS}; path=/`
  try {
    win.localStorage.removeItem(REJECT_TIMESTAMP_KEY)
  } catch {
    /* storage blocked */
  }
  ;(win._hsq = win._hsq || []).push(['doNotTrack', { track: true }])
}

export function rejectCookies(win: Window, now: number): void {
  try {
    win.localStorage.setItem(REJECT_TIMESTAMP_KEY, now.toString())
  } catch {
    /* storage blocked */
  }
  ;(win._hsq = win._hsq || []).push(['doNotTrack'])
}
