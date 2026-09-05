/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the supplier portal. A credit supplier's own slice of the
// pool: their lots, how fast they are being drawn down, and the statements
// issued to them. They can submit new credits; approval stays with the
// operator. See docs/credit-pool.md.

import { useQuery } from '@tanstack/react-query'
import { Coins, Plus } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
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
  getSupplierStatements,
  getSupplierUsage,
} from './api'
import { SubmitLotDialog } from './components/submit-lot-dialog'

const STATEMENT_STATUS_LABELS = {
  issued: 'Issued',
  settled: 'Paid',
  void: 'Withdrawn',
} as const

export function SupplierPortal() {
  const { t } = useTranslation()
  const [submitOpen, setSubmitOpen] = useState(false)

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
  const statements = useQuery({
    queryKey: ['supplier', 'statements'],
    queryFn: getSupplierStatements,
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

  if (me.isError) {
    return (
      <SectionPageLayout>
        <SectionPageLayout.Title>
          {t('Supplier portal')}
        </SectionPageLayout.Title>
        <SectionPageLayout.Content>
          <EmptyState
            icon={Coins}
            title={t('This account is not a credit supplier')}
            description={t(
              'If you hold vendor credits you would like to sell to us, contact the operator. Once your login is linked to a supplier record, your lots, draw-down and statements appear here.'
            )}
          />
        </SectionPageLayout.Content>
      </SectionPageLayout>
    )
  }

  const data = me.data

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Supplier portal')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {data ? (
          <Button
            type='button'
            size='sm'
            disabled={data.supplier.status !== 'active'}
            onClick={() => setSubmitOpen(true)}
          >
            <Plus className='size-4' />
            {t('Submit credits')}
          </Button>
        ) : null}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        {me.isLoading || !data ? (
          <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
        ) : (
          <div className='flex flex-col gap-4'>
            <div className='flex flex-wrap items-center gap-3'>
              <h2 className='text-lg font-semibold'>{data.supplier.name}</h2>
              <code className='text-muted-foreground text-xs'>
                {data.supplier.counterparty}
              </code>
              <Badge
                variant={
                  data.supplier.status === 'active' ? 'default' : 'secondary'
                }
              >
                {data.supplier.status === 'active'
                  ? t('Active')
                  : t('Suspended')}
              </Badge>
            </div>

            <div className='grid gap-3 sm:grid-cols-2 lg:grid-cols-4'>
              <Headline
                label={t('Face value supplied')}
                value={formatUSD(data.totals.face_usd)}
                hint={t('At the vendor’s list price')}
              />
              <Headline
                label={t('Consumed')}
                value={formatUSD(data.totals.consumed_usd)}
                hint={t('Drawn down by customer traffic')}
              />
              <Headline
                label={t('Remaining')}
                value={formatUSD(data.totals.remaining_usd)}
                hint={t('Across your live lots')}
              />
              <Headline
                label={t('Owed to you to date')}
                value={formatUSD(data.totals.payable_usd)}
                hint={t('Consumed × your rate; issued monthly as a statement')}
              />
            </div>

            <Card>
              <CardHeader>
                <CardTitle className='text-base'>{t('Your lots')}</CardTitle>
                <CardDescription>
                  {t(
                    'A lot is one tranche of credits on one channel. Pending lots are waiting for the operator; retired lots were fully drawn or expired.'
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className='overflow-x-auto'>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Lot')}</TableHead>
                      <TableHead className='min-w-56'>
                        {t('Drawn down')}
                      </TableHead>
                      <TableHead>{t('Rate')}</TableHead>
                      <TableHead>{t('Owed')}</TableHead>
                      <TableHead>{t('Expires')}</TableHead>
                      <TableHead>{t('Status')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {data.lots.map((lot) => (
                      <TableRow key={lot.id}>
                        <TableCell>
                          <div className='font-medium'>
                            #{lot.id} · {lot.vendor}
                          </div>
                          <div className='text-muted-foreground text-xs'>
                            {lot.channel_name || t('Not bound yet')}
                          </div>
                        </TableCell>
                        <TableCell>
                          <div className='flex items-baseline justify-between gap-2 text-sm'>
                            <span>
                              {formatUSD(lot.consumed_usd)}{' '}
                              <span className='text-muted-foreground'>
                                / {formatUSD(lot.face_value_usd)}
                              </span>
                            </span>
                            <span className='text-muted-foreground text-xs tabular-nums'>
                              {formatUSD(lot.remaining_usd)} {t('left')}
                            </span>
                          </div>
                          <Progress value={consumedPct(lot)} className='mt-1' />
                        </TableCell>
                        <TableCell className='tabular-nums'>
                          {formatRate(lot.acquisition_rate)}
                        </TableCell>
                        <TableCell className='tabular-nums'>
                          {formatUSD(lot.payable_usd)}
                        </TableCell>
                        <TableCell className='text-sm'>
                          {lot.expires_at
                            ? new Date(
                                lot.expires_at * 1000
                              ).toLocaleDateString()
                            : t('No expiry')}
                        </TableCell>
                        <TableCell>
                          <Badge
                            variant={
                              lot.status === 'active' ? 'default' : 'secondary'
                            }
                          >
                            {t(LOT_STATUS_LABELS[lot.status])}
                          </Badge>
                          {lot.status_reason ? (
                            <div className='text-muted-foreground mt-1 max-w-48 text-xs'>
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
                          {t(
                            'No lots yet. Submit your first tranche of credits.'
                          )}
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
                  <CardTitle className='text-base'>{t('Statements')}</CardTitle>
                  <CardDescription>
                    {t(
                      'Issued once a calendar month closes. Each line is one model’s traffic on your channel, priced at list × your rate.'
                    )}
                  </CardDescription>
                </CardHeader>
                <CardContent className='grid gap-2'>
                  {(statements.data ?? []).map((statement) => (
                    <details
                      key={statement.id}
                      className='rounded border px-3 py-2 text-sm'
                    >
                      <summary className='flex cursor-pointer flex-wrap items-center justify-between gap-2'>
                        <span className='font-medium'>
                          {statement.period_start} → {statement.period_end}
                        </span>
                        <span className='flex items-center gap-2'>
                          <span className='tabular-nums'>
                            {formatUSD(statement.amount_usd)}
                          </span>
                          <Badge
                            variant={
                              statement.status === 'settled'
                                ? 'default'
                                : 'secondary'
                            }
                          >
                            {t(STATEMENT_STATUS_LABELS[statement.status])}
                          </Badge>
                        </span>
                      </summary>
                      {statement.lines && statement.lines.length > 0 ? (
                        <div className='mt-2 grid gap-1 text-xs'>
                          {statement.lines.map((line) => (
                            <div
                              key={`${line.model}-${line.channel_id ?? 0}`}
                              className='flex justify-between gap-2'
                            >
                              <span>
                                {line.model}{' '}
                                <span className='text-muted-foreground'>
                                  · {line.requests.toLocaleString()}{' '}
                                  {t('requests')}
                                </span>
                              </span>
                              <span className='tabular-nums'>
                                {formatUSD(line.amount_usd)}
                              </span>
                            </div>
                          ))}
                        </div>
                      ) : null}
                    </details>
                  ))}
                  {(statements.data ?? []).length === 0 ? (
                    <p className='text-muted-foreground text-sm'>
                      {t('No statements issued yet.')}
                    </p>
                  ) : null}
                </CardContent>
              </Card>
            </div>
          </div>
        )}
      </SectionPageLayout.Content>

      {data ? (
        <SubmitLotDialog
          open={submitOpen}
          onOpenChange={setSubmitOpen}
          vendors={data.vendors}
        />
      ) : null}
    </SectionPageLayout>
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
