/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
// Pure helpers for the Binance Pay deposit-address editor. Kept out of the
// React component so tests can import them without pulling the whole
// settings UI (and its module-level side effects) into the test process.

export interface DepositAddress {
  network: string
  address: string
}

/** One address per line: `NETWORK address`, e.g. `TRX TXYZ…`. */
export function parseDepositAddressLines(text: string): {
  addresses: DepositAddress[]
  invalidLine: string | null
} {
  const addresses: DepositAddress[] = []
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line) continue
    const parts = line.split(/[\s,]+/).filter(Boolean)
    if (parts.length !== 2) {
      return { addresses: [], invalidLine: line }
    }
    addresses.push({ network: parts[0].toUpperCase(), address: parts[1] })
  }
  return { addresses, invalidLine: null }
}

export function depositAddressesToLines(json: string): string {
  const trimmed = json.trim()
  if (!trimmed || trimmed === '[]') return ''
  try {
    const parsed = JSON.parse(trimmed)
    if (!Array.isArray(parsed)) return ''
    return parsed
      .filter(
        (item): item is Record<string, unknown> =>
          !!item && typeof item === 'object'
      )
      .map((item) =>
        typeof item.network === 'string' && typeof item.address === 'string'
          ? `${item.network} ${item.address}`
          : ''
      )
      .filter(Boolean)
      .join('\n')
  } catch {
    return ''
  }
}
