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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { formatExplicitCurrencyAmount } from '../currency'

describe('formatExplicitCurrencyAmount', () => {
  test('base currency keeps its symbol, others carry an unambiguous ISO code', () => {
    // narrowSymbol renders SGD as "$13.20" in en-US, which is impossible to
    // tell from USD on the button that charges the card.
    assert.equal(formatExplicitCurrencyAmount(10, 'USD', 'en-US'), '$10.00')
    assert.equal(
      formatExplicitCurrencyAmount(40.9, 'MYR', 'en-US'),
      'MYR\u00a040.90'
    )
    assert.equal(
      formatExplicitCurrencyAmount(330, 'THB', 'en-US'),
      'THB\u00a0330.00'
    )
    assert.equal(
      formatExplicitCurrencyAmount(13.2, 'SGD', 'en-US'),
      'SGD\u00a013.20'
    )
  })

  test('fraction digits follow the currency, not a fixed two', () => {
    // A zero-decimal currency shown with cents implies a precision the gateway
    // cannot charge.
    assert.equal(
      formatExplicitCurrencyAmount(1500, 'JPY', 'en-US'),
      'JPY\u00a01,500'
    )
    assert.equal(
      formatExplicitCurrencyAmount(250000, 'VND', 'en-US'),
      'VND\u00a0250,000'
    )
  })

  test('does not convert the amount it is given', () => {
    // The server already computed the total in this currency; applying a rate
    // here would double-convert.
    assert.equal(
      formatExplicitCurrencyAmount(100, 'MYR', 'en-US'),
      'MYR\u00a0100.00'
    )
  })

  test('a malformed code falls back instead of throwing', () => {
    // Intl throws a RangeError on a bad currency code, which would replace the
    // payment figure with a blank render.
    assert.doesNotThrow(() =>
      formatExplicitCurrencyAmount(10, 'NOTACODE', 'en-US')
    )
    assert.doesNotThrow(() => formatExplicitCurrencyAmount(10, '', 'en-US'))
  })

  test('nullish amounts render as a dash, not NaN', () => {
    assert.equal(formatExplicitCurrencyAmount(null, 'MYR'), '-')
    assert.equal(formatExplicitCurrencyAmount(undefined, 'MYR'), '-')
    assert.equal(formatExplicitCurrencyAmount(Number.NaN, 'MYR'), '-')
  })
})
