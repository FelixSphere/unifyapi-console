/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: the settlement screen -- billing a customer, and paying an
upstream, from the same period's facts.

The profit screen answers "are we making money". This one produces the two
documents that follow from it: the statement you send a customer, and the
figure you check a vendor's invoice against. Both are folds over the same
consume-log rows the profit screen reads, so a bill can never disagree with the
dashboard it was reconciled against.

Three decisions worth stating, because they are what make this usable rather
than merely present:

  * PERIODS ARE CALENDAR MONTHS. Nobody invoices "the last 30 days". The
    current month is offered but marked as still accruing, because a bill
    issued from it is wrong by however much of the month is left.

  * ISSUING FREEZES THE STATEMENT. A customer's amount comes from the ledger
    and never moves, but an upstream's is modelled from today's catalog prices
    and today's purchasing ratios -- renegotiate a rate in December and
    re-running August gives a different August. Once a period has been acted
    on, the number has to stop moving; the row flags it when the live figure
    has since drifted from the frozen one.

  * THE SERVER OWNS OUR SIDE OF EVERY COMPARISON. The screen posts the
    counterparty's invoice, a note and a status. It never posts our own amount.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Download,
  FileText,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import {
  downloadFrozenSettlementCSV,
  downloadSettlementCSV,
  getSettlements,
  issueSettlement,
  printCustomerInvoice,
  updateSettlement,
} from '../api'
import { SettingsSection } from '../components/settings-section'
import type { SettlementRow, SettlementStatus, StatementKind } from '../types'
import {
  type SettlementState,
  type VarianceVerdict,
  PRIMARY_ACTION_LABELS,
  SETTLEMENT_STATE_LABELS,
  VARIANCE_VERDICT_LABELS,
  customerInvoiceUI,
  csvHref,
  deriveStatement,
  formatSigned,
  formatTokens,
  formatUSD,
  isPeriodClosed,
  periodFromMonthLabel,
  recentPeriods,
  settlementState,
  statementIsBalanced,
  varianceVerdict,
} from './settlement-logic'

/** expansionId scopes "which row is open" to the tab and period it was opened
 *  in, so switching either does not leave a same-named row in another period
 *  expanded. */
function expansionId(
  kind: StatementKind,
  periodLabel: string,
  counterparty: string
) {
  return `${kind}:${periodLabel}:${counterparty}`
}

export function SettlementSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [kind, setKind] = useState<StatementKind>('customer')
  // Scoped to the tab and period it was opened in, so switching either does not
  // leave an unrelated row expanded.
  const [expanded, setExpanded] = useState<string | null>(null)

  // Resolved once per render against the real clock, and part of the query key,
  // so crossing a month boundary refetches rather than serving the old period.
  const today = useMemo(() => new Date(), [])
  const periods = useMemo(() => recentPeriods(today), [today])
  const [periodLabel, setPeriodLabel] = useState(
    periods[1]?.label ?? periods[0].label
  )
  const period = periodFromMonthLabel(periodLabel) ?? periods[1] ?? periods[0]
  const closed = isPeriodClosed(period, today)
  const [downloading, setDownloading] = useState(false)

  const printInvoice = async (settlementId: number) => {
    setDownloading(true)
    try {
      await printCustomerInvoice(settlementId)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('Failed'))
    } finally {
      setDownloading(false)
    }
  }

  const downloadFrozenCSV = async (settlementId: number) => {
    setDownloading(true)
    try {
      await downloadFrozenSettlementCSV(settlementId)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('Failed'))
    } finally {
      setDownloading(false)
    }
  }

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['settlement', kind, period.start, period.end],
    queryFn: () =>
      getSettlements({ kind, start: period.start, end: period.end }),
  })

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['settlement'] })

  const issue = useMutation({
    mutationFn: issueSettlement,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.message ?? t('Failed to save'))
        return
      }
      toast.success(response.message ?? t('Recorded'))
      invalidate()
    },
    onError: (err: Error) => toast.error(err.message),
  })

  const update = useMutation({
    mutationFn: (input: {
      id: number
      invoiced_usd: number
      invoice_recorded: boolean
      status: SettlementStatus
      note: string
    }) => updateSettlement(input.id, input),
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.message ?? t('Failed to save'))
        return
      }
      toast.success(response.message ?? t('Recorded'))
      invalidate()
    },
    onError: (err: Error) => toast.error(err.message),
  })

  const rows = data?.data?.rows ?? []
  const totals = data?.data?.totals
  const orphaned = data?.data?.orphaned ?? []
  const busy = issue.isPending || update.isPending

  const download = async (counterparty?: string) => {
    setDownloading(true)
    try {
      await downloadSettlementCSV(csvHref(kind, period, counterparty))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('Failed'))
    } finally {
      setDownloading(false)
    }
  }

  return (
    <SettingsSection title={t('Settlement')}>
      <div className='flex flex-col gap-4'>
        <Tabs value={kind} onValueChange={(v) => setKind(v as StatementKind)}>
          <TabsList>
            <TabsTrigger value='customer'>{t('Customer bills')}</TabsTrigger>
            <TabsTrigger value='vendor'>{t('Upstream settlement')}</TabsTrigger>
          </TabsList>
        </Tabs>

        <div className='flex flex-wrap items-center gap-2'>
          <Tabs value={period.label} onValueChange={setPeriodLabel}>
            <TabsList>
              {periods.map((p) => (
                <TabsTrigger key={p.label} value={p.label}>
                  {p.label}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <Input
            type='month'
            className='h-8 w-36 font-mono text-xs'
            value={period.label}
            onChange={(event) => setPeriodLabel(event.target.value)}
            aria-label={t('Month')}
          />
          <span className='text-muted-foreground font-mono text-xs'>
            {period.start} → {period.end}
          </span>
          <Button
            variant='outline'
            size='sm'
            className='ml-auto'
            disabled={downloading}
            onClick={() => download()}
          >
            <Download className='size-4' />
            {t('Export all line items')}
          </Button>
        </div>

        {/*
          Said before any number, because it changes what issuing means: a bill
          cut from a month that has not ended is short by the rest of it.
        */}
        {!closed ? (
          <Alert>
            <AlertTriangle className='size-4' />
            <AlertDescription className='text-xs'>
              {t(
                'This period has not ended yet, so the amounts below are still accruing. A statement issued now will be short by the rest of the month.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        {data?.warning ? (
          <Alert variant='destructive'>
            <AlertDescription className='text-xs'>
              {data.warning}
            </AlertDescription>
          </Alert>
        ) : null}

        {kind === 'vendor' &&
        Object.keys(data?.cost_basis?.channel_cost_ratios ?? {}).length ===
          0 ? (
          <Alert variant='destructive'>
            <AlertTriangle className='size-4' />
            <AlertDescription className='text-xs'>
              {t(
                'No channel has a purchasing cost configured, so every amount below is the vendor list price rather than what we actually pay. Enter your negotiated rates under 上游采购成本 before settling from these figures.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        {totals ? <SettlementHeadline kind={kind} totals={totals} /> : null}

        {isLoading ? (
          <div className='text-muted-foreground p-6 text-sm'>
            {t('Loading...')}
          </div>
        ) : null}
        {!isLoading && isError ? (
          <Alert variant='destructive'>
            <AlertDescription>{(error as Error)?.message}</AlertDescription>
          </Alert>
        ) : null}

        {!isLoading && !isError ? (
          <div className='max-h-[60vh] min-h-0 overflow-auto rounded-md border'>
            <Table>
              <TableHeader className='bg-background sticky top-0 z-10'>
                <TableRow>
                  <TableHead className='w-8' />
                  <TableHead>
                    {kind === 'customer' ? t('Customer') : t('Upstream vendor')}
                  </TableHead>
                  <TableHead className='text-right'>{t('Requests')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Tokens in/out')}
                  </TableHead>
                  <TableHead className='text-right'>
                    {kind === 'customer'
                      ? t('Amount due')
                      : t('We owe (modelled)')}
                  </TableHead>
                  {kind === 'vendor' ? (
                    <TableHead className='text-right'>
                      {t('Their invoice')}
                    </TableHead>
                  ) : null}
                  <TableHead>{t('Status')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row) => (
                  <SettlementTableRow
                    /*
                      Keyed by period as well as counterparty. The invoice and
                      note fields are local draft state, so a key that ignored
                      the period would carry August's invoice number into
                      September's row and pre-fill a number nobody typed.
                    */
                    key={`${kind}:${period.label}:${row.statement.counterparty}`}
                    row={row}
                    kind={kind}
                    closed={closed}
                    open={
                      expanded ===
                      expansionId(
                        kind,
                        period.label,
                        row.statement.counterparty
                      )
                    }
                    busy={busy}
                    onToggle={() => {
                      const id = expansionId(
                        kind,
                        period.label,
                        row.statement.counterparty
                      )
                      setExpanded(expanded === id ? null : id)
                    }}
                    onIssue={(input) =>
                      issue.mutate({
                        kind,
                        counterparty: row.statement.counterparty,
                        start: period.start,
                        end: period.end,
                        ...input,
                      })
                    }
                    onUpdate={(input) =>
                      row.settlement &&
                      update.mutate({ id: row.settlement.id, ...input })
                    }
                    onReplace={(input) =>
                      issue.mutate({
                        kind,
                        counterparty: row.statement.counterparty,
                        start: period.start,
                        end: period.end,
                        ...input,
                      })
                    }
                    onDownload={() => download(row.statement.counterparty)}
                    onPrintInvoice={printInvoice}
                    onDownloadFrozenCSV={downloadFrozenCSV}
                  />
                ))}
                {rows.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={kind === 'vendor' ? 7 : 6}
                      className='text-muted-foreground py-8 text-center text-sm'
                    >
                      {t('No usage in this period.')}
                    </TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </div>
        ) : null}

        {/*
          Not folded into the table: a settlement whose counterparty has no
          traffic left in the period is the interesting case, not a stale row.
          Either the logs stopped attributing that traffic to them, or the
          statement was issued against the wrong period.
        */}
        {orphaned.length > 0 ? (
          <Alert variant='destructive'>
            <AlertTriangle className='size-4' />
            <AlertDescription className='text-xs'>
              {t(
                '{{count}} settlement(s) exist for this period against counterparties with no usage in it: {{names}}. Either the traffic is no longer attributed to them, or the statement was issued for the wrong period.',
                {
                  count: orphaned.length,
                  names: orphaned.map((s) => s.label).join(', '),
                }
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        <p className='text-muted-foreground text-xs'>
          {kind === 'customer'
            ? t(
                'Amounts are read from the consume-log ledger — exactly the quota deducted, already including the model discount, the group ratio and cache pricing. That is what makes a line defensible in a dispute.'
              )
            : t(
                'Amounts are modelled from token counts x the vendor official price x the channel purchasing ratio, because vendors issue no per-request receipt. Record their invoice to turn the estimate into a checked number.'
              )}
        </p>
      </div>
    </SettingsSection>
  )
}

function SettlementHeadline({
  kind,
  totals,
}: {
  kind: StatementKind
  totals: {
    counterparties: number
    requests: number
    amount_usd: number
    unpriced_requests: number
  }
}) {
  const { t } = useTranslation()
  return (
    <div className='grid gap-3 sm:grid-cols-3'>
      <Stat
        label={
          kind === 'customer' ? t('Customers billed') : t('Vendors to settle')
        }
        value={String(totals.counterparties)}
      />
      <Stat label={t('Requests')} value={totals.requests.toLocaleString()} />
      <Stat
        label={kind === 'customer' ? t('Total to invoice') : t('Total we owe')}
        value={formatUSD(totals.amount_usd)}
        sub={
          totals.unpriced_requests > 0
            ? t('{{count}} requests could not be priced', {
                count: totals.unpriced_requests,
              })
            : undefined
        }
        warn={totals.unpriced_requests > 0}
      />
    </div>
  )
}

function Stat({
  label,
  value,
  sub,
  warn,
}: {
  label: string
  value: string
  sub?: string
  warn?: boolean
}) {
  return (
    <div className='rounded-md border p-3'>
      <div className='text-muted-foreground text-xs'>{label}</div>
      <div className='font-mono text-xl font-semibold tabular-nums'>
        {value}
      </div>
      {sub ? (
        <div
          className={`text-xs ${warn ? 'text-destructive' : 'text-muted-foreground'}`}
        >
          {sub}
        </div>
      ) : null}
    </div>
  )
}

/** stateToneClass is the colour half of a row's state; the label half lives in
 *  SETTLEMENT_STATE_LABELS so the i18n test can see it. A switch rather than
 *  nested ternaries, so a new state cannot fall through unstyled. */
function stateToneClass(state: SettlementState): string {
  switch (state) {
    case 'settled':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'issued':
      return ''
    case 'drifted':
      return 'text-amber-600 dark:text-amber-400'
    case 'void':
      return 'text-muted-foreground line-through'
    default:
      return 'text-muted-foreground'
  }
}

function varianceClass(verdict: VarianceVerdict): string {
  switch (verdict) {
    case 'reconciled':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'pending':
      return 'text-muted-foreground'
    default:
      return 'text-destructive font-semibold'
  }
}

function SettlementTableRow({
  row,
  kind,
  closed,
  open,
  busy,
  onToggle,
  onIssue,
  onUpdate,
  onDownload,
  onPrintInvoice,
  onDownloadFrozenCSV,
  onReplace,
}: {
  row: SettlementRow
  kind: StatementKind
  closed: boolean
  open: boolean
  busy: boolean
  onToggle: () => void
  onIssue: (input: {
    invoiced_usd?: number
    invoice_recorded?: boolean
    status?: SettlementStatus
    note?: string
  }) => void
  onUpdate: (input: {
    invoiced_usd: number
    invoice_recorded: boolean
    status: SettlementStatus
    note: string
  }) => void
  onDownload: () => void
  onPrintInvoice: (id: number) => void
  onDownloadFrozenCSV: (id: number) => void
  onReplace: (input: {
    status: SettlementStatus
    note: string
    replace_existing: true
    replacement_reason: string
    replacement_compliance_confirmed: true
  }) => void
}) {
  const { t } = useTranslation()
  const statement = row.statement
  const state = settlementState(row)
  const verdict = varianceVerdict(row)
  const customerInvoice = customerInvoiceUI(state)
  const displayedStatement = row.issued_statement ?? statement

  const [invoiceDraft, setInvoiceDraft] = useState<string>(
    row.settlement?.invoice_recorded ? String(row.settlement.invoiced_usd) : ''
  )
  const [noteDraft, setNoteDraft] = useState(row.settlement?.note ?? '')

  const parsedInvoice = Number.parseFloat(invoiceDraft)
  const invoiceRecorded =
    invoiceDraft.trim() !== '' && Number.isFinite(parsedInvoice)

  const recordInvoice = () => {
    const payload = {
      invoiced_usd: invoiceRecorded ? parsedInvoice : 0,
      invoice_recorded: invoiceRecorded,
      status: 'issued' as SettlementStatus,
      note: noteDraft,
    }
    if (row.settlement) {
      onUpdate({ ...payload, status: row.settlement.status })
      return
    }
    onIssue(payload)
  }

  const replaceInvoice = () => {
    const defaultReason = row.issuance_blocked
      ? 'Consolidated into the current Pricing Group invoice'
      : ''
    const reason = window.prompt(
      'Reason for replacing the existing invoice (required)',
      defaultReason
    )
    if (!reason?.trim()) return
    const confirmed = window.confirm(
      'The previous invoice will remain in the audit trail as SUPERSEDED and a new invoice number will be created. If it was submitted to MyInvois, cancel it within the permitted window or complete the required credit/debit/refund note first. Continue?'
    )
    if (!confirmed) return
    onReplace({
      status: 'issued',
      note: noteDraft,
      replace_existing: true,
      replacement_reason: reason.trim(),
      replacement_compliance_confirmed: true,
    })
  }

  return (
    <>
      <TableRow
        className='hover:bg-muted/50 cursor-pointer'
        onClick={onToggle}
        aria-expanded={open}
      >
        <TableCell className='py-2'>
          {open ? (
            <ChevronDown className='size-4' />
          ) : (
            <ChevronRight className='size-4' />
          )}
        </TableCell>
        <TableCell className='font-mono text-xs'>
          {displayedStatement.label}
          {displayedStatement.group ? (
            <Badge variant='outline' className='ml-2 text-[10px]'>
              {displayedStatement.group}
            </Badge>
          ) : null}
          {displayedStatement.unpriced_requests > 0 ? (
            <Badge
              variant='outline'
              className='text-destructive ml-2 gap-1 text-[10px]'
            >
              <AlertTriangle className='size-3' />
              {t('not costable')}
            </Badge>
          ) : null}
        </TableCell>
        <TableCell className='text-right font-mono text-xs tabular-nums'>
          {displayedStatement.requests.toLocaleString()}
        </TableCell>
        <TableCell className='text-right font-mono text-xs whitespace-nowrap tabular-nums'>
          {formatTokens(displayedStatement.prompt_tokens)} /{' '}
          {formatTokens(displayedStatement.completion_tokens)}
        </TableCell>
        <TableCell className='text-right font-mono text-xs font-semibold tabular-nums'>
          {formatUSD(displayedStatement.amount_usd)}
        </TableCell>
        {kind === 'vendor' ? (
          <TableCell className='text-right font-mono text-xs tabular-nums'>
            <span className={varianceClass(verdict)}>
              {row.settlement?.invoice_recorded
                ? formatUSD(row.settlement.invoiced_usd)
                : '—'}
            </span>
          </TableCell>
        ) : null}
        <TableCell className='text-xs'>
          <span className={stateToneClass(state)}>
            {t(SETTLEMENT_STATE_LABELS[state])}
          </span>
          {kind === 'vendor' ? (
            <span className={`block text-[10px] ${varianceClass(verdict)}`}>
              {verdict === 'pending'
                ? t(VARIANCE_VERDICT_LABELS[verdict])
                : `${formatSigned(row.variance_usd ?? 0)} · ${t(VARIANCE_VERDICT_LABELS[verdict])}`}
            </span>
          ) : null}
        </TableCell>
      </TableRow>

      {open ? (
        <TableRow className='bg-muted/30 hover:bg-muted/30'>
          <TableCell />
          <TableCell colSpan={kind === 'vendor' ? 6 : 5} className='py-3'>
            <div className='flex flex-col gap-4'>
              <StatementDetail row={row} />

              {state === 'drifted' ? (
                <Alert variant='destructive'>
                  <AlertTriangle className='size-4' />
                  <AlertDescription className='text-xs'>
                    {t(
                      'Issued at {{issued}}, but re-modelling this period today gives {{live}} — a difference of {{drift}}. Pricing or a purchasing ratio changed after this was settled. The frozen figure is what was acted on; the live one is what the same traffic would cost under current settings.',
                      {
                        issued: formatUSD(row.settlement?.amount_usd ?? 0),
                        live: formatUSD(statement.amount_usd),
                        drift: formatSigned(row.drift_usd ?? 0),
                      }
                    )}
                  </AlertDescription>
                </Alert>
              ) : null}

              {(row.legacy_settlements?.length ?? 0) > 0 ? (
                <Alert
                  variant={row.issuance_blocked ? 'destructive' : 'default'}
                >
                  <AlertTriangle className='size-4' />
                  <AlertDescription className='flex flex-col gap-2 text-xs'>
                    <span>
                      This Pricing Group contains usage covered by an older
                      individual or group invoice. Replace and reissue creates
                      one consolidated invoice number and marks the documents
                      below as superseded in the same transaction.
                    </span>
                    <div className='flex flex-wrap gap-2'>
                      {row.legacy_settlements?.map((legacy) => (
                        <span
                          key={legacy.id}
                          className='bg-background inline-flex items-center gap-1 rounded-md border p-1'
                        >
                          <span className='px-1 font-mono'>
                            #{legacy.id} {legacy.label} ·{' '}
                            {formatUSD(legacy.amount_usd)} · {legacy.status}
                          </span>
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => onPrintInvoice(legacy.id)}
                          >
                            Open frozen invoice
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={busy}
                            onClick={() => onDownloadFrozenCSV(legacy.id)}
                          >
                            Export frozen CSV
                          </Button>
                        </span>
                      ))}
                    </div>
                    <Button size='sm' disabled={busy} onClick={replaceInvoice}>
                      Replace &amp; issue consolidated invoice
                    </Button>
                    <span className='text-muted-foreground'>
                      Malaysian e-Invoices validated in MyInvois can only be
                      cancelled within its permitted window. After that, record
                      the applicable credit, debit, or refund note before
                      replacing the invoice here.
                    </span>
                  </AlertDescription>
                </Alert>
              ) : null}

              {(row.superseded_settlements?.length ?? 0) > 0 ? (
                <Alert>
                  <FileText className='size-4' />
                  <AlertDescription className='flex flex-col gap-2 text-xs'>
                    <span>
                      Superseded invoice history — these frozen documents remain
                      available for audit and must not be sent as the current
                      invoice.
                    </span>
                    <div className='flex flex-wrap gap-2'>
                      {row.superseded_settlements?.map((prior) => (
                        <span
                          key={prior.id}
                          className='bg-background inline-flex items-center gap-1 rounded-md border p-1'
                        >
                          <span className='px-1 font-mono'>
                            #{prior.id} {prior.label} ·{' '}
                            {formatUSD(prior.amount_usd)} · {prior.status}
                          </span>
                          <Button
                            size='sm'
                            variant='outline'
                            onClick={() => onPrintInvoice(prior.id)}
                          >
                            Open frozen invoice
                          </Button>
                          <Button
                            size='sm'
                            variant='outline'
                            disabled={busy}
                            onClick={() => onDownloadFrozenCSV(prior.id)}
                          >
                            Export frozen CSV
                          </Button>
                        </span>
                      ))}
                    </div>
                  </AlertDescription>
                </Alert>
              ) : null}

              {kind === 'customer' ? (
                <div className='bg-background rounded-md border px-3 py-2'>
                  <div className='text-xs font-medium'>
                    {customerInvoice.heading}
                  </div>
                  <p className='text-muted-foreground mt-0.5 text-xs'>
                    {customerInvoice.description}
                  </p>
                </div>
              ) : null}

              <div className='flex flex-wrap items-end gap-2'>
                {kind === 'vendor' ? (
                  <label className='flex flex-col gap-1 text-xs'>
                    <span className='text-muted-foreground'>
                      {t('Their invoice for this period (USD)')}
                    </span>
                    <Input
                      className='h-8 w-40 font-mono text-xs'
                      inputMode='decimal'
                      placeholder={t('not received yet')}
                      value={invoiceDraft}
                      onChange={(e) => setInvoiceDraft(e.target.value)}
                    />
                  </label>
                ) : null}

                <label className='flex flex-1 flex-col gap-1 text-xs'>
                  <span className='text-muted-foreground'>
                    {kind === 'customer'
                      ? 'Internal note (optional)'
                      : t('Note')}
                  </span>
                  <Input
                    className='h-8 text-xs'
                    placeholder={
                      kind === 'customer'
                        ? 'Payment reference or follow-up note — not shown on the invoice'
                        : t(
                            'invoice number, payment reference, anything to find it by'
                          )
                    }
                    value={noteDraft}
                    onChange={(e) => setNoteDraft(e.target.value)}
                  />
                </label>

                <Button
                  size='sm'
                  variant={
                    kind === 'customer' && row.settlement
                      ? 'outline'
                      : 'default'
                  }
                  disabled={
                    busy ||
                    (!row.settlement && (!closed || row.issuance_blocked))
                  }
                  onClick={recordInvoice}
                >
                  <FileText className='size-4' />
                  {kind === 'customer'
                    ? customerInvoice.saveLabel
                    : t(
                        row.settlement
                          ? PRIMARY_ACTION_LABELS.update
                          : PRIMARY_ACTION_LABELS[kind]
                      )}
                </Button>

                {kind === 'customer' ? (
                  <Button
                    size='sm'
                    disabled={
                      busy || !row.settlement || !customerInvoice.canOpen
                    }
                    onClick={() =>
                      row.settlement && onPrintInvoice(row.settlement.id)
                    }
                    title={
                      row.settlement
                        ? 'Open a print-ready invoice'
                        : 'Issue the invoice first'
                    }
                  >
                    <FileText className='size-4' />
                    Open / save invoice PDF
                  </Button>
                ) : null}

                {kind === 'customer' && row.settlement ? (
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={busy}
                    onClick={replaceInvoice}
                  >
                    Replace &amp; reissue invoice
                  </Button>
                ) : null}

                {row.settlement && row.settlement.status !== 'settled' ? (
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={busy}
                    onClick={() =>
                      onUpdate({
                        invoiced_usd: invoiceRecorded ? parsedInvoice : 0,
                        invoice_recorded: invoiceRecorded,
                        status: 'settled',
                        note: noteDraft,
                      })
                    }
                  >
                    {kind === 'customer' ? t('Mark paid') : t('Mark settled')}
                  </Button>
                ) : null}

                <Button
                  size='sm'
                  variant='outline'
                  disabled={busy}
                  onClick={onDownload}
                >
                  <Download className='size-4' />
                  {kind === 'customer'
                    ? 'Export line items CSV'
                    : t('Export CSV')}
                </Button>
              </div>

              {row.settlement ? (
                <p className='text-muted-foreground text-[10px]'>
                  {t(
                    'Frozen at {{amount}} against the {{snapshot}} price catalog. Typing an invoice does not re-model the period — that would replace the figure the invoice is being compared against.',
                    {
                      amount: formatUSD(row.settlement.amount_usd),
                      snapshot: row.settlement.pricing_snapshot_date || '—',
                    }
                  )}
                </p>
              ) : null}
            </div>
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}

/** StatementDetail is the bill itself: the per-model lines a counterparty can
 *  check, followed by how the total was arrived at. */
function StatementDetail({ row }: { row: SettlementRow }) {
  const { t } = useTranslation()
  const statement = row.issued_statement ?? row.statement
  const vendor = statement.kind === 'vendor'

  return (
    <div className='flex flex-col gap-3'>
      {!statementIsBalanced(statement) ? (
        <Alert variant='destructive'>
          <AlertTriangle className='size-4' />
          <AlertDescription className='text-xs'>
            {t('Error')}: {t('Line items')} ≠ {t('Total')}
          </AlertDescription>
        </Alert>
      ) : null}
      <div>
        <div className='text-muted-foreground mb-1 text-[10px] font-semibold tracking-wider uppercase'>
          {t('Line items')}
        </div>
        <div className='overflow-x-auto rounded border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className='text-xs'>{t('Model')}</TableHead>
                {vendor ? (
                  <TableHead className='text-xs'>{t('Channel')}</TableHead>
                ) : null}
                {vendor ? (
                  <TableHead className='text-xs'>{t('Base URL')}</TableHead>
                ) : null}
                {vendor ? (
                  <TableHead className='text-right text-xs'>
                    {t('Multiplier')}
                  </TableHead>
                ) : null}
                <TableHead className='text-right text-xs'>
                  {t('Requests')}
                </TableHead>
                <TableHead className='text-right text-xs'>
                  {t('Input Tokens')}
                </TableHead>
                <TableHead className='text-right text-xs'>
                  {t('Cached')}
                </TableHead>
                <TableHead className='text-right text-xs'>
                  {t('Output Tokens')}
                </TableHead>
                <TableHead className='text-right text-xs'>
                  {t('Amount')}
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {statement.lines.map((line) => (
                <TableRow key={line.model}>
                  <TableCell className='font-mono text-xs'>
                    {line.model}
                    {line.unpriced ? (
                      <Badge
                        variant='outline'
                        className='text-destructive ml-2 text-[10px]'
                      >
                        {t('no catalog price')}
                      </Badge>
                    ) : null}
                  </TableCell>
                  {vendor ? (
                    <TableCell className='font-mono text-xs'>
                      {line.channel_name || `#${line.channel_id ?? 0}`}
                    </TableCell>
                  ) : null}
                  {vendor ? (
                    <TableCell
                      className='max-w-56 truncate font-mono text-[10px]'
                      title={line.channel_base_url}
                    >
                      {line.channel_base_url || '—'}
                    </TableCell>
                  ) : null}
                  {vendor ? (
                    <TableCell className='text-right font-mono text-xs tabular-nums'>
                      {(line.cost_ratio ?? 1).toFixed(4)}×
                    </TableCell>
                  ) : null}
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {line.requests.toLocaleString()}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {formatTokens(line.prompt_tokens)}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {formatTokens(line.cached_tokens)}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {formatTokens(line.completion_tokens)}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {formatUSD(line.amount_usd)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </div>

      <div className='flex flex-col gap-1'>
        <div className='text-muted-foreground mb-1 text-[10px] font-semibold tracking-wider uppercase'>
          {t('How this amount is derived')}
        </div>
        {deriveStatement(statement).map((step) => (
          <div
            key={step.labelKey}
            className={`flex items-baseline justify-between gap-4 text-xs ${
              step.emphasis ? 'border-t pt-1 font-semibold' : ''
            }`}
          >
            <div className='flex flex-col'>
              <span>{t(step.labelKey)}</span>
              {step.noteKey ? (
                <span className='text-muted-foreground text-[10px]'>
                  {t(step.noteKey, step.noteParams)}
                </span>
              ) : null}
            </div>
            {step.amountUSD !== undefined ? (
              <span className='font-mono tabular-nums'>
                {formatUSD(step.amountUSD)}
              </span>
            ) : null}
          </div>
        ))}
      </div>
    </div>
  )
}
