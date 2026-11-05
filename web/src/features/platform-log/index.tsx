/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { getPlatformEvents, type PlatformEvent } from './api'

const PAGE_SIZE = 50

// The three layers a request can die at, in the order it meets them. A caller
// that fails TLS never reaches the next one, which is the whole reason this
// page exists.
const SOURCES = [
  { value: '', labelKey: 'All sources' },
  { value: 'alb_conn', labelKey: 'TLS handshake' },
  { value: 'alb_access', labelKey: 'Load balancer' },
  { value: 'auth', labelKey: 'Authentication' },
]

function severityVariant(severity: string) {
  return severity === 'error' ? 'destructive' : 'secondary'
}

export function PlatformLog() {
  const { t } = useTranslation()
  const [events, setEvents] = useState<PlatformEvent[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [source, setSource] = useState('')
  const [clientIp, setClientIp] = useState('')
  const [keyPrefix, setKeyPrefix] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // `applied` is what the last search actually used. Typing in a filter must
  // not refetch on every keystroke, and the page number must reset when the
  // filters change or page 3 of the old query is requested against the new one.
  const [applied, setApplied] = useState({
    source: '',
    clientIp: '',
    keyPrefix: '',
  })

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError('')
    void getPlatformEvents({
      source: applied.source,
      clientIp: applied.clientIp,
      keyPrefix: applied.keyPrefix,
      page,
      pageSize: PAGE_SIZE,
    })
      .then((res) => {
        if (cancelled) return
        setEvents(res.items)
        setTotal(res.total)
      })
      .catch((e: unknown) => {
        if (cancelled) return
        setEvents([])
        setError(e instanceof Error ? e.message : String(e))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [applied, page])

  const search = () => {
    setPage(1)
    setApplied({ source, clientIp, keyPrefix })
  }

  const lastPage = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        <span className='inline-flex min-w-0 items-center gap-2'>
          <span className='truncate'>{t('Platform Log')}</span>
          <Badge variant='outline' className='shrink-0'>
            Root
          </Badge>
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Requests that never reached the relay, so they leave no row in the usage log: TLS handshakes that failed, connections the load balancer refused, and callers turned away at authentication.'
            )}
          </p>

          <div className='flex flex-wrap items-end gap-2'>
            <NativeSelect
              value={source}
              onChange={(e) => setSource(e.target.value)}
              className='w-44'
              aria-label={t('Source')}
            >
              {SOURCES.map((s) => (
                <option key={s.value} value={s.value}>
                  {t(s.labelKey)}
                </option>
              ))}
            </NativeSelect>
            <Input
              value={clientIp}
              onChange={(e) => setClientIp(e.target.value)}
              placeholder={t('Client IP')}
              className='w-44'
            />
            <Input
              value={keyPrefix}
              onChange={(e) => setKeyPrefix(e.target.value)}
              placeholder={t('Key prefix')}
              className='w-44'
            />
            <Button onClick={search} disabled={loading}>
              {t('Search')}
            </Button>
          </div>

          {error ? <p className='text-destructive text-sm'>{error}</p> : null}

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Time')}</TableHead>
                <TableHead>{t('Source')}</TableHead>
                <TableHead>{t('What happened')}</TableHead>
                <TableHead>{t('Client IP')}</TableHead>
                <TableHead>{t('Key prefix')}</TableHead>
                <TableHead>{t('Path')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.length === 0 && !loading ? (
                <TableRow>
                  <TableCell colSpan={7} className='text-muted-foreground'>
                    {t('Nothing recorded for this filter.')}
                  </TableCell>
                </TableRow>
              ) : (
                events.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className='whitespace-nowrap'>
                      {new Date(e.created_at * 1000).toLocaleString()}
                    </TableCell>
                    <TableCell>
                      <Badge variant={severityVariant(e.severity)}>
                        {e.source}
                      </Badge>
                    </TableCell>
                    <TableCell>{e.kind}</TableCell>
                    <TableCell className='font-mono text-xs'>
                      {e.client_ip}
                    </TableCell>
                    <TableCell className='font-mono text-xs'>
                      {e.key_prefix}
                    </TableCell>
                    <TableCell className='max-w-[18rem] truncate'>
                      {e.path}
                    </TableCell>
                    <TableCell>{e.status || '-'}</TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>

          <div className='flex items-center justify-between'>
            <span className='text-muted-foreground text-sm'>
              {t('{{total}} entries', { total })}
            </span>
            <div className='flex gap-2'>
              <Button
                variant='outline'
                size='sm'
                disabled={page <= 1 || loading}
                onClick={() => setPage((p) => Math.max(1, p - 1))}
              >
                {t('Previous')}
              </Button>
              <Button
                variant='outline'
                size='sm'
                disabled={page >= lastPage || loading}
                onClick={() => setPage((p) => p + 1)}
              >
                {t('Next')}
              </Button>
            </div>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
