/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useTranslation } from 'react-i18next'

import { MARKETING_SITE_URL } from '@/lib/marketing-site'
import { cn } from '@/lib/utils'

import type { SystemStatus } from '../types'

interface TermsFooterProps {
  variant?: 'sign-in' | 'sign-up'
  className?: string
  status?: SystemStatus | null
}

/**
 * UNIFYAPI-BRAND: UI-STANDARD.md legal line, sign-up only: "By signing up
 * you agree to the Terms and Privacy Policy." Upstream rendered it on both
 * pages and only when the operator had enabled the in-app agreement pages;
 * the standard wants it on every sign-up, so the marketing site's Terms and
 * Privacy are the fallback when those pages are off.
 */
export function TermsFooter({
  variant = 'sign-in',
  className,
  status,
}: TermsFooterProps) {
  const { t } = useTranslation()

  if (variant !== 'sign-up') return null

  const termsHref = status?.user_agreement_enabled
    ? '/user-agreement'
    : `${MARKETING_SITE_URL}terms`
  const privacyHref = status?.privacy_policy_enabled
    ? '/privacy-policy'
    : `${MARKETING_SITE_URL}privacy`

  return (
    <p className={cn('text-muted-foreground text-xs', className)}>
      {t('By signing up you agree to the')}{' '}
      <a
        href={termsHref}
        className='hover:text-foreground underline underline-offset-4'
      >
        {t('Terms')}
      </a>{' '}
      {t('and')}{' '}
      <a
        href={privacyHref}
        className='hover:text-foreground underline underline-offset-4'
      >
        {t('Privacy Policy')}
      </a>
      .
    </p>
  )
}
