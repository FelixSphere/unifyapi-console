/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * The cookie notice, the same one FelixSphere shows (same copy, same look,
 * same storage), on exactly the routes where HubSpot chat loads.
 *
 * One deliberate difference from FelixSphere: Reject is effective. It queues
 * HubSpot's `doNotTrack`, so HubSpot sets `__hs_do_not_track` and stops its
 * tracking cookies; live chat keeps working. Accept queues the opt-back-in.
 */
import { useState, useSyncExternalStore } from 'react'
import { useTranslation } from 'react-i18next'

import {
  PRIVACY_POLICY_URL,
  acceptCookies,
  rejectCookies,
  shouldShowCookieNotice,
} from './cookie-consent'
import { shouldShowHubSpotChat } from './hubspot-chat'

/** The slice of the TanStack router this component reads. */
export interface CookieNoticeRouteSource {
  subscribe: (event: 'onResolved', listener: () => void) => () => void
  state: { location: { pathname: string } }
}

export function CookieNotice(props: { router: CookieNoticeRouteSource }) {
  const { t } = useTranslation()
  const pathname = useSyncExternalStore(
    (onChange) => props.router.subscribe('onResolved', onChange),
    () => props.router.state.location.pathname
  )
  const [visible, setVisible] = useState(() =>
    shouldShowCookieNotice(window, Date.now())
  )

  if (!visible || !shouldShowHubSpotChat(window, pathname)) return null

  return (
    <div
      role='dialog'
      aria-label='Cookie notice'
      className='fixed right-4 bottom-24 left-4 z-50 max-w-[480px] rounded-xl border border-[#34446a] bg-[#101a30] p-5 text-[#f3f4f2] shadow-[0_24px_48px_-16px_rgba(0,0,0,0.5)] sm:right-auto sm:bottom-6 sm:left-6'
    >
      <p className='font-mono text-[11px] tracking-[0.14em] text-[#a2a7a5] uppercase'>
        {t('Cookie notice')}
      </p>
      <p className='mt-3 text-[14px] leading-[1.55] text-[#d4d7d5]'>
        {t(
          'We use required, functional, and advertising cookies. Reject to keep only essential cookies. See our'
        )}{' '}
        <a
          href={PRIVACY_POLICY_URL}
          target='_blank'
          rel='noopener noreferrer'
          className='underline underline-offset-2 hover:text-white'
        >
          {t('Privacy Policy')}
        </a>
        .
      </p>
      <div className='mt-4 flex gap-2'>
        <button
          type='button'
          onClick={() => {
            setVisible(false)
            rejectCookies(window, Date.now())
          }}
          className='h-9 flex-1 rounded-lg border border-[#34446a] text-[14px] font-medium hover:bg-[#1b2846]'
        >
          {t('Reject cookies')}
        </button>
        <button
          type='button'
          onClick={() => {
            setVisible(false)
            acceptCookies(window)
          }}
          className='h-9 flex-1 rounded-lg bg-[#2563eb] text-[14px] font-medium text-white hover:opacity-90'
        >
          {t('Accept all')}
        </button>
      </div>
    </div>
  )
}
