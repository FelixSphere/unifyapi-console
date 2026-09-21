/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
// Pure helpers for the Binance Pay deposit-network picker. Kept out of the
// React component so tests can import them without pulling the whole
// settings UI into the test process.

/**
 * The deposit networks an operator can accept, as Binance names them. The
 * ADDRESS for each is never typed by hand: it is read from Binance for the
 * account the API key belongs to, so "where customers send money" and "which
 * account we reconcile against" cannot drift apart.
 */
export const DEPOSIT_NETWORKS = [
  { code: 'TRX', label: 'Tron (TRC20)' },
  { code: 'BSC', label: 'BNB Smart Chain (BEP20)' },
  { code: 'ETH', label: 'Ethereum (ERC20)' },
  { code: 'SOL', label: 'Solana' },
  { code: 'MATIC', label: 'Polygon' },
  { code: 'ARBITRUM', label: 'Arbitrum One' },
  { code: 'AVAXC', label: 'Avalanche C-Chain' },
  { code: 'BASE', label: 'Base' },
] as const

export const BINANCE_DEPOSIT_NETWORK_CODES = DEPOSIT_NETWORKS.map(
  (network) => network.code
)

/** Reads the stored option, tolerating anything a hand-edit could leave. */
export function parseNetworks(raw: string): string[] {
  try {
    const parsed = JSON.parse(raw || '[]')
    return Array.isArray(parsed)
      ? parsed.filter((n): n is string => typeof n === 'string')
      : []
  } catch {
    return []
  }
}
