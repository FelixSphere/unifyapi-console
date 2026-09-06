/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the seller's page behind the "Credit Supply" sidebar entry.
// Anyone can sell: the posted rates are the first thing on the page, the sale
// is verified while they wait, and the lot shows exactly where their payment
// stands. Nothing is consumed before they have been paid.

import { useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  LOT_STATUS_LABELS,
  consumedPct,
  formatRate,
  formatUSD,
} from '@/features/system-settings/billing/credit-supply-logic'

import {
  getSupplierPortal,
  getSupplierTerms,
  getSupplierUsage,
  type SupplierLot,
  type SupplierTerms,
  type SupplierVendorPreset,
} from './api'
import { SubmitLotDialog } from './components/submit-lot-dialog'

const VENDOR_LABELS: Record<string, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  google: 'Google',
}

export function SupplierPortal() {
  const { t } = useTranslation()
  const [sellOpen, setSellOpen] = useState(false)

  // Terms are public to any login; /me is 404 until the first sale.
  const terms = useQuery({
    queryKey: ['supplier', 'terms'],
    queryFn: getSupplierTerms,
  })
  const me = useQuery({
    queryKey: ['supplier', 'me'],
    queryFn: getSupplierPortal,
    retry: false,
  })
  const usage = useQuery({
    queryKey: ['supplier', 'usage', 30],
    queryFn: () => getSupplierUsage(30),
    enabled: me.isSuccess,
  })

  const usageTotals = useMemo(() => {
    const rows = usage.data ?? []
    return {
      requests: rows.reduce((sum, row) => sum + row.requests, 0),
      face: rows.reduce((sum, row) => sum + row.face_usd, 0),
      peak: rows.reduce((max, row) => Math.max(max, row.face_usd), 0),
    }
  }, [usage.data])

  const data = me.data
  const activeTerms: SupplierTerms | undefined = data?.terms ?? terms.data
  const vendors: SupplierVendorPreset[] =
    data?.vendors ?? terms.data?.vendors ?? []
  const canSell = Boolean(activeTerms) && data?.supplier.status !== 'suspended'
  const payments = (data?.lots ?? []).filter((lot) => lot.paid_at > 0)

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Sell credits')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          type='button'
          size='sm'
          disabled={!canSell}
          onClick={() => setSellOpen(true)}
        >
          <Plus className='size-4' />
          {t('Sell credits')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          {activeTerms ? (
            <Card>
              <CardHeader>
                <CardTitle className='text-base'>
                  {t('Turn unused vendor credits into cash or platform credit')}
                </CardTitle>
                <CardDescription>
                  {t(
                    'Submit the credit balance and the API key; we verify the key with one small request while you wait, pay you the posted rate, and only then start routing customer traffic through your account. Credits are drawn down at the vendor’s list price and you can watch it happen here.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='grid gap-3 sm:grid-cols-3'>
                {Object.entries(activeTerms.buy_rates)
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([vendor, rate]) => (
                    <div
                      key={vendor}
                      className='bg-muted/40 rounded-lg p-3'
                      data-testid={`posted-rate-${vendor}`}
                    >
                      <div className='text-muted-foreground text-xs'>
                        {VENDOR_LABELS[vendor] ?? vendor}
                      </div>
                      <div className='text-2xl font-semibold tabular-nums'>
                        {formatRate(rate)}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        {t('per $1 of credit · min. {{min}}', {
                          min: formatUSD(activeTerms.min_face_usd),
                        })}
                      </div>
                    </div>
                  ))}
                {activeTerms.platform_credit_bonus > 0 ? (
                  <p className='text-muted-foreground text-xs sm:col-span-3'>
                    {t(
                      'Take platform credit instead of a transfer and receive {{bonus}}% more, instantly.',
                      {
                        bonus: Math.round(
                          activeTerms.platform_credit_bonus * 100
                        ),
                      }
                    )}
                  </p>
                ) : null}
              </CardContent>
            </Card>
          ) : null}

          {data ? (
            <>
              <div className='flex flex-wrap items-center gap-3'>
                <h2 className='text-lg font-semibold'>{data.supplier.name}</h2>
                <code className='text-muted-foreground text-xs'>
                  {data.supplier.counterparty}
                </code>
                {data.supplier.status === 'suspended' ? (
                  <Badge variant='secondary'>{t('Suspended')}</Badge>
                ) : null}
                {data.supplier.status_reason ? (
                  <span className='text-muted-foreground text-xs'>
                    {data.supplier.status_reason}
                  </span>
                ) : null}
              </div>

              <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-5'>
                <Headline
                  label={t('Sold')}
                  value={formatUSD(data.totals.face_usd)}
                  hint={t('Face value at list price')}
                />
                <Headline
                  label={t('Awaiting payment')}
                  value={formatUSD(data.totals.awaiting_payment_usd)}
                  hint={t('Verified sales not yet paid')}
                />
                <Headline
                  label={t('Paid to you')}
                  value={formatUSD(data.totals.paid_usd)}
                  hint={t('Across all sales')}
                />
                <Headline
                  label={t('Consumed')}
                  value={formatUSD(data.totals.consumed_usd)}
                  hint={t('Drawn down by customer traffic')}
                />
                <Headline
                  label={t('Remaining')}
                  value={formatUSD(data.totals.remaining_usd)}
                  hint={t('Still to be drawn')}
                />
              </div>

              <Card>
                <CardHeader>
                  <CardTitle className='text-base'>{t('Your sales')}</CardTitle>
                  <CardDescription>
                    {t(
                      'Each sale is one tranche of credits. Verifying → awaiting payment → paid & in use → fully drawn or expired.'
                    )}
                  </CardDescription>
                </CardHeader>
                <CardContent className='overflow-x-auto'>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('Sale')}</TableHead>
                        <TableHead className='min-w-56'>
                          {t('Drawn down')}
                        </TableHead>
                        <TableHead>{t('Rate')}</TableHead>
                        <TableHead>{t('Payment')}</TableHead>
                        <TableHead>{t('Expires')}</TableHead>
                        <TableHead>{t('Status')}</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {data.lots.map((lot) => (
                        <TableRow key={lot.id}>
                          <TableCell>
                            <div className='font-medium'>
                              #{lot.id} ·{' '}
                              {VENDOR_LABELS[lot.vendor] ?? lot.vendor}
                            </div>
                            <div className='text-muted-foreground text-xs'>
                              {new Date(
                                lot.created_at * 1000
                              ).toLocaleDateString()}
                            </div>
                          </TableCell>
                          <TableCell>
                            <div className='flex items-center gap-2'>
                              <Progress
                                value={consumedPct(lot)}
                                className='h-2 w-32'
                              />
                              <span className='text-xs tabular-nums'>
                                {formatUSD(lot.consumed_usd)} /{' '}
                                {formatUSD(lot.face_value_usd)}
                              </span>
                            </div>
                          </TableCell>
                          <TableCell className='tabular-nums'>
                            {formatRate(lot.acquisition_rate)}
                          </TableCell>
                          <TableCell className='tabular-nums'>
                            <PaymentCell lot={lot} />
                          </TableCell>
                          <TableCell className='text-sm'>
                            {lot.expires_at
                              ? new Date(
                                  lot.expires_at * 1000
                                ).toLocaleDateString()
                              : t('No expiry')}
                          </TableCell>
                          <TableCell>
                            <Badge variant={lotBadgeVariant(lot.status)}>
                              {t(LOT_STATUS_LABELS[lot.status])}
                            </Badge>
                            {lot.status_reason ? (
                              <div className='text-muted-foreground mt-1 max-w-56 text-xs'>
                                {lot.status_reason}
                              </div>
                            ) : null}
                          </TableCell>
                        </TableRow>
                      ))}
                      {data.lots.length === 0 ? (
                        <TableRow>
                          <TableCell
                            colSpan={6}
                            className='text-muted-foreground text-center'
                          >
                            {t('No sales yet.')}
                          </TableCell>
                        </TableRow>
                      ) : null}
                    </TableBody>
                  </Table>
                </CardContent>
              </Card>

              <div className='grid gap-4 lg:grid-cols-2'>
                <Card>
                  <CardHeader>
                    <CardTitle className='text-base'>
                      {t('Draw-down, last 30 days')}
                    </CardTitle>
                    <CardDescription>
                      {t('{{requests}} requests · {{face}} at list price', {
                        requests: usageTotals.requests.toLocaleString(),
                        face: formatUSD(usageTotals.face),
                      })}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='grid gap-1.5'>
                    {(usage.data ?? []).map((row) => (
                      <div
                        key={row.day}
                        className='grid grid-cols-[5.5rem_1fr_6rem] items-center gap-2 text-xs'
                      >
                        <span className='text-muted-foreground tabular-nums'>
                          {row.day}
                        </span>
                        <div className='bg-muted h-2 overflow-hidden rounded'>
                          <div
                            className='bg-primary h-full'
                            style={{
                              width: `${usageTotals.peak > 0 ? (row.face_usd / usageTotals.peak) * 100 : 0}%`,
                            }}
                          />
                        </div>
                        <span className='text-right tabular-nums'>
                          {formatUSD(row.face_usd)}
                        </span>
                      </div>
                    ))}
                    {(usage.data ?? []).length === 0 ? (
                      <p className='text-muted-foreground text-sm'>
                        {t('No traffic yet.')}
                      </p>
                    ) : null}
                  </CardContent>
                </Card>

                <Card>
                  <CardHeader>
                    <CardTitle className='text-base'>{t('Payments')}</CardTitle>
                    <CardDescription>
                      {t(
                        'One payment per sale, made before your credits are used.'
                      )}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='grid gap-2'>
                    {payments.map((lot) => (
                      <div
                        key={lot.id}
                        className='flex flex-wrap items-center justify-between gap-2 rounded border px-3 py-2 text-sm'
                      >
                        <span>
                          {t('Sale #{{id}}', { id: lot.id })} ·{' '}
                          {VENDOR_LABELS[lot.vendor] ?? lot.vendor}
                          <span className='text-muted-foreground'>
                            {' '}
                            ·{' '}
                            {new Date(lot.paid_at * 1000).toLocaleDateString()}
                          </span>
                        </span>
                        <span className='flex items-center gap-2 tabular-nums'>
                          {formatUSD(lot.paid_usd)}
                          <Badge variant='outline'>
                            {lot.payout_method === 'platform_credit'
                              ? t('Platform credit')
                              : lot.payout_reference || t('Transfer')}
                          </Badge>
                        </span>
                      </div>
                    ))}
                    {payments.length === 0 ? (
                      <p className='text-muted-foreground text-sm'>
                        {t('No payments yet.')}
                      </p>
                    ) : null}
                  </CardContent>
                </Card>
              </div>
            </>
          ) : null}
          {!data && me.isLoading ? (
            <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
          ) : null}
          {!data && !me.isLoading ? (
            <p className='text-muted-foreground text-sm'>
              {t(
                'You have not sold any credits yet. Your sales, payments and draw-down will appear here.'
              )}
            </p>
          ) : null}
        </div>

        {/* Inside the Content slot on purpose: SectionPageLayout renders only
            its named slots and silently drops every other child, so a dialog
            parked next to them never mounts and its trigger goes dead. Pinned
            by components/layout/__tests__/section-page-layout-slots.test.tsx. */}
        {activeTerms ? (
          <SubmitLotDialog
            open={sellOpen}
            onOpenChange={setSellOpen}
            vendors={vendors}
            terms={activeTerms}
          />
        ) : null}
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function PaymentCell({ lot }: { lot: SupplierLot }) {
  const { t } = useTranslation()
  if (lot.paid_at) {
    return (
      <>
        <div>{formatUSD(lot.paid_usd)}</div>
        <div className='text-muted-foreground text-xs'>
          {lot.payout_method === 'platform_credit'
            ? t('platform credit, {{date}}', {
                date: new Date(lot.paid_at * 1000).toLocaleDateString(),
              })
            : t('transfer {{ref}}', { ref: lot.payout_reference || '—' })}
        </div>
      </>
    )
  }
  if (lot.status === 'verified') {
    return (
      <>
        <div>{formatUSD(lot.payout_usd)}</div>
        <div className='text-muted-foreground text-xs'>
          {t('due · we pay, then use')}
        </div>
      </>
    )
  }
  if (lot.status === 'rejected') {
    return <span className='text-muted-foreground'>—</span>
  }
  return (
    <span className='text-muted-foreground'>{formatUSD(lot.payout_usd)}</span>
  )
}

function Headline({
  label,
  value,
  hint,
}: {
  label: string
  value: string
  hint: string
}) {
  return (
    <Card>
      <CardHeader className='pb-2'>
        <CardDescription>{label}</CardDescription>
        <CardTitle className='text-2xl tabular-nums'>{value}</CardTitle>
      </CardHeader>
      <CardContent className='text-muted-foreground text-xs'>
        {hint}
      </CardContent>
    </Card>
  )
}

function lotBadgeVariant(
  status: string
): 'default' | 'destructive' | 'secondary' {
  if (status === 'active') return 'default'
  if (status === 'rejected') return 'destructive'
  return 'secondary'
}
