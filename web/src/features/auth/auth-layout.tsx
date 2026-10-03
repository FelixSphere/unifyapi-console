/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useTranslation } from 'react-i18next'

import { BrandLockup } from '@/brand/brand-lockup'
import { UNIFYAI_COMPANY_NAME } from '@/brand/company'
import { HomeAnchor } from '@/components/layout/components/home-anchor'
import { MARKETING_SITE_URL } from '@/lib/marketing-site'

type AuthLayoutProps = {
  children: React.ReactNode
}

const PRIVACY_URL = `${MARKETING_SITE_URL}privacy`
const TERMS_URL = `${MARKETING_SITE_URL}terms`

/**
 * UNIFYAPI-BRAND: UI-STANDARD.md "Log in / Sign up pages", identical in every
 * Unify product. A Paper page with one centred white card (400px, 1px rule,
 * radius 6, padding 32; full width minus 16px gutters on phones). The card
 * starts with the UnifyAI lockup exactly as in the app header, and the page
 * ends with the company line and the legal links. No split screens,
 * illustrations, gradients or shadows.
 */
export function AuthLayout({ children }: AuthLayoutProps) {
  const { t } = useTranslation()
  const year = new Date().getFullYear()

  return (
    <div className='bg-background flex min-h-svh flex-col items-center justify-center px-4 py-10'>
      <main className='bg-card border-border w-full max-w-[400px] rounded-md border p-8'>
        {/* The logo leaves for the marketing site; `/` here is upstream's
            landing page, which is never shown. */}
        <HomeAnchor
          to={MARKETING_SITE_URL}
          className='inline-flex items-center transition-opacity hover:opacity-80'
        >
          <BrandLockup />
        </HomeAnchor>
        <div className='mt-6'>{children}</div>
      </main>
      <p className='text-muted-foreground mt-6 text-center text-xs'>
        © {year} {UNIFYAI_COMPANY_NAME}
        <span aria-hidden='true'> · </span>
        <a
          href={PRIVACY_URL}
          className='hover:text-foreground underline-offset-4 hover:underline'
        >
          {t('Privacy')}
        </a>
        <span aria-hidden='true'> · </span>
        <a
          href={TERMS_URL}
          className='hover:text-foreground underline-offset-4 hover:underline'
        >
          {t('Terms')}
        </a>
      </p>
    </div>
  )
}
