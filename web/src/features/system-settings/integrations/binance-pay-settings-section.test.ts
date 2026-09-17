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
  depositAddressesToLines,
  parseDepositAddressLines,
} from './binance-pay-deposit-addresses'

describe('Binance Pay deposit address editor', () => {
  test('round-trips "NETWORK address" lines through the stored JSON', () => {
    const { addresses, invalidLine } = parseDepositAddressLines(
      'trx TXYZ\n\n  BSC, 0xabc  \n'
    )
    assert.equal(invalidLine, null)
    assert.deepEqual(addresses, [
      { network: 'TRX', address: 'TXYZ' },
      { network: 'BSC', address: '0xabc' },
    ])
    assert.equal(
      depositAddressesToLines(JSON.stringify(addresses)),
      'TRX TXYZ\nBSC 0xabc'
    )
  })

  test('names the line it could not read instead of silently dropping it', () => {
    const { addresses, invalidLine } = parseDepositAddressLines(
      'TRX TXYZ\njust-an-address'
    )
    assert.equal(invalidLine, 'just-an-address')
    assert.deepEqual(addresses, [])
  })

  test('treats a broken stored value as no addresses', () => {
    assert.equal(depositAddressesToLines('not json'), '')
    assert.equal(depositAddressesToLines('[]'), '')
    assert.equal(depositAddressesToLines('{"network":"TRX"}'), '')
  })
})
