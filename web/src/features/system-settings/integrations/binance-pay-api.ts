/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

// Health of the Binance Pay receiving accounts, for the settings page.

export interface BinancePayLastCheck {
  checked_at: number
  ok: boolean
  error?: string
  source: string
}

export interface BinancePayAccountStatus {
  platform: string
  label: string
  payment_method: string
  enabled: boolean
  has_credentials: boolean
  api_key_length: number
  secret_length: number
  pay_id: string
  pay_id_valid: boolean
  supports_pay: boolean
  address_count: number
  configured: boolean
  pending_orders: number
  last_check?: BinancePayLastCheck
  configured_reason: string
}

export interface BinancePayStatusResponse {
  compliance_confirmed: boolean
  accounts: BinancePayAccountStatus[]
}

export async function getBinancePayStatus(): Promise<BinancePayStatusResponse> {
  const res = await api.get('/api/option/binance-pay/status')
  return res.data.data as BinancePayStatusResponse
}

export interface BinancePayTestResult {
  platform: string
  label: string
  deposits_24h?: number
  pay_transactions_24h?: number
}

export async function testBinancePayAccount(platform: string): Promise<{
  success?: boolean
  message?: string
  data?: BinancePayTestResult
}> {
  const res = await api.post('/api/option/binance-pay/test', { platform }, {
    skipBusinessError: true,
  } as Record<string, unknown>)
  return res.data
}
