/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useTranslation } from 'react-i18next'

import { LongText } from '@/components/long-text'

import type { User } from '../types'
import { useAdminEmail } from './compose-context'

type UsernameEmailButtonProps = {
  user: User
}

// The username in the admin Users table. Clicking it opens "Send email" for
// that user; the row's own click and selection are left alone.
export function UsernameEmailButton(props: UsernameEmailButtonProps) {
  const { t } = useTranslation()
  const adminEmail = useAdminEmail()
  const label = (
    <LongText className='max-w-[140px] font-medium'>
      {props.user.username}
    </LongText>
  )
  if (!adminEmail) return label
  return (
    <button
      type='button'
      data-admin-email-username
      className='hover:text-primary cursor-pointer rounded-sm text-left underline-offset-4 hover:underline focus-visible:ring-2 focus-visible:outline-none'
      title={t('Send email to {{name}}', { name: props.user.username })}
      aria-label={t('Send email to {{name}}', { name: props.user.username })}
      onClick={(event) => {
        event.stopPropagation()
        adminEmail.openCompose([props.user])
      }}
    >
      {label}
    </button>
  )
}
