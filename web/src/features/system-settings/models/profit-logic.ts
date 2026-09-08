/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: pure logic for the profit view.

Split from the component for the usual reason -- this decides the numbers an
operator reads as their margin, so it is tested directly rather than through the
DOM. But there is a second reason specific to this screen: the value of a profit
report is not the total, it is the DERIVATION. "How did we get here" is what
lets someone act on it, and a derivation assembled inline in JSX is one nobody
can check.
*/
import type { ReconcileLine } from '../types'

/** A named reporting window, resolved against a caller-supplied "today". */
export type PeriodPreset = {
  id: string
  labelKey: string
  /** Days back from today for the start; 0 means today. */
  daysBack: number
  /** True when the window ends today, i.e. includes a partial day. */
  includesToday: boolean
}

export const PERIOD_PRESETS: PeriodPreset[] = [
  { id: 'today', labelKey: 'Today', daysBack: 0, includesToday: true },
  { id: '7d', labelKey: 'Last 7 days', daysBack: 6, includesToday: true },
  { id: '30d', labelKey: 'Last 30 days', daysBack: 29, includesToday: true },
  { id: 'mtd', labelKey: 'Month to date', daysBack: -1, includesToday: true },
]

/** toISODate formats a Date as YYYY-MM-DD in LOCAL time.
 *
 * Not toISOString().slice(0,10): that converts to UTC first, so anywhere east
 * of Greenwich the evening rolls the date forward and "today" silently becomes
 * tomorrow — a window with no data in it.
 */
export function toISODate(date: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`
}

/** resolvePeriod turns a preset into the inclusive start/end the API expects. */
export function resolvePeriod(
  preset: PeriodPreset,
  today: Date
): { start: string; end: string } {
  const end = toISODate(today)
  if (preset.id === 'mtd') {
    const first = new Date(today.getFullYear(), today.getMonth(), 1)
    return { start: toISODate(first), end }
  }
  const start = new Date(today)
  start.setDate(start.getDate() - preset.daysBack)
  return { start: toISODate(start), end }
}

/**
 * MarginHealth classifies a line so the table can encode state in form, not
 * only in a number. The thresholds mirror the server's alert rules
 * (service/reconcile_alerts.go) so the screen and the nightly alert never
 * disagree about what counts as thin.
 */
export type MarginHealth = 'loss' | 'thin' | 'healthy' | 'unmeasured'

export const THIN_MARGIN_PCT = 10

export function marginHealth(
  line: Pick<ReconcileLine, 'revenue_usd' | 'margin_usd' | 'margin_pct'>,
  hasCostBasis: boolean
): MarginHealth {
  // With no purchasing cost configured anywhere, every line is costed at the
  // vendor's list price, so its margin is UNMEASURED rather than zero. Painting
  // the whole table red in that state would be precise and useless.
  if (!hasCostBasis) return 'unmeasured'
  if (line.margin_usd < 0) return 'loss'
  if (line.revenue_usd > 0 && line.margin_pct < THIN_MARGIN_PCT) return 'thin'
  return 'healthy'
}

/**
 * DerivationStep is one link in the chain from the vendor's price to our
 * margin. The profit table renders these on expand, which is the whole point of
 * the screen: a margin nobody can decompose is a number nobody can act on.
 */
export type DerivationStep = {
  labelKey: string
  /** Rendered as money when set, otherwise `noteKey` carries the explanation. */
  amountUSD?: number
  /** Translation key, not prose: these render inside a Chinese console. */
  noteKey?: string
  /** Interpolation values for `noteKey`, when it takes any. */
  noteParams?: Record<string, string | number>
  emphasis?: boolean
}

/**
 * deriveMargin explains one line's margin in the order the money actually
 * moves: what the customer was billed, what the upstream charged, what is left.
 *
 * Revenue is stated as read-from-ledger rather than recomputed, because that is
 * the property that makes it trustworthy — it is the quota actually deducted,
 * already carrying the model discount, the group ratio, cache pricing and any
 * clamp. Cost is stated as modelled, because vendors issue no per-request
 * receipt. Presenting them as if they were the same kind of number is how a
 * reader ends up trusting the wrong one.
 */
export function deriveMargin(line: ReconcileLine): DerivationStep[] {
  const steps: DerivationStep[] = [
    {
      labelKey: 'Billed to customers (from the ledger)',
      amountUSD: line.revenue_usd,
      noteKey:
        'Sum of quota actually deducted — already includes model discount, group ratio and cache pricing.',
    },
    {
      labelKey: 'Upstream cost (modelled)',
      amountUSD: -line.cost_usd,
      noteKey:
        'Tokens x vendor official price x this channel purchasing ratio.',
    },
    {
      labelKey: 'Margin',
      amountUSD: line.margin_usd,
      emphasis: true,
    },
  ]

  if (line.unpriced_requests > 0) {
    steps.push({
      labelKey: 'Not costable',
      noteKey:
        '{{count}} requests used models with no catalog price ({{models}}), so this margin is overstated.',
      noteParams: {
        count: line.unpriced_requests,
        models: (line.unpriced_models ?? []).join(', '),
      },
    })
  }
  return steps
}

/** formatUSD renders money at a precision that keeps small lines distinct. */
export function formatUSD(value: number): string {
  const sign = value < 0 ? '-' : ''
  const n = Math.abs(value)
  if (n === 0) return '$0.00'
  if (n < 0.01) return `${sign}$${n.toFixed(4)}`
  if (n < 1) return `${sign}$${n.toFixed(3)}`
  return `${sign}$${n.toFixed(2)}`
}

/** formatPct renders a margin percentage, blank when it is undefined. */
export function formatPct(line: ReconcileLine): string {
  if (line.revenue_usd === 0) return '—'
  return `${line.margin_pct.toFixed(1)}%`
}

/** formatTokens abbreviates large token counts so columns stay scannable. */
export function formatTokens(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return String(n)
}

/**
 * cacheHitRate is the share of prompt tokens served from cache.
 *
 * Worth surfacing next to margin: cached reads cost a tenth of fresh input at
 * the vendors that offer them, so a line whose margin looks healthy on heavy
 * caching is one whose margin collapses if the caching stops.
 */
export function cacheHitRate(line: ReconcileLine): number | null {
  if (line.prompt_tokens <= 0) return null
  return line.cached_tokens / line.prompt_tokens
}
