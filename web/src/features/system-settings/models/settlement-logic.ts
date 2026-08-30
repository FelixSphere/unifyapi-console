/*
UNIFYAPI-FORK: pure logic for the settlement screen.

Split from the component because these decide what goes on an invoice. A
mis-rendered dashboard is embarrassing; a mis-rendered invoice is a dispute, and
the difference is worth testing directly rather than through the DOM.

Two things here are deliberately NOT like the profit screen:

  * periods are CALENDAR MONTHS, not rolling windows. Nobody invoices "the last
    30 days" — they invoice August, and the boundary has to be the same boundary
    the counterparty is using.
  * money is formatted by the profit screen's own formatter, imported rather
    than reimplemented. A bill and the dashboard it was reconciled against must
    round identically, and two copies of a rounding rule eventually stop
    agreeing.
*/
import type {
  CustomerPayment,
  SettlementRow,
  Statement,
  StatementKind,
} from '../types'
import { formatUSD, toISODate } from './profit-logic'

export { formatTokens, formatUSD } from './profit-logic'

/** A billing period: one calendar month, inclusive of both ends. */
export type BillingPeriod = {
  start: string
  end: string
  /** YYYY-MM, for labelling. */
  label: string
}

/**
 * monthPeriod resolves a calendar month relative to `today`.
 *
 * `monthsBack: 1` is last month, which is the one you actually invoice — the
 * current month is still accruing and a bill issued from it is wrong by
 * however much of the month is left.
 *
 * Day 0 of the next month is the last day of this one, which is how February
 * and the 30/31 split come out right without a table of month lengths.
 */
export function monthPeriod(today: Date, monthsBack: number): BillingPeriod {
  const year = today.getFullYear()
  const month = today.getMonth() - monthsBack
  const first = new Date(year, month, 1)
  const last = new Date(year, month + 1, 0)
  return {
    start: toISODate(first),
    end: toISODate(last),
    label: `${first.getFullYear()}-${String(first.getMonth() + 1).padStart(2, '0')}`,
  }
}

/** MONTH_OFFSETS are the choices offered, newest-billable first. */
export const MONTH_OFFSETS = [1, 0, 2, 3] as const

/** recentPeriods lists the billing periods a user can pick. */
export function recentPeriods(today: Date): BillingPeriod[] {
  return MONTH_OFFSETS.map((offset) => monthPeriod(today, offset))
}

/** isPeriodClosed reports whether a period has finished accruing.
 *
 *  Issuing from an open period produces a bill that is wrong by the rest of the
 *  month, so the screen says so rather than quietly letting it happen.
 */
export function isPeriodClosed(period: BillingPeriod, today: Date): boolean {
  return period.end < toISODate(today)
}

/**
 * SettlementState is what the operator needs to know at a glance about one row.
 *
 * `drifted` is the one that does not exist on any other screen: the period was
 * issued, and re-modelling it today gives a different number. That happens when
 * a purchasing ratio or a catalog price changed after the fact. It is not an
 * error — but a settlement you have already paid from, whose basis has since
 * moved, is exactly the thing you want flagged before you reconcile again.
 */
export type SettlementState =
  | 'not-issued'
  | 'issued'
  | 'settled'
  | 'drifted'
  | 'void'

/**
 * SETTLEMENT_STATE_LABELS and VARIANCE_VERDICT_LABELS are translation keys
 * chosen at RUNTIME, so no static scan of `t('…')` calls can find them and an
 * untranslated one would render as English with no warning at all.
 *
 * Declared here as exhaustive Records for two reasons: adding a state without a
 * label becomes a type error, and the i18n test can iterate the values instead
 * of trying to parse them out of a component.
 */
export const SETTLEMENT_STATE_LABELS: Record<SettlementState, string> = {
  'not-issued': 'not issued',
  issued: 'issued',
  settled: 'settled',
  drifted: 'basis changed',
  void: 'void',
}

/** driftToleranceUSD is below the smallest amount worth a second look. Floating
 *  point sums of thousands of rows do not land on the same bit twice. */
export const DRIFT_TOLERANCE_USD = 0.005

export function settlementState(row: SettlementRow): SettlementState {
  if (!row.settlement) return 'not-issued'
  if (row.settlement.status === 'void') return 'void'
  if (Math.abs(row.drift_usd ?? 0) > DRIFT_TOLERANCE_USD) return 'drifted'
  if (row.settlement.status === 'settled') return 'settled'
  return 'issued'
}

/**
 * VarianceVerdict classifies a vendor invoice against what we modelled.
 *
 * The tolerance mirrors service/reconcile.go's: some drift is expected and
 * benign — vendors round in their own units, tokenizer counts differ slightly,
 * and a request that failed after we counted tokens is billed differently by
 * each side. Beyond it, the sign says which way to look.
 */
export type VarianceVerdict = 'pending' | 'reconciled' | 'over' | 'under'

/** VARIANCE_TOLERANCE_PCT must equal varianceTolerancePct in
 *  service/reconcile.go, or the screen and the nightly alert disagree about
 *  what counts as reconciled. */
export const VARIANCE_TOLERANCE_PCT = 2.0

export function varianceVerdict(row: SettlementRow): VarianceVerdict {
  const settlement = row.settlement
  if (!settlement?.invoice_recorded) return 'pending'
  const modelled = settlement.amount_usd
  if (modelled === 0) return settlement.invoiced_usd === 0 ? 'reconciled' : 'over'
  const pct = ((settlement.invoiced_usd - modelled) / modelled) * 100
  if (Math.abs(pct) <= VARIANCE_TOLERANCE_PCT) return 'reconciled'
  return pct > 0 ? 'over' : 'under'
}

/** See SETTLEMENT_STATE_LABELS. Spelled as sentences rather than "over" and
 *  "under", which read as arithmetic rather than as who owes whom. */
export const VARIANCE_VERDICT_LABELS: Record<VarianceVerdict, string> = {
  pending: 'invoice not received',
  reconciled: 'reconciled',
  over: 'they billed more than we modelled',
  under: 'they billed less than we modelled',
}

/**
 * PRIMARY_ACTION_LABELS names the main button on an expanded row.
 *
 * A record rather than a helper returning string literals: a helper's returns
 * are invisible both to a scan of `t('…')` calls and to the `_LABELS` scan, and
 * three of these shipped in English exactly that way.
 */
export const PRIMARY_ACTION_LABELS: Record<'update' | StatementKind, string> = {
  update: 'Update record',
  customer: 'Issue statement',
  vendor: 'Record settlement',
}

/** paymentsTotalUSD sums one customer's receipts for the period. */
export function paymentsTotalUSD(payments: CustomerPayment[] | undefined): number {
  if (!payments?.length) return 0
  return payments.reduce((sum, payment) => sum + payment.credited_usd, 0)
}

/**
 * DerivationStep mirrors the profit screen's, so the expanded row on an invoice
 * reads the same way as the expanded row on the dashboard.
 */
export type StatementStep = {
  labelKey: string
  amountUSD?: number
  noteKey?: string
  noteParams?: Record<string, string | number>
  emphasis?: boolean
}

/**
 * deriveStatement explains one statement's amount.
 *
 * The customer and vendor cases say materially different things, because the
 * numbers mean materially different things: one is a reading, the other is an
 * estimate. Collapsing them into a single wording is how a reader ends up
 * treating a modelled cost as a settled fact.
 */
export function deriveStatement(
  statement: Statement,
  payments?: CustomerPayment[]
): StatementStep[] {
  if (statement.kind === 'customer') {
    const paid = paymentsTotalUSD(payments)
    const steps: StatementStep[] = [
      {
        labelKey: 'Usage this period (from the ledger)',
        amountUSD: statement.amount_usd,
        noteKey:
          'Sum of quota actually deducted — already includes model discount, group ratio and cache pricing.',
      },
    ]
    if (payments?.length) {
      steps.push({
        labelKey: 'Received this period',
        amountUSD: paid,
        noteKey: 'Top-ups credited between {{start}} and {{end}}, across {{count}} orders.',
        noteParams: {
          start: statement.period_start,
          end: statement.period_end,
          count: payments.reduce((sum, payment) => sum + payment.orders, 0),
        },
      })
      steps.push({
        labelKey: 'Received minus used',
        amountUSD: paid - statement.amount_usd,
        emphasis: true,
        noteKey:
          'Not an account balance: it compares this period only, and says nothing about credit carried in or out.',
      })
    }
    return steps
  }

  const steps: StatementStep[] = [
    {
      labelKey: 'Modelled upstream cost',
      amountUSD: statement.amount_usd,
      noteKey: 'Tokens x vendor official price x this channel purchasing ratio.',
      emphasis: true,
    },
  ]
  if (statement.unpriced_requests > 0) {
    steps.push({
      labelKey: 'Not costable',
      noteKey:
        '{{count}} requests used models with no catalog price ({{models}}), so this amount is understated — do not pay from it as-is.',
      noteParams: {
        count: statement.unpriced_requests,
        models: (statement.unpriced_models ?? []).join(', '),
      },
    })
  }
  return steps
}

/**
 * csvHref builds the export link. Kept here rather than inline so the two
 * buttons (one statement, or the whole period) cannot drift apart.
 */
export function csvHref(
  kind: StatementKind,
  period: BillingPeriod,
  counterparty?: string
): string {
  const params = new URLSearchParams({
    kind,
    start: period.start,
    end: period.end,
  })
  if (counterparty) params.set('counterparty', counterparty)
  return `/api/pricing/settlement.csv?${params.toString()}`
}

/** formatSigned renders a variance with an explicit sign, so "they billed us
 *  more" reads without comparing two columns. */
export function formatSigned(value: number): string {
  if (value > 0) return `+${formatUSD(value)}`
  return formatUSD(value)
}
