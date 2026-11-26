/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { StatusBadge } from '@/components/status-badge'
import { formatTimestamp } from '@/lib/format'

import { getAdminEmailSends, type AdminEmailSendRow } from './api'

const STATUS_VARIANT: Record<
  AdminEmailSendRow['status'],
  'success' | 'danger' | 'warning' | 'neutral' | 'info'
> = {
  queued: 'info',
  sent: 'success',
  failed: 'danger',
  skipped: 'neutral',
  interrupted: 'warning',
}

const STATUS_LABEL: Record<AdminEmailSendRow['status'], string> = {
  queued: 'Queued',
  sent: 'Sent',
  failed: 'Failed',
  skipped: 'Skipped',
  interrupted: 'Interrupted',
}

type SendRowsProps = {
  rows: AdminEmailSendRow[]
  showRecipient: boolean
}

export function SendRows(props: SendRowsProps) {
  const { t } = useTranslation()
  if (props.rows.length === 0) {
    return (
      <p className='text-muted-foreground py-6 text-center text-sm'>
        {t('No emails sent yet.')}
      </p>
    )
  }
  return (
    <ul className='divide-y rounded-md border text-sm'>
      {props.rows.map((row) => (
        <li key={row.id} className='flex items-start gap-3 px-3 py-2'>
          <StatusBadge
            label={t(STATUS_LABEL[row.status])}
            variant={STATUS_VARIANT[row.status]}
            copyable={false}
          />
          <div className='min-w-0 flex-1'>
            <div className='truncate font-medium'>
              {props.showRecipient
                ? row.email || `#${row.user_id}`
                : row.subject}
            </div>
            <div className='text-muted-foreground text-xs'>
              {formatTimestamp(row.sent_at || row.created_at)}
              {row.error ? ` · ${row.error}` : ''}
            </div>
          </div>
        </li>
      ))}
    </ul>
  )
}

// One send in progress: polls until no recipient is still queued.
export function BatchStatus(props: { batchId: string }) {
  const { t } = useTranslation()
  const { data = [] } = useQuery({
    queryKey: ['admin-email', 'batch', props.batchId],
    queryFn: () => getAdminEmailSends({ batch_id: props.batchId, limit: 500 }),
    refetchInterval: (query) =>
      (query.state.data ?? []).some((row) => row.status === 'queued')
        ? 1500
        : false,
  })
  const count = (status: AdminEmailSendRow['status']) =>
    data.filter((row) => row.status === status).length
  return (
    <div className='space-y-3'>
      <p className='text-sm'>
        {t(
          'Sent {{sent}} · Queued {{queued}} · Failed {{failed}} · Skipped {{skipped}}',
          {
            sent: count('sent'),
            queued: count('queued'),
            failed: count('failed'),
            skipped: count('skipped'),
          }
        )}
      </p>
      <div className='max-h-80 overflow-y-auto'>
        <SendRows rows={data} showRecipient />
      </div>
    </div>
  )
}

// What this user has been sent from the Users page, newest first.
export function UserSendHistory(props: { userId: number }) {
  const { t } = useTranslation()
  const { data, isLoading } = useQuery({
    queryKey: ['admin-email', 'history', props.userId],
    queryFn: () => getAdminEmailSends({ user_id: props.userId, limit: 20 }),
  })
  if (isLoading) {
    return <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
  }
  return <SendRows rows={data ?? []} showRecipient={false} />
}
