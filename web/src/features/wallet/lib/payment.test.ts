/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { PAYMENT_TYPES } from '../constants'
import {
  binancePayPlatformForType,
  dispatchSelectedPayment,
  isBinancePayPayment,
  isStripePayment,
  isWaffoPayment,
  isWaffoPancakePayment,
} from './payment'

describe('payment type classification', () => {
  test('keeps Waffo and Waffo Pancake on their dedicated flows', () => {
    assert.equal(isWaffoPayment(PAYMENT_TYPES.WAFFO), true)
    assert.equal(isWaffoPayment(PAYMENT_TYPES.WAFFO_PANCAKE), false)
    assert.equal(isWaffoPancakePayment(PAYMENT_TYPES.WAFFO_PANCAKE), true)
    assert.equal(isWaffoPancakePayment(PAYMENT_TYPES.WAFFO), false)
    assert.equal(isStripePayment(PAYMENT_TYPES.STRIPE), true)
    assert.equal(isBinancePayPayment(PAYMENT_TYPES.BINANCE_PAY), true)
    assert.equal(isBinancePayPayment(PAYMENT_TYPES.BINANCE_PAY_US), true)
    assert.equal(isBinancePayPayment(PAYMENT_TYPES.STRIPE), false)
    assert.equal(
      binancePayPlatformForType(PAYMENT_TYPES.BINANCE_PAY),
      'binance.com'
    )
    assert.equal(
      binancePayPlatformForType(PAYMENT_TYPES.BINANCE_PAY_US),
      'binance.us'
    )
  })
})

describe('payment dispatch', () => {
  test('keeps the selected Waffo method index through confirmation', async () => {
    const calls: string[] = []
    const success = await dispatchSelectedPayment(
      { name: 'Waffo Card', type: PAYMENT_TYPES.WAFFO },
      120,
      3,
      {
        regular: async () => {
          calls.push('regular')
          return false
        },
        waffo: async (amount, index) => {
          calls.push(`waffo:${amount}:${index}`)
          return true
        },
        waffoPancake: async () => {
          calls.push('pancake')
          return false
        },
        binancePay: async () => {
          calls.push('binance')
          return false
        },
      }
    )

    assert.equal(success, true)
    assert.deepEqual(calls, ['waffo:120:3'])
  })

  test('routes both Binance tiles to the Binance processor with their own type', async () => {
    const calls: string[] = []
    const processors = {
      regular: async () => {
        calls.push('regular')
        return false
      },
      waffo: async () => false,
      waffoPancake: async () => false,
      binancePay: async (amount: number, type: string) => {
        calls.push(`binance:${amount}:${type}`)
        return true
      },
    }
    assert.equal(
      await dispatchSelectedPayment(
        { name: 'Binance Pay (binance.com)', type: PAYMENT_TYPES.BINANCE_PAY },
        50,
        null,
        processors
      ),
      true
    )
    assert.equal(
      await dispatchSelectedPayment(
        { name: 'Binance.US', type: PAYMENT_TYPES.BINANCE_PAY_US },
        20,
        null,
        processors
      ),
      true
    )
    // The receiving company travels with the tile type; the epay form is never used.
    assert.deepEqual(calls, [
      'binance:50:binance_pay',
      'binance:20:binance_pay_us',
    ])
  })

  test('does not create a Waffo order without a selected method index', async () => {
    let called = false
    const success = await dispatchSelectedPayment(
      { name: 'Waffo Card', type: PAYMENT_TYPES.WAFFO },
      120,
      null,
      {
        regular: async () => false,
        waffo: async () => {
          called = true
          return true
        },
        waffoPancake: async () => false,
        binancePay: async () => false,
      }
    )

    assert.equal(success, false)
    assert.equal(called, false)
  })
})
