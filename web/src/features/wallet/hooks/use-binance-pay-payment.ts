/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import i18next from 'i18next'
import { useCallback, useEffect, useRef, useState } from 'react'
import { toast } from 'sonner'

import {
  getBinancePayOrderStatus,
  isApiSuccess,
  requestBinancePayPayment,
} from '../api'
import { getPaymentErrorMessage } from '../lib'
import type { BinancePayOrder } from '../types'

// ============================================================================
// Binance Pay (personal account) hooks
// ============================================================================

const DEFAULT_POLL_INTERVAL_SECONDS = 10

/**
 * Normalise the order payload the backend returns. Every field the dialog
 * shows the payer must be a real value, never `undefined` rendered as text.
 */
export function parseBinancePayOrder(data: unknown): BinancePayOrder | null {
  if (!data || typeof data !== 'object') {
    return null
  }
  const raw = data as Record<string, unknown>
  const tradeNo = typeof raw.trade_no === 'string' ? raw.trade_no : ''
  const payAmount = typeof raw.pay_amount === 'string' ? raw.pay_amount : ''
  if (!tradeNo || !payAmount) {
    return null
  }
  const addresses = Array.isArray(raw.deposit_addresses)
    ? raw.deposit_addresses
        .filter(
          (item): item is Record<string, unknown> =>
            !!item && typeof item === 'object'
        )
        .map((item) => ({
          network: typeof item.network === 'string' ? item.network : '',
          address: typeof item.address === 'string' ? item.address : '',
        }))
        .filter((item) => item.network && item.address)
    : []

  return {
    trade_no: tradeNo,
    status: typeof raw.status === 'string' ? raw.status : 'pending',
    amount: Number(raw.amount) || 0,
    pay_amount: payAmount,
    currency: typeof raw.currency === 'string' ? raw.currency : 'USDT',
    receiver_id: typeof raw.receiver_id === 'string' ? raw.receiver_id : '',
    receiver_nickname:
      typeof raw.receiver_nickname === 'string' ? raw.receiver_nickname : '',
    deposit_addresses: addresses,
    created_at: Number(raw.created_at) || 0,
    expires_at: Number(raw.expires_at) || 0,
    poll_interval_seconds:
      Number(raw.poll_interval_seconds) || DEFAULT_POLL_INTERVAL_SECONDS,
  }
}

/**
 * Creates a Binance Pay order. Unlike the redirect gateways there is nothing
 * to open: the order carries the transfer instructions and the caller shows
 * them in a dialog.
 */
export function useBinancePayPayment() {
  const [processing, setProcessing] = useState(false)
  const [order, setOrder] = useState<BinancePayOrder | null>(null)

  const processBinancePayPayment = useCallback(async (topupAmount: number) => {
    setProcessing(true)
    try {
      const response = await requestBinancePayPayment({
        amount: Math.floor(topupAmount),
      })
      if (isApiSuccess(response)) {
        const parsed = parseBinancePayOrder(response.data)
        if (parsed) {
          setOrder(parsed)
          return true
        }
      }
      toast.error(getPaymentErrorMessage(response.message, response.data))
      return false
    } catch {
      toast.error(i18next.t('Payment request failed'))
      return false
    } finally {
      setProcessing(false)
    }
  }, [])

  const clearOrder = useCallback(() => setOrder(null), [])

  return { processing, order, processBinancePayPayment, clearOrder }
}

export type BinancePayOrderPhase = 'pending' | 'success' | 'expired' | 'failed'

export function binancePayPhase(status: string): BinancePayOrderPhase {
  switch (status) {
    case 'success':
      return 'success'
    case 'expired':
      return 'expired'
    case 'failed':
      return 'failed'
    default:
      return 'pending'
  }
}

/**
 * Polls the order until it leaves `pending`. The backend nudges its own
 * reconciler on each poll (rate limited), so a payer who has just sent the
 * money sees the credit land without waiting for the background tick.
 */
export function useBinancePayOrderStatus(
  order: BinancePayOrder | null,
  active: boolean,
  onPaid?: (order: BinancePayOrder) => void
) {
  const [latest, setLatest] = useState<BinancePayOrder | null>(order)
  const [checking, setChecking] = useState(false)
  const paidNotifiedRef = useRef<string | null>(null)
  const onPaidRef = useRef(onPaid)
  onPaidRef.current = onPaid

  useEffect(() => {
    setLatest(order)
    paidNotifiedRef.current = null
  }, [order])

  const check = useCallback(async () => {
    if (!order) return null
    setChecking(true)
    try {
      const response = await getBinancePayOrderStatus(order.trade_no)
      if (!isApiSuccess(response)) return null
      const parsed = parseBinancePayOrder(response.data)
      if (!parsed) return null
      setLatest(parsed)
      if (
        parsed.status === 'success' &&
        paidNotifiedRef.current !== parsed.trade_no
      ) {
        paidNotifiedRef.current = parsed.trade_no
        onPaidRef.current?.(parsed)
      }
      return parsed
    } catch {
      return null
    } finally {
      setChecking(false)
    }
  }, [order])

  useEffect(() => {
    if (!order || !active) return
    if (latest && binancePayPhase(latest.status) !== 'pending') return

    const seconds =
      order.poll_interval_seconds && order.poll_interval_seconds > 0
        ? order.poll_interval_seconds
        : DEFAULT_POLL_INTERVAL_SECONDS
    const timer = setInterval(() => {
      void check()
    }, seconds * 1000)
    return () => clearInterval(timer)
  }, [order, active, latest, check])

  return { latest, checking, check }
}
