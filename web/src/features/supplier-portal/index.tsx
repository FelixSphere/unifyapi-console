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
import { Landmark, Plus } from 'lucide-react'
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
  formatUSD,
} from '@/features/system-settings/billing/credit-supply-logic'

import {
  PAYOUT_METHOD_LABELS,
  getSupplierPortal,
  getSupplierTerms,
  getSupplierUsage,
  type SupplierLot,
  type SupplierTerms,
  type SupplierVendorPreset,
} from './api'
import { PayoutAccountDialog } from './components/payout-account-dialog'
import { SubmitLotDialog } from './components/submit-lot-dialog'
import { UsageProofCard } from './components/usage-proof-card'

const VENDOR_LABELS: Record<string, string> = {
  anthropic: 'Anthropic',
  openai: 'OpenAI',
  google: 'Google',
}

export function SupplierPortal() {
  const { t } = useTranslation()
  const [sellOpen, setSellOpen] = useState(false)
  const [payoutOpen, setPayoutOpen] = useState(false)

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
      revenue: rows.reduce((sum, row) => sum + (row.revenue_usd ?? 0), 0),
      peak: rows.reduce((max, row) => Math.max(max, row.face_usd), 0),
    }
  }, [usage.data])

  const data = me.data
  const activeTerms: SupplierTerms | undefined = data?.terms ?? terms.data
  const vendors: SupplierVendorPreset[] =
    data?.vendors ?? terms.data?.vendors ?? []
  const hasPayoutAccount = Boolean(data?.supplier.has_payout_account)
  const canSell =
    Boolean(activeTerms) &&
    data?.supplier.status !== 'suspended' &&
    hasPayoutAccount
  const payoutAccount = data?.supplier
    ? {
        method: data.supplier.payout_method,
        holder: data.supplier.payout_holder,
        details: data.supplier.payout_details,
        currency: data.supplier.payout_currency,
      }
    : null

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Sell credits')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {hasPayoutAccount ? (
          <Button
            type='button'
            size='sm'
            disabled={!canSell}
            onClick={() => setSellOpen(true)}
          >
            <Plus className='size-4' />
            {t('Sell credits')}
          </Button>
        ) : (
          <Button
            type='button'
            size='sm'
            disabled={!activeTerms}
            onClick={() => setPayoutOpen(true)}
          >
            <Landmark className='size-4' />
            {t('Add payout account to start')}
          </Button>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          {activeTerms ? (
            <Card>
              <CardHeader>
                <CardTitle className='text-base'>
                  {t(
                    'Sell unused vendor credits for a share of what they sell for'
                  )}
                </CardTitle>
                <CardDescription>
                  {t(
                    'Submit the credit balance and the API key. We verify the key with one small request while you wait, then route paying customer traffic through your account and draw the credits down at the vendor’s list price. You keep the posted share of what they sell for, paid out as the balance builds up — nothing is paid up front, nothing is capped.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='grid gap-3 sm:grid-cols-3'>
                {Object.entries(activeTerms.revenue_share_rates ?? {})
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([vendor, share]) => (
                    <div
                      key={vendor}
                      className='bg-muted/40 rounded-lg p-3'
                      data-testid={`posted-share-${vendor}`}
                    >
                      <div className='text-muted-foreground text-xs'>
                        {VENDOR_LABELS[vendor] ?? vendor}
                      </div>
                      <div className='text-2xl font-semibold tabular-nums'>
                        {t('you keep {{share}}%', {
                          share: Math.round(share * 100),
                        })}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        {t(
                          'of what your credits sell for · min. {{min}} balance',
                          {
                            min: formatUSD(activeTerms.min_face_usd),
                          }
                        )}
                      </div>
                    </div>
                  ))}
                <p className='text-muted-foreground text-xs sm:col-span-3'>
                  {activeTerms.min_share_payout_usd > 0
                    ? t(
                        'Settled once you are owed at least {{min}}, to your payout account or as platform credit.',
                        { min: formatUSD(activeTerms.min_share_payout_usd) }
                      )
                    : t(
                        'Settled to your payout account or as platform credit.'
                      )}
                  {activeTerms.manual_review
                    ? ` ${t('Verified keys go live once the operator has accepted them.')}`
                    : ` ${t('Verified keys go live immediately.')}`}
                </p>
              </CardContent>
            </Card>
          ) : null}

          <Card>
            <CardHeader className='pb-3'>
              <CardTitle className='flex items-center gap-2 text-base'>
                <Landmark className='size-4' />
                {t('Payout account')}
              </CardTitle>
              <CardDescription>
                {hasPayoutAccount
                  ? t(
                      'Where your share is paid. Only the operator who pays you can read it.'
                    )
                  : t(
                      'Before you can sell, tell us where to send your share. Nothing is paid before your credits are used, so this has to be on file first.'
                    )}
              </CardDescription>
            </CardHeader>
            <CardContent className='flex flex-wrap items-center justify-between gap-3 text-sm'>
              {hasPayoutAccount && data ? (
                <div>
                  <div className='font-medium'>
                    {t(
                      PAYOUT_METHOD_LABELS[
                        data.supplier
                          .payout_method as keyof typeof PAYOUT_METHOD_LABELS
                      ] ?? data.supplier.payout_method
                    )}
                    {data.supplier.payout_currency
                      ? ` · ${data.supplier.payout_currency}`
                      : ''}
                  </div>
                  {data.supplier.payout_holder ? (
                    <div className='text-muted-foreground text-xs'>
                      {data.supplier.payout_holder} ·{' '}
                      {maskTail(data.supplier.payout_details)}
                    </div>
                  ) : null}
                </div>
              ) : (
                <span className='text-muted-foreground'>
                  {t('No payout account yet.')}
                </span>
              )}
              <Button
                type='button'
                size='sm'
                variant={hasPayoutAccount ? 'outline' : 'default'}
                onClick={() => setPayoutOpen(true)}
              >
                {hasPayoutAccount ? t('Change') : t('Add payout account')}
              </Button>
            </CardContent>
          </Card>

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
                  label={t('Drawn down')}
                  value={formatUSD(data.totals.consumed_usd)}
                  hint={t('Of {{face}} at the vendor’s list price', {
                    face: formatUSD(data.totals.face_usd),
                  })}
                />
                <Headline
                  label={t('Sold for')}
                  value={formatUSD(data.totals.share_revenue_usd)}
                  hint={t('What customers paid for traffic on your keys')}
                />
                <Headline
                  label={t('Your share')}
                  value={formatUSD(data.totals.share_earned_usd)}
                  hint={t('Earned to date at your posted share')}
                />
                <Headline
                  label={t('Paid to you')}
                  value={formatUSD(data.totals.share_paid_usd)}
                  hint={t('Across all payouts')}
                />
                <Headline
                  label={t('Unpaid')}
                  value={formatUSD(data.totals.share_unpaid_usd)}
                  hint={
                    activeTerms && activeTerms.min_share_payout_usd > 0
                      ? t('Settled once it reaches {{min}}', {
                          min: formatUSD(activeTerms.min_share_payout_usd),
                        })
                      : t('Settled by the operator')
                  }
                />
              </div>

              <Card>
                <CardHeader>
                  <CardTitle className='text-base'>{t('Your sales')}</CardTitle>
                  <CardDescription>
                    {t(
                      'Each key is one tranche of credits. Verifying → in use → fully drawn or expired. You are paid a share of what each one sells for.'
                    )}
                  </CardDescription>
                </CardHeader>
                <CardContent className='overflow-x-auto'>
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>{t('Key')}</TableHead>
                        <TableHead className='min-w-56'>
                          {t('Drawn down')}
                        </TableHead>
                        <TableHead>{t('Sold for')}</TableHead>
                        <TableHead>{t('Your share')}</TableHead>
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
                            <SoldForCell lot={lot} />
                          </TableCell>
                          <TableCell className='tabular-nums'>
                            <ShareCell lot={lot} />
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
                            {t('No keys yet.')}
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
                      {usageTotals.revenue > 0
                        ? ` · ${t('{{revenue}} of revenue served', {
                            revenue: formatUSD(usageTotals.revenue),
                          })}`
                        : null}
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

                <UsageProofCard
                  vendorLabel={
                    data.lots[0]
                      ? (VENDOR_LABELS[data.lots[0].vendor] ??
                        data.lots[0].vendor)
                      : t('your vendor')
                  }
                />

                <Card>
                  <CardHeader>
                    <CardTitle className='text-base'>{t('Payments')}</CardTitle>
                    <CardDescription>
                      {t(
                        'Your share is settled as it builds up; each payout is listed here with how it was sent.'
                      )}
                    </CardDescription>
                  </CardHeader>
                  <CardContent className='grid gap-2'>
                    {(data.share_payouts ?? []).map((payout) => (
                      <div
                        key={`share-${payout.id}`}
                        className='flex flex-wrap items-center justify-between gap-2 rounded border px-3 py-2 text-sm'
                      >
                        <span>
                          {t('Revenue share on key #{{id}}', {
                            id: payout.lot_id,
                          })}
                          <span className='text-muted-foreground'>
                            {' '}
                            ·{' '}
                            {new Date(
                              payout.created_at * 1000
                            ).toLocaleDateString()}
                          </span>
                        </span>
                        <span className='flex items-center gap-2 tabular-nums'>
                          {formatUSD(payout.amount_usd)}
                          <Badge variant='outline'>
                            {payout.method === 'platform_credit'
                              ? t('Platform credit')
                              : payout.reference || t('Transfer')}
                          </Badge>
                        </span>
                      </div>
                    ))}
                    {(data.share_payouts ?? []).length === 0 ? (
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
                'You have not submitted any credits yet. Add your payout account, then submit a key; what it sells for and your share will appear here.'
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
        <PayoutAccountDialog
          open={payoutOpen}
          onOpenChange={setPayoutOpen}
          current={payoutAccount}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function SoldForCell({ lot }: { lot: SupplierLot }) {
  const { t } = useTranslation()
  return (
    <>
      <div>{formatUSD(lot.share_revenue_usd)}</div>
      <div className='text-muted-foreground text-xs'>
        {t('paid by customers')}
      </div>
    </>
  )
}

function ShareCell({ lot }: { lot: SupplierLot }) {
  const { t } = useTranslation()
  return (
    <>
      <div>
        {formatUSD(lot.share_earned_usd)}{' '}
        <span className='text-muted-foreground text-xs'>
          ({Math.round(lot.revenue_share_pct * 100)}%)
        </span>
      </div>
      <div className='text-muted-foreground text-xs'>
        {lot.share_unpaid_usd > 0
          ? t('{{unpaid}} unpaid', { unpaid: formatUSD(lot.share_unpaid_usd) })
          : t('all paid')}
      </div>
    </>
  )
}

// maskTail keeps the last four characters so the seller recognises their own
// account; the full details are one click away in the dialog.
function maskTail(details: string): string {
  const d = details.trim()
  if (d.length <= 4) return '••••'
  return `••••${d.slice(-4)}`
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
