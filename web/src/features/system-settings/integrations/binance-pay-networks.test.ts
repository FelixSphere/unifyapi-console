/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  BINANCE_DEPOSIT_NETWORK_CODES,
  normaliseStatusAccounts,
  parseNetworks,
  type BinancePayResolvedAddress,
} from './binance-pay-networks'

describe('Binance Pay deposit networks', () => {
  test('reads the stored list and survives anything else the option holds', () => {
    assert.deepEqual(parseNetworks('["TRX","BSC"]'), ['TRX', 'BSC'])
    assert.deepEqual(parseNetworks('[]'), [])
    assert.deepEqual(parseNetworks(''), [])
    // A legacy or hand-edited value must not crash the settings page.
    assert.deepEqual(parseNetworks('TRX,BSC'), [])
    assert.deepEqual(parseNetworks('{"network":"TRX"}'), [])
    assert.deepEqual(parseNetworks('["TRX",7,null]'), ['TRX'])
  })

  test('offers the networks the backend knows how to normalise', () => {
    // Every code here must be one setting.NormalizeBinanceNetwork maps to
    // itself, or the resolver would ask Binance for a network it rejects.
    assert.deepEqual(BINANCE_DEPOSIT_NETWORK_CODES, [
      'TRX',
      'BSC',
      'ETH',
      'SOL',
      'MATIC',
      'ARBITRUM',
      'AVAXC',
      'BASE',
    ])
  })
})

describe('Binance Pay account status normalisation', () => {
  type RawAccount = {
    platform?: string
    networks?: string[] | null
    addresses?: BinancePayResolvedAddress[] | null
  }

  test('turns the null arrays an empty Go slice serialises into into empty ones', () => {
    // This exact payload -- a configured-but-address-less account -- crashed
    // the settings page into the error boundary, which renders a big "500".
    const raw: RawAccount[] = [
      { platform: 'binance.com', networks: null, addresses: null },
    ]
    const [account] = normaliseStatusAccounts(raw)
    assert.deepEqual(account.networks, [])
    assert.deepEqual(account.addresses, [])
    assert.equal(account.platform, 'binance.com')
  })

  test('survives a missing accounts list and drops malformed address rows', () => {
    assert.deepEqual(normaliseStatusAccounts(undefined), [])
    assert.deepEqual(normaliseStatusAccounts(null), [])

    const raw: RawAccount[] = [
      {
        addresses: [
          { network: 'TRX', address: 'TJUgo' },
          { network: 'BSC' },
          null,
        ] as unknown as BinancePayResolvedAddress[],
      },
    ]
    const [account] = normaliseStatusAccounts(raw)
    assert.deepEqual(account.addresses, [{ network: 'TRX', address: 'TJUgo' }])
  })
})
