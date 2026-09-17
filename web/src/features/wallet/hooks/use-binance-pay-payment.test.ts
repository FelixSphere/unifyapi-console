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
  binancePayPhase,
  parseBinancePayOrder,
} from './use-binance-pay-payment'

describe('Binance Pay order parsing', () => {
  test('keeps the exact pay amount as a string and drops malformed addresses', () => {
    const order = parseBinancePayOrder({
      trade_no: 'BNP-1-2-abc',
      status: 'pending',
      amount: 20,
      // A float here would lose the identifier suffix; the backend sends text.
      pay_amount: '20.0137',
      currency: 'USDT',
      receiver_id: '34355667',
      deposit_addresses: [
        { network: 'TRX', address: 'TXYZ' },
        { network: 'BSC' },
        'garbage',
      ],
      created_at: 1,
      expires_at: 3601,
      poll_interval_seconds: 10,
    })

    assert.ok(order)
    assert.equal(order.pay_amount, '20.0137')
    assert.equal(order.receiver_id, '34355667')
    assert.deepEqual(order.deposit_addresses, [
      { network: 'TRX', address: 'TXYZ' },
    ])
  })

  test('refuses a payload without the two fields the payer must see', () => {
    assert.equal(parseBinancePayOrder({ trade_no: 'x' }), null)
    assert.equal(parseBinancePayOrder({ pay_amount: '1.0001' }), null)
    assert.equal(parseBinancePayOrder('error text'), null)
  })

  test('maps every unknown status to pending, never to paid', () => {
    assert.equal(binancePayPhase('success'), 'success')
    assert.equal(binancePayPhase('expired'), 'expired')
    assert.equal(binancePayPhase('failed'), 'failed')
    assert.equal(binancePayPhase('pending'), 'pending')
    assert.equal(binancePayPhase('something-new'), 'pending')
  })
})
