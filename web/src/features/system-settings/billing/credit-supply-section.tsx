/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the credit supply screen -- what we hold, what is left, and
// what we owe the people who sold it to us. See docs/credit-supply.md.
//
// Layout follows the question order an operator actually asks: how much is in
// the pool and what does it cost (headline), what needs me today (attention),
// which vendors is it spread across (breakdown), then the two ledgers (lots,
// suppliers). Lifecycle actions live on the lot row so approving a supplier's
// submission is one click from the number that justifies it.

import { useQuery } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { getChannels } from '@/features/channels/api'

import { SettingsSection } from '../components/settings-section'
import {
  getCreditLots,
  getCreditSupplyOverview,
  getCreditSuppliers,
  type CreditLot,
  type CreditLotStatus,
} from './credit-supply-api'
import {
  LOT_STATUS_LABELS,
  daysUntil,
  formatUSD,
  remainingUSD,
} from './credit-supply-logic'
import { CreditLotsPanel } from './credit-supply-lots'
import { CreditSuppliersPanel } from './credit-supply-suppliers'

const STATUS_ORDER: CreditLotStatus[] = [
  'active',
  'pending',
  'suspended',
  'exhausted',
  'expired',
  'rejected',
]

function attentionReason(
  lot: CreditLot,
  now: number,
  t: (key: string, opts?: Record<string, unknown>) => string
) {
  if (lot.status === 'pending') return t('Awaiting approval')
  if (lot.low_water_usd > 0 && remainingUSD(lot) <= lot.low_water_usd) {
    return t('{{remaining}} left, at or below the low-water mark', {
      remaining: formatUSD(remainingUSD(lot)),
    })
  }
  if (lot.expires_at) {
    return t('Expires in {{days}} days with {{remaining}} unused', {
      days: daysUntil(lot.expires_at, now),
      remaining: formatUSD(remainingUSD(lot)),
    })
  }
  return ''
}

export function CreditSupplySection() {
  const { t } = useTranslation()
  const [tab, setTab] = useState<'lots' | 'suppliers'>('lots')
  const now = useMemo(() => Math.floor(Date.now() / 1000), [])

  const overview = useQuery({
    queryKey: ['credit-supply', 'overview'],
    queryFn: getCreditSupplyOverview,
  })
  const suppliers = useQuery({
    queryKey: ['credit-supply', 'suppliers'],
    queryFn: getCreditSuppliers,
  })
  const lots = useQuery({
    queryKey: ['credit-supply', 'lots'],
    queryFn: () => getCreditLots(),
  })
  const channels = useQuery({
    queryKey: ['credit-supply', 'channels'],
    queryFn: async () => {
      const response = await getChannels({ p: 1, page_size: 200 })
      return response.success ? (response.data?.items ?? []) : []
    },
    staleTime: 60 * 1000,
  })

  const loading = overview.isLoading || suppliers.isLoading || lots.isLoading
  const firstError = [overview, suppliers, lots].find((query) => query.isError)

  return (
    <SettingsSection title={t('Credit Supply')}>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'Suppliers sell us vendor credits. Each lot is bound to a channel carrying their key, drawn down at the vendor’s list price, and settled with the supplier at the agreed acquisition rate. Activating a lot writes that rate into the channel’s purchasing cost ratio, so Profit and Settlement already account for it. No money moves here: payables are issued from Settlement → Vendor.'
          )}
        </AlertDescription>
      </Alert>

      {loading ? (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      ) : null}
      {firstError ? (
        <Alert variant='destructive'>
          <AlertDescription>
            {(firstError.error as Error).message}
          </AlertDescription>
        </Alert>
      ) : null}

      {overview.data ? (
        <>
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
            <Headline
              label={t('Face value in pool')}
              value={formatUSD(overview.data.face_usd)}
              hint={t('{{count}} suppliers', {
                count: overview.data.suppliers,
              })}
            />
            <Headline
              label={t('Consumed at list price')}
              value={formatUSD(overview.data.consumed_usd)}
              hint={
                overview.data.unpriced_lots > 0
                  ? t('{{count}} lots understated by uncatalogued models', {
                      count: overview.data.unpriced_lots,
                    })
                  : t('Every request priced')
              }
              warn={overview.data.unpriced_lots > 0}
            />
            <Headline
              label={t('Remaining')}
              value={formatUSD(overview.data.remaining_usd)}
              hint={t('Across live lots')}
            />
            <Headline
              label={t('Payable to suppliers to date')}
              value={formatUSD(overview.data.payable_usd)}
              hint={t('Issue from Settlement → Vendor')}
            />
          </div>

          <div className='flex flex-wrap gap-2'>
            {STATUS_ORDER.map((status) => {
              const count = overview.data?.lots_by_status[status] ?? 0
              if (count === 0) return null
              return (
                <Badge
                  key={status}
                  variant={status === 'active' ? 'default' : 'secondary'}
                >
                  {count} {t(LOT_STATUS_LABELS[status]).toLowerCase()}
                </Badge>
              )
            })}
          </div>

          {overview.data.attention.length > 0 ? (
            <Card>
              <CardHeader>
                <CardTitle className='flex items-center gap-2 text-base'>
                  <AlertTriangle className='size-4' />
                  {t('Needs attention')}
                </CardTitle>
                <CardDescription>
                  {t(
                    'Pending submissions, lots at their low-water mark, and lots expiring within seven days.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='grid gap-2'>
                {overview.data.attention.map((lot) => (
                  <div
                    key={lot.id}
                    className='flex flex-wrap items-center justify-between gap-2 rounded border px-3 py-2 text-sm'
                  >
                    <span className='font-medium'>
                      #{lot.id} · {lot.vendor}
                    </span>
                    <span className='text-muted-foreground'>
                      {attentionReason(lot, now, t)}
                    </span>
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : null}

          {overview.data.by_vendor.length > 0 ? (
            <div className='overflow-x-auto rounded-md border'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Vendor')}</TableHead>
                    <TableHead>{t('Lots')}</TableHead>
                    <TableHead>{t('Face value')}</TableHead>
                    <TableHead>{t('Consumed')}</TableHead>
                    <TableHead>{t('Remaining')}</TableHead>
                    <TableHead>{t('Payable')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {overview.data.by_vendor.map((row) => (
                    <TableRow key={row.vendor}>
                      <TableCell className='font-medium'>
                        {row.vendor}
                      </TableCell>
                      <TableCell className='tabular-nums'>{row.lots}</TableCell>
                      <TableCell className='tabular-nums'>
                        {formatUSD(row.face_usd)}
                      </TableCell>
                      <TableCell className='tabular-nums'>
                        {formatUSD(row.consumed_usd)}
                      </TableCell>
                      <TableCell className='tabular-nums'>
                        {formatUSD(row.remaining_usd)}
                      </TableCell>
                      <TableCell className='tabular-nums'>
                        {formatUSD(row.payable_usd)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : null}
        </>
      ) : null}

      <Tabs
        value={tab}
        onValueChange={(value) => setTab(value as 'lots' | 'suppliers')}
      >
        <TabsList>
          <TabsTrigger value='lots'>{t('Lots')}</TabsTrigger>
          <TabsTrigger value='suppliers'>{t('Suppliers')}</TabsTrigger>
        </TabsList>
      </Tabs>

      {tab === 'lots' ? (
        <CreditLotsPanel
          lots={lots.data ?? []}
          suppliers={suppliers.data ?? []}
          channels={channels.data ?? []}
          now={now}
        />
      ) : (
        <CreditSuppliersPanel
          suppliers={suppliers.data ?? []}
          lots={lots.data ?? []}
        />
      )}
    </SettingsSection>
  )
}

function Headline({
  label,
  value,
  hint,
  warn,
}: {
  label: string
  value: string
  hint: string
  warn?: boolean
}) {
  return (
    <Card>
      <CardHeader className='pb-2'>
        <CardDescription>{label}</CardDescription>
        <CardTitle className='text-2xl tabular-nums'>{value}</CardTitle>
      </CardHeader>
      <CardContent
        className={
          warn ? 'text-destructive text-xs' : 'text-muted-foreground text-xs'
        }
      >
        {hint}
      </CardContent>
    </Card>
  )
}
