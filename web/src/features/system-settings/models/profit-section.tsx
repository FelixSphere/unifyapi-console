/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: the profit view.

Reconciliation existed only as JSON and CSV endpoints, which meant the answer to
"are we making money right now, and where from" required a terminal. This is
that answer as a screen.

Two things it deliberately does that a plain revenue table would not:

  * it shows COST beside revenue, and marks each side for how much it can be
    trusted -- revenue is read from the consume-log ledger untouched, cost is
    modelled from the vendors' published prices. Presenting both as if they were
    equally certain is how a reader ends up acting on the wrong one.
  * every row expands into the derivation. A margin nobody can decompose is a
    number nobody can act on; the point of the screen is the chain from the
    vendor's price to what is left, not the total at the top.
*/
import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Download,
  TrendingDown,
  TrendingUp,
} from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'

import { downloadReconciliationCSV, getReconciliation } from '../api'
import { SettingsSection } from '../components/settings-section'
import type { ReconcileGroupBy, ReconcileLine } from '../types'
import {
  PERIOD_PRESETS,
  cacheHitRate,
  deriveMargin,
  formatPct,
  formatTokens,
  formatUSD,
  marginHealth,
  resolvePeriod,
} from './profit-logic'

const DIMENSIONS: { id: ReconcileGroupBy; labelKey: string }[] = [
  { id: 'model', labelKey: 'By model' },
  { id: 'vendor', labelKey: 'By vendor' },
  { id: 'customer', labelKey: 'By customer' },
  { id: 'channel', labelKey: 'By channel' },
  { id: 'day', labelKey: 'By day' },
]

export function ProfitSection() {
  const { t } = useTranslation()
  const [presetId, setPresetId] = useState('7d')
  const [groupBy, setGroupBy] = useState<ReconcileGroupBy>('model')
  const [expanded, setExpanded] = useState<string | null>(null)
  const [downloading, setDownloading] = useState(false)

  const preset =
    PERIOD_PRESETS.find((p) => p.id === presetId) ?? PERIOD_PRESETS[1]
  // Resolved once per render against the real clock. The window is part of the
  // query key, so crossing midnight refetches rather than serving yesterday.
  const period = useMemo(() => resolvePeriod(preset, new Date()), [preset])

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['reconcile', period.start, period.end, groupBy],
    queryFn: () => getReconciliation({ ...period, group_by: groupBy }),
  })

  const report = data?.data
  const costRatios = data?.cost_basis?.channel_cost_ratios ?? {}
  const hasCostBasis = Object.keys(costRatios).length > 0

  const csvHref = `/api/pricing/reconcile.csv?start=${period.start}&end=${period.end}&group_by=${groupBy}`
  const downloadCSV = async () => {
    setDownloading(true)
    try {
      await downloadReconciliationCSV(csvHref)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : t('Failed'))
    } finally {
      setDownloading(false)
    }
  }

  return (
    <SettingsSection title={t('Profit')}>
      <div className='flex flex-col gap-4'>
        {/*
          Stated before any number, because it changes what every number below
          means. Without a purchasing cost the modelled cost is list price, so
          margin is unmeasured rather than zero.
        */}
        {!hasCostBasis ? (
          <Alert variant='destructive'>
            <AlertTriangle className='size-4' />
            <AlertDescription className='text-xs'>
              {t(
                'No channel has a purchasing cost configured, so every line is costed at the vendor list price and its margin is unmeasured rather than real. Enter your negotiated rates under 上游采购成本 to make these numbers mean something.'
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        <div className='flex flex-wrap items-center gap-2'>
          <Tabs value={presetId} onValueChange={setPresetId}>
            <TabsList>
              {PERIOD_PRESETS.map((p) => (
                <TabsTrigger key={p.id} value={p.id}>
                  {t(p.labelKey)}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <span className='text-muted-foreground font-mono text-xs'>
            {period.start} → {period.end}
          </span>
          {/* This codebase's Button takes `render`, not `asChild`. */}
          <Button
            variant='outline'
            size='sm'
            className='ml-auto'
            disabled={downloading}
            onClick={downloadCSV}
          >
            <Download className='size-4' />
            {t('Export CSV')}
          </Button>
        </div>

        {report ? (
          <ProfitHeadline total={report.total} hasCostBasis={hasCostBasis} />
        ) : null}

        {data?.warning ? (
          <Alert variant='destructive'>
            <AlertDescription className='text-xs'>
              {data.warning}
            </AlertDescription>
          </Alert>
        ) : null}

        <Tabs
          value={groupBy}
          onValueChange={(v) => setGroupBy(v as ReconcileGroupBy)}
        >
          <TabsList>
            {DIMENSIONS.map((d) => (
              <TabsTrigger key={d.id} value={d.id}>
                {t(d.labelKey)}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>

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
                  <TableHead>{t('Name')}</TableHead>
                  <TableHead className='text-right'>{t('Requests')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Tokens in/out')}
                  </TableHead>
                  <TableHead className='text-right'>{t('Billed')}</TableHead>
                  <TableHead className='text-right'>
                    {t('Upstream cost')}
                  </TableHead>
                  <TableHead className='text-right'>{t('Margin')}</TableHead>
                  <TableHead className='text-right'>{t('Margin %')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(report?.lines ?? []).map((line) => (
                  <ProfitRow
                    key={line.key}
                    line={line}
                    hasCostBasis={hasCostBasis}
                    open={expanded === line.key}
                    onToggle={() =>
                      setExpanded(expanded === line.key ? null : line.key)
                    }
                  />
                ))}
                {report && report.lines.length === 0 ? (
                  <TableRow>
                    <TableCell
                      colSpan={8}
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

        <p className='text-muted-foreground text-xs'>
          {t(
            'Billed is read from the consume-log ledger and is exactly what customers were charged. Upstream cost is modelled from token counts x the vendor official price x the channel purchasing ratio, because vendors issue no per-request receipt — diff it against their invoice to validate it.'
          )}
        </p>
      </div>
    </SettingsSection>
  )
}

/**
 * marginToneClass encodes health as colour AND weight, so a loss is legible
 * without comparing two columns -- and to a reader who cannot distinguish the
 * hues, since the weight change carries independently.
 */
function marginToneClass(health: ReturnType<typeof marginHealth>): string {
  switch (health) {
    case 'loss':
      return 'text-destructive font-semibold'
    case 'thin':
      return 'text-amber-600 dark:text-amber-400'
    case 'unmeasured':
      return 'text-muted-foreground'
    default:
      return 'text-emerald-600 dark:text-emerald-400'
  }
}

function statToneClass(
  tone: 'default' | 'positive' | 'negative' | 'muted'
): string {
  switch (tone) {
    case 'positive':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'negative':
      return 'text-destructive'
    case 'muted':
      return 'text-muted-foreground'
    default:
      return ''
  }
}

/** headlineTone: an unmeasured margin is neither good nor bad, so it stays neutral. */
function headlineTone(
  health: ReturnType<typeof marginHealth>,
  positive: boolean
): 'positive' | 'negative' | 'muted' {
  if (health === 'unmeasured') return 'muted'
  return positive ? 'positive' : 'negative'
}

/** headlineIcon: no arrow while the margin is unmeasured -- a direction we
 *  cannot vouch for should not be drawn as one. */
function headlineIcon(
  health: ReturnType<typeof marginHealth>,
  positive: boolean
): React.ReactNode {
  if (health === 'unmeasured') return null
  return positive ? (
    <TrendingUp className='size-4' />
  ) : (
    <TrendingDown className='size-4' />
  )
}

function ProfitHeadline({
  total,
  hasCostBasis,
}: {
  total: ReconcileLine
  hasCostBasis: boolean
}) {
  const { t } = useTranslation()
  const health = marginHealth(total, hasCostBasis)
  const positive = total.margin_usd >= 0

  return (
    <div className='grid gap-3 sm:grid-cols-3'>
      <Stat
        label={t('Billed to customers')}
        value={formatUSD(total.revenue_usd)}
      />
      <Stat
        label={t('Upstream cost')}
        value={formatUSD(total.cost_usd)}
        muted
      />
      <Stat
        label={t('Margin')}
        value={formatUSD(total.margin_usd)}
        sub={health === 'unmeasured' ? t('unmeasured') : formatPct(total)}
        tone={headlineTone(health, positive)}
        icon={headlineIcon(health, positive)}
      />
    </div>
  )
}

function Stat({
  label,
  value,
  sub,
  tone = 'default',
  muted,
  icon,
}: {
  label: string
  value: string
  sub?: string
  tone?: 'default' | 'positive' | 'negative' | 'muted'
  muted?: boolean
  icon?: React.ReactNode
}) {
  const toneClass = statToneClass(muted ? 'muted' : tone)
  return (
    <div className='rounded-md border p-3'>
      <div className='text-muted-foreground text-xs'>{label}</div>
      <div
        className={`flex items-center gap-2 font-mono text-xl font-semibold tabular-nums ${toneClass}`}
      >
        {icon}
        {value}
      </div>
      {sub ? <div className={`text-xs ${toneClass}`}>{sub}</div> : null}
    </div>
  )
}

function ProfitRow({
  line,
  hasCostBasis,
  open,
  onToggle,
}: {
  line: ReconcileLine
  hasCostBasis: boolean
  open: boolean
  onToggle: () => void
}) {
  const { t } = useTranslation()
  const health = marginHealth(line, hasCostBasis)
  const hit = cacheHitRate(line)

  // State encoded in form as well as in the number, so a loss reads at a glance
  // rather than only after comparing two columns.
  const marginClass = marginToneClass(health)

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
          {line.label}
          {health === 'loss' ? (
            <Badge
              variant='outline'
              className='text-destructive ml-2 text-[10px]'
            >
              {t('losing money')}
            </Badge>
          ) : null}
          {line.unpriced_requests > 0 ? (
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
          {line.requests.toLocaleString()}
        </TableCell>
        <TableCell className='text-right font-mono text-xs whitespace-nowrap tabular-nums'>
          {formatTokens(line.prompt_tokens)} /{' '}
          {formatTokens(line.completion_tokens)}
          {hit !== null && hit > 0 ? (
            <span className='text-muted-foreground block text-[10px]'>
              {t('cache')} {(hit * 100).toFixed(0)}%
            </span>
          ) : null}
        </TableCell>
        <TableCell className='text-right font-mono text-xs tabular-nums'>
          {formatUSD(line.revenue_usd)}
        </TableCell>
        <TableCell className='text-muted-foreground text-right font-mono text-xs tabular-nums'>
          {formatUSD(line.cost_usd)}
        </TableCell>
        <TableCell
          className={`text-right font-mono text-xs tabular-nums ${marginClass}`}
        >
          {formatUSD(line.margin_usd)}
        </TableCell>
        <TableCell
          className={`text-right font-mono text-xs tabular-nums ${marginClass}`}
        >
          {health === 'unmeasured' ? '—' : formatPct(line)}
        </TableCell>
      </TableRow>

      {open ? (
        <TableRow className='bg-muted/30 hover:bg-muted/30'>
          <TableCell />
          <TableCell colSpan={7} className='py-3'>
            <div className='flex flex-col gap-1'>
              <div className='text-muted-foreground mb-1 text-[10px] font-semibold tracking-wider uppercase'>
                {t('How this margin is derived')}
              </div>
              {deriveMargin(line).map((step) => (
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
                    <span
                      className={`font-mono tabular-nums ${
                        step.emphasis ? marginClass : ''
                      }`}
                    >
                      {formatUSD(step.amountUSD)}
                    </span>
                  ) : null}
                </div>
              ))}
            </div>
          </TableCell>
        </TableRow>
      ) : null}
    </>
  )
}
