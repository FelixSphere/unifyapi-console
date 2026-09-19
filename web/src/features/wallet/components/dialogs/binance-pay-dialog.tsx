/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { Check, CheckCircle2, Copy, Loader2, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import {
  binancePayPhase,
  useBinancePayOrderStatus,
} from '../../hooks/use-binance-pay-payment'
import { getPaymentIcon } from '../../lib'
import type { BinancePayOrder } from '../../types'

interface BinancePayDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  order: BinancePayOrder | null
  /** Called once when the order flips to paid. */
  onPaid?: (order: BinancePayOrder) => void
}

function formatRemaining(seconds: number): string {
  const s = Math.max(0, Math.floor(seconds))
  const mm = Math.floor(s / 60)
  const ss = s % 60
  return `${mm}:${ss.toString().padStart(2, '0')}`
}

function CopyRow({
  label,
  value,
  mono = true,
  emphasis = false,
}: {
  label: string
  value: string
  mono?: boolean
  emphasis?: boolean
}) {
  const { t } = useTranslation()
  const { copyToClipboard, copiedText } = useCopyToClipboard()
  const copied = copiedText === value
  return (
    <div className='bg-muted/30 flex items-center justify-between gap-3 rounded-md border px-3 py-2'>
      <div className='min-w-0'>
        <div className='text-muted-foreground text-[11px] tracking-wider uppercase'>
          {label}
        </div>
        <div
          className={`${mono ? 'font-mono' : ''} ${
            emphasis ? 'text-lg font-semibold' : 'text-sm'
          } truncate`}
          title={value}
        >
          {value}
        </div>
      </div>
      <Button
        type='button'
        variant='outline'
        size='sm'
        className='shrink-0 gap-1'
        onClick={() => void copyToClipboard(value)}
        aria-label={t('Copy {{label}}', { label })}
      >
        {copied ? (
          <Check className='h-3.5 w-3.5' />
        ) : (
          <Copy className='h-3.5 w-3.5' />
        )}
        {copied ? t('Copied') : t('Copy')}
      </Button>
    </div>
  )
}

/**
 * Transfer instructions for a Binance Pay order into the operator's personal
 * Binance account, plus live status. There is no redirect: the payer sends
 * the exact amount from their own Binance app and this dialog waits for the
 * reconciler to see it.
 */
export function BinancePayDialog({
  open,
  onOpenChange,
  order,
  onPaid,
}: BinancePayDialogProps) {
  const { t } = useTranslation()
  const { latest, checking, check } = useBinancePayOrderStatus(
    order,
    open,
    onPaid
  )
  const current = latest ?? order
  const phase = current ? binancePayPhase(current.status) : 'pending'

  const [now, setNow] = useState(() => Math.floor(Date.now() / 1000))
  useEffect(() => {
    if (!open || phase !== 'pending') return
    const timer = setInterval(() => setNow(Math.floor(Date.now() / 1000)), 1000)
    return () => clearInterval(timer)
  }, [open, phase])

  if (!current) {
    return null
  }

  const remaining = current.expires_at > 0 ? current.expires_at - now : 0
  const expiredLocally = current.expires_at > 0 && remaining <= 0
  const amountLabel = `${current.pay_amount} ${current.currency}`
  const addresses = current.deposit_addresses ?? []
  const hasPayId = !!current.receiver_id
  const isUS = current.platform === 'binance.us'
  const platformLabel =
    current.platform_label || (isUS ? 'Binance.US' : 'Binance (binance.com)')
  const otherPlatform = isUS ? 'binance.com' : 'Binance.US'

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={
        <span className='inline-flex items-center gap-2'>
          {getPaymentIcon(current.payment_method, 'h-5 w-5')}
          {isUS
            ? t('Pay with Binance.US')
            : t('Pay with Binance Pay (binance.com)')}
        </span>
      }
      description={
        hasPayId
          ? t(
              'Send the exact amount below from your Binance app. Your balance is credited automatically once the transfer is seen.'
            )
          : t(
              'Send the exact amount below to one of the addresses. Your balance is credited automatically once the deposit is confirmed.'
            )
      }
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <div className='flex w-full flex-wrap items-center justify-between gap-2'>
          <span className='text-muted-foreground text-xs'>
            {t('Order')} <span className='font-mono'>{current.trade_no}</span>
          </span>
          <div className='flex gap-2'>
            {phase === 'pending' && !expiredLocally && (
              <Button
                type='button'
                variant='outline'
                onClick={() => void check()}
                disabled={checking}
                className='gap-2'
              >
                {checking ? (
                  <Loader2 className='h-4 w-4 animate-spin' />
                ) : (
                  <RefreshCw className='h-4 w-4' />
                )}
                {t("I've paid, check now")}
              </Button>
            )}
            <Button type='button' onClick={() => onOpenChange(false)}>
              {phase === 'success' ? t('Done') : t('Close')}
            </Button>
          </div>
        </div>
      }
    >
      {phase === 'success' ? (
        <Alert>
          <CheckCircle2 className='h-4 w-4 text-green-600' />
          <AlertDescription>
            {t(
              'Payment received. {{amount}} has been credited to your balance.',
              {
                amount: amountLabel,
              }
            )}
          </AlertDescription>
        </Alert>
      ) : phase === 'expired' || expiredLocally ? (
        <Alert>
          <AlertDescription>
            {t(
              'This order has expired. If you already sent the transfer, contact support with the order number; otherwise start a new top-up.'
            )}
          </AlertDescription>
        </Alert>
      ) : phase === 'failed' ? (
        <Alert>
          <AlertDescription>
            {t('This order was cancelled. Please start a new top-up.')}
          </AlertDescription>
        </Alert>
      ) : (
        <>
          <div className='rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs'>
            <span className='font-medium'>
              {t('Receiving account: {{platform}}.', {
                platform: platformLabel,
              })}
            </span>{' '}
            {t(
              'Binance (binance.com) and Binance.US are separate companies. Send only from an account on {{platform}}; a transfer from {{other}} cannot reach it.',
              { platform: platformLabel, other: otherPlatform }
            )}
          </div>
          <CopyRow label={t('Amount to send')} value={amountLabel} emphasis />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Send exactly this amount, including the last decimals: they identify your order. A different amount cannot be matched automatically.'
            )}
          </p>

          {hasPayId && (
            <>
              <CopyRow
                label={t('Recipient Pay ID')}
                value={current.receiver_id}
              />
              {current.receiver_nickname && (
                <p className='text-muted-foreground text-xs'>
                  {t('Binance should show the recipient as')}{' '}
                  <span className='text-foreground font-medium'>
                    {current.receiver_nickname}
                  </span>
                </p>
              )}

              <ol className='text-muted-foreground list-decimal space-y-1 pl-5 text-xs'>
                <li>
                  {t(
                    'Open the Binance (binance.com) app and go to Pay, then Send.'
                  )}
                </li>
                <li>{t('Enter the recipient Pay ID above.')}</li>
                <li>
                  {t('Choose {{currency}} and enter the exact amount.', {
                    currency: current.currency,
                  })}
                </li>
                <li>
                  {t(
                    'Confirm the transfer. Binance Pay transfers are free and instant.'
                  )}
                </li>
              </ol>
            </>
          )}

          {addresses.length > 0 && (
            <div className='space-y-2 border-t pt-3'>
              <div className='text-muted-foreground text-[11px] tracking-wider uppercase'>
                {hasPayId ? t('Or send on-chain to') : t('Send on-chain to')}
              </div>
              {addresses.map((entry) => (
                <CopyRow
                  key={`${entry.network}-${entry.address}`}
                  label={t('{{network}} network', { network: entry.network })}
                  value={entry.address}
                />
              ))}
              <p className='text-muted-foreground text-xs'>
                {t(
                  'The amount that arrives must equal the amount above, so cover any network fee on your side. On-chain transfers are credited after network confirmation.'
                )}
              </p>
            </div>
          )}

          <div className='flex items-center justify-between rounded-md border px-3 py-2 text-xs'>
            <span className='inline-flex items-center gap-2'>
              <Loader2 className='h-3.5 w-3.5 animate-spin' />
              {t('Waiting for your transfer…')}
            </span>
            {current.expires_at > 0 && (
              <span className='text-muted-foreground'>
                {t('Expires in {{time}}', { time: formatRemaining(remaining) })}
              </span>
            )}
          </div>
        </>
      )}
    </Dialog>
  )
}
