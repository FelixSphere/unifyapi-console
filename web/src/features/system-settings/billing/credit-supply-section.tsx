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

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import { updateSystemOption } from '@/features/system-settings/api'

import { SettingsSection } from '../components/settings-section'
import {
  getCreditLots,
  getCreditSupplyOverview,
  getCreditSupplyTerms,
  type CreditSupplyTerms,
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
  'verified',
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
  if (lot.status === 'pending') return t('Verifying the key')
  if (lot.status === 'verified') {
    return t('Verified — pay {{amount}} to activate', {
      amount: formatUSD(lot.face_value_usd * lot.acquisition_rate),
    })
  }
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

// TermsCard edits the posted buy terms. Saving writes the CreditSupplyTerms
// option; lots already submitted keep the rate they were submitted under.
function TermsCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const terms = useQuery({
    queryKey: ['credit-supply', 'terms'],
    queryFn: getCreditSupplyTerms,
  })
  const [draft, setDraft] = useState<CreditSupplyTerms | null>(null)
  const current = draft ?? terms.data ?? null
  const save = useMutation({
    mutationFn: (value: CreditSupplyTerms) =>
      updateSystemOption({
        key: 'CreditSupplyTerms',
        value: JSON.stringify(value),
      }),
    onSuccess: () => {
      toast.success(
        t('Buy terms saved. New sales use them; existing lots keep their rate.')
      )
      setDraft(null)
      queryClient.invalidateQueries({ queryKey: ['credit-supply'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })
  if (!current) return null
  const vendors = Object.keys(current.buy_rates).sort()
  const setRate = (vendor: string, pct: string) => {
    const value = Number(pct)
    setDraft({
      ...current,
      buy_rates: {
        ...current.buy_rates,
        [vendor]: Number.isFinite(value) ? value / 100 : 0,
      },
    })
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-base'>{t('Buy terms')}</CardTitle>
        <CardDescription>
          {t(
            'What we pay per dollar of each vendor’s credit, shown to sellers before they submit. 20% means a seller of $1,000 Anthropic credit receives $200.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='grid gap-3'>
        <div className='grid gap-3 sm:grid-cols-3'>
          {vendors.map((vendor) => (
            <div key={vendor} className='grid gap-1'>
              <Label className='capitalize'>{vendor}</Label>
              <div className='flex items-center gap-2'>
                <Input
                  type='number'
                  min={1}
                  max={99}
                  step={1}
                  value={Math.round((current.buy_rates[vendor] ?? 0) * 100)}
                  onChange={(event) => setRate(vendor, event.target.value)}
                  data-testid={`buy-rate-${vendor}`}
                />
                <span className='text-muted-foreground text-sm'>%</span>
              </div>
            </div>
          ))}
        </div>
        <div className='grid gap-3 sm:grid-cols-3'>
          <div className='grid gap-1'>
            <Label>{t('Minimum sale (USD)')}</Label>
            <Input
              type='number'
              min={0}
              value={current.min_face_usd}
              onChange={(event) =>
                setDraft({
                  ...current,
                  min_face_usd: Number(event.target.value),
                })
              }
            />
          </div>
          <div className='grid gap-1'>
            <Label>{t('Supplier channel priority')}</Label>
            <Input
              type='number'
              min={0}
              value={current.channel_priority}
              onChange={(event) =>
                setDraft({
                  ...current,
                  channel_priority: Number(event.target.value),
                })
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Higher than your own channels, so bought credits are consumed first.'
              )}
            </p>
          </div>
          <div className='grid gap-1'>
            <Label>{t('Bonus for platform credit (%)')}</Label>
            <Input
              type='number'
              min={0}
              max={100}
              value={Math.round((current.platform_credit_bonus ?? 0) * 100)}
              onChange={(event) =>
                setDraft({
                  ...current,
                  platform_credit_bonus: Number(event.target.value) / 100,
                })
              }
            />
          </div>
        </div>
        <div className='flex justify-end gap-2'>
          <Button
            type='button'
            variant='outline'
            disabled={draft === null}
            onClick={() => setDraft(null)}
          >
            {t('Reset')}
          </Button>
          <Button
            type='button'
            disabled={draft === null || save.isPending}
            onClick={() => save.mutate(current)}
          >
            {t('Save buy terms')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
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
            'Anyone can sell us vendor credits at the posted buy rates. A sale is verified automatically (one request through the key), then waits for you to pay it; paying books the payout and enables the channel at supplier priority, so bought credits are consumed first and drawn down at list price. The rate paid becomes the channel’s purchasing cost ratio, so Profit already reflects it. Suppliers are paid once, here — never per period from Settlement.'
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
          <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-5'>
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
              label={t('Awaiting payment')}
              value={formatUSD(overview.data.awaiting_payment_usd)}
              hint={t('Verified sales; pay to activate')}
              warn={overview.data.awaiting_payment_usd > 0}
            />
            <Headline
              label={t('Paid to suppliers')}
              value={formatUSD(overview.data.paid_usd)}
              hint={t('{{consumed}} of it consumed so far', {
                consumed: formatUSD(overview.data.payable_usd),
              })}
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

      <TermsCard />

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
