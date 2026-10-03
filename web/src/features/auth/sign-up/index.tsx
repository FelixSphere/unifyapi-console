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

import { useStatus } from '@/hooks/use-status'
import { DEFAULT_SYSTEM_NAME } from '@/lib/constants'

import { AuthLayout } from '../auth-layout'
import { AuthHeading } from '../components/auth-heading'
import { SwitchLink } from '../components/switch-link'
import { TermsFooter } from '../components/terms-footer'
import { SignUpForm } from './components/sign-up-form'

// UNIFYAPI-BRAND: UI-STANDARD.md "Log in / Sign up pages". The H1 reads
// "Create your Unify API account"; the legal line sits under the form and the
// switch link closes the card.
export function SignUp() {
  const { t } = useTranslation()
  const { status } = useStatus()

  return (
    <AuthLayout>
      <AuthHeading
        title={t('Create your {{product}} account', {
          product: DEFAULT_SYSTEM_NAME,
        })}
      />

      <div className='mt-6'>
        <SignUpForm />
      </div>

      <TermsFooter variant='sign-up' status={status} className='mt-4' />

      <SwitchLink
        prompt={t('Already have an account?')}
        to='/sign-in'
        label={t('Log in')}
      />
    </AuthLayout>
  )
}
