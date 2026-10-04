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
import { useSearch } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { useStatus } from '@/hooks/use-status'
import { DEFAULT_SYSTEM_NAME } from '@/lib/constants'

import { AuthLayout } from '../auth-layout'
import { AuthHeading } from '../components/auth-heading'
import { SwitchLink } from '../components/switch-link'
import { UserAuthForm } from './components/user-auth-form'

// UNIFYAPI-BRAND: UI-STANDARD.md "Log in / Sign up pages". The H1 reads
// "Log in to Unify API", the switch link
// closes the card. The legal line belongs to sign-up only.
export function SignIn() {
  const { t } = useTranslation()
  const { redirect } = useSearch({ from: '/(auth)/sign-in' })
  const { status } = useStatus()
  const canSignUp =
    !status?.self_use_mode_enabled && status?.register_enabled !== false

  return (
    <AuthLayout>
      <AuthHeading
        title={t('Log in to {{product}}', { product: DEFAULT_SYSTEM_NAME })}
      />

      <div className='mt-6'>
        <UserAuthForm redirectTo={redirect} />
      </div>

      {canSignUp && (
        <SwitchLink
          prompt={t('No account?')}
          to='/sign-up'
          label={t('Sign up')}
        />
      )}
    </AuthLayout>
  )
}
