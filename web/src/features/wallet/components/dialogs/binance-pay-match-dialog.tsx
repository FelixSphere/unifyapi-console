/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { AlertTriangle, CheckCircle2, Loader2, RefreshCw } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { cn } from '@/lib/utils'

import {
  getBinancePayCandidates,
  isApiSuccess,
  matchBinancePayTransaction,
} from '../../api'
import { formatTimestamp } from '../../lib/billing'
import type {
  BinancePayCandidate,
  BinancePayCandidatesResponse,
  BinancePayEvidence,
} from '../../types'

// ============================================================================
// Operator proof-of-receipt for Binance Pay orders
// ============================================================================

function formatTxTime(ms: number): string {
  return ms > 0 ? formatTimestamp(Math.floor(ms / 1000)) : '-'
}

function sourceLabel(source: string, t: (k: string) => string): string {
  if (source.endsWith('deposit')) return t('On-chain deposit')
  return t('Binance Pay transfer')
}

/**
 * Shown under a settled Binance Pay order: which Binance transaction paid
 * it. This is the receipt an administrator can check against the account.
 */
export function BinancePayEvidenceBlock({
  evidence,
}: {
  evidence: BinancePayEvidence
}) {
  const { t } = useTranslation()
  return (
    <div className='mt-3 rounded-md border border-green-600/30 bg-green-600/5 p-3 text-xs'>
      <div className='flex items-center gap-2 font-medium text-green-700 dark:text-green-400'>
        <CheckCircle2 className='h-3.5 w-3.5' />
        {evidence.manual
          ? t('Matched by an administrator to a Binance transaction')
          : t('Matched automatically to a Binance transaction')}
      </div>
      <div className='mt-2 grid grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4'>
        <div>
          <div className='text-muted-foreground'>{t('Transaction')}</div>
          <div className='truncate font-mono' title={evidence.transaction_id}>
            {evidence.transaction_id}
          </div>
        </div>
        <div>
          <div className='text-muted-foreground'>{t('Received')}</div>
          <div className='font-mono'>
            {evidence.amount} {evidence.currency}
          </div>
        </div>
        <div>
          <div className='text-muted-foreground'>{t('Payer')}</div>
          <div className='font-mono'>{evidence.payer_id || '-'}</div>
        </div>
        <div>
          <div className='text-muted-foreground'>{t('Paid at')}</div>
          <div>{formatTxTime(evidence.transact_time)}</div>
        </div>
      </div>
    </div>
  )
}

interface BinancePayMatchDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  tradeNo: string | null
  /** Called after a successful match so the list can refresh. */
  onMatched: () => void
}

/**
 * Replaces blind "Complete Order" for Binance Pay: lists the receiving
 * account's real incoming transfers around the order, closest amount first,
 * and lets the administrator pin one of them to the order.
 */
export function BinancePayMatchDialog({
  open,
  onOpenChange,
  tradeNo,
  onMatched,
}: BinancePayMatchDialogProps) {
  const { t } = useTranslation()
  const [data, setData] = useState<BinancePayCandidatesResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [matching, setMatching] = useState(false)

  const load = useCallback(async () => {
    if (!tradeNo) return
    setLoading(true)
    setError(null)
    try {
      const response = await getBinancePayCandidates(tradeNo)
      if (isApiSuccess(response) && response.data) {
        setData(response.data)
        const exact = response.data.candidates.find(
          (c) => c.exact_match && !c.used_by_trade_no
        )
        setSelected(exact ? exact.transaction_id : null)
      } else {
        setData(null)
        setError(response.message || t('Failed to read Binance history'))
      }
    } catch {
      setData(null)
      setError(t('Failed to read Binance history'))
    } finally {
      setLoading(false)
    }
  }, [tradeNo, t])

  useEffect(() => {
    if (open && tradeNo) {
      setData(null)
      setSelected(null)
      void load()
    }
  }, [open, tradeNo, load])

  const chosen: BinancePayCandidate | undefined = data?.candidates.find(
    (c) => c.transaction_id === selected
  )

  const handleMatch = async () => {
    if (!tradeNo || !chosen) return
    setMatching(true)
    try {
      const response = await matchBinancePayTransaction({
        trade_no: tradeNo,
        transaction_id: chosen.transaction_id,
      })
      if (isApiSuccess(response)) {
        toast.success(t('Order matched and credited'))
        onMatched()
        onOpenChange(false)
      } else {
        toast.error(response.message || t('Failed to match transaction'))
      }
    } catch {
      toast.error(t('Failed to match transaction'))
    } finally {
      setMatching(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Match Binance transaction')}
      description={t(
        'These are the incoming transfers actually recorded in your Binance account around this order. Pick the one that paid it; the order is credited only from that transaction, and the transaction can never be used for another order.'
      )}
      contentClassName='sm:max-w-3xl'
      contentHeight='auto'
      bodyClassName='space-y-3'
      footer={
        <div className='flex w-full items-center justify-between gap-2'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={() => void load()}
            disabled={loading}
            className='gap-2'
          >
            {loading ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : (
              <RefreshCw className='h-4 w-4' />
            )}
            {t('Reload from Binance')}
          </Button>
          <div className='flex gap-2'>
            <Button
              type='button'
              variant='outline'
              onClick={() => onOpenChange(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              onClick={() => void handleMatch()}
              disabled={!chosen || !!chosen.used_by_trade_no || matching}
              variant={chosen?.warn ? 'destructive' : 'default'}
            >
              {matching && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
              {chosen?.warn
                ? t('Accept short payment and credit')
                : t('Credit order from this transaction')}
            </Button>
          </div>
        </div>
      }
    >
      {data && (
        <div className='grid grid-cols-2 gap-3 rounded-md border p-3 text-xs sm:grid-cols-4'>
          <div>
            <Label className='text-muted-foreground text-xs'>
              {t('Order')}
            </Label>
            <div className='truncate font-mono' title={data.trade_no}>
              {data.trade_no}
            </div>
          </div>
          <div>
            <Label className='text-muted-foreground text-xs'>
              {t('Expected')}
            </Label>
            <div className='font-mono font-semibold'>
              {data.expected_amount} {data.currency}
            </div>
          </div>
          <div>
            <Label className='text-muted-foreground text-xs'>
              {t('Credits')}
            </Label>
            <div className='font-semibold'>{data.amount}</div>
          </div>
          <div>
            <Label className='text-muted-foreground text-xs'>
              {t('Created')}
            </Label>
            <div>{formatTimestamp(data.created_at)}</div>
          </div>
        </div>
      )}

      {error && (
        <Alert>
          <AlertTriangle className='h-4 w-4' />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      {loading && !data && (
        <div className='text-muted-foreground flex items-center gap-2 py-6 text-sm'>
          <Loader2 className='h-4 w-4 animate-spin' />
          {t('Reading your Binance account history…')}
        </div>
      )}

      {data && data.candidates.length === 0 && (
        <Alert>
          <AlertDescription>
            {t(
              'No incoming transfer in this asset was found between the order time and now. If the customer says they paid, ask for their Binance transaction record; do not credit without one.'
            )}
          </AlertDescription>
        </Alert>
      )}

      {data && data.candidates.length > 0 && (
        <div className='space-y-2' role='radiogroup'>
          {data.candidates.map((c) => {
            const used = !!c.used_by_trade_no
            const isSelected = selected === c.transaction_id
            return (
              <button
                type='button'
                key={c.transaction_id}
                role='radio'
                aria-checked={isSelected}
                disabled={used}
                onClick={() => setSelected(c.transaction_id)}
                className={cn(
                  'w-full rounded-md border p-3 text-left text-xs transition-colors',
                  used && 'cursor-not-allowed opacity-50',
                  !used && 'hover:bg-muted/40',
                  isSelected && 'border-foreground bg-foreground/5'
                )}
              >
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <span className='font-mono text-sm font-semibold'>
                    {c.amount} {c.currency}
                  </span>
                  <span
                    className={cn(
                      'font-mono',
                      c.exact_match && 'text-green-600',
                      c.warn && 'font-semibold text-red-600'
                    )}
                  >
                    {c.exact_match
                      ? t('Exact match')
                      : `${c.delta.startsWith('-') ? '' : '+'}${c.delta} (${c.delta_percent.toFixed(2)}%)`}
                  </span>
                </div>
                <div className='text-muted-foreground mt-1 grid grid-cols-2 gap-x-4 gap-y-0.5 sm:grid-cols-4'>
                  <span>{sourceLabel(c.source, t)}</span>
                  <span className='truncate' title={c.transaction_id}>
                    {c.transaction_id}
                  </span>
                  <span>
                    {t('Payer')}:{' '}
                    {c.payer_name || c.payer_id || c.network || '-'}
                  </span>
                  <span>{formatTxTime(c.transact_time)}</span>
                </div>
                {used && (
                  <div className='mt-1 text-red-600'>
                    {t('Already used for order {{tradeNo}}', {
                      tradeNo: c.used_by_trade_no,
                    })}
                  </div>
                )}
                {c.warn && !used && (
                  <div className='mt-1 flex items-center gap-1 text-red-600'>
                    <AlertTriangle className='h-3 w-3' />
                    {t(
                      'Differs from the expected amount by more than 5%. Crediting it gives the full order amount.'
                    )}
                  </div>
                )}
              </button>
            )
          })}
        </div>
      )}
    </Dialog>
  )
}
