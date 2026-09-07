/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * The dollars <-> quota conversion used by every money input in the UI.
 *
 * These two functions are the only place a number an operator TYPES becomes a
 * balance a customer SPENDS, across a factor of 500,000 and a currency rate.
 * They had no tests at all. Production runs a CUSTOM currency (RM at 4.09), so
 * the custom-rate path below is the live one, not a hypothetical.
 *
 * Pinned here: the scale, the rate direction, and the round trip. A rate
 * applied in the wrong direction still "works" -- it silently misprices every
 * top-up by the square of the rate.
 */
import { beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
  type CurrencyConfig,
} from '@/stores/system-config-store'

import { parseQuotaFromDollars, quotaUnitsToDollars } from '../format'

function configure(currency: Partial<CurrencyConfig>) {
  const state = useSystemConfigStore.getState()
  useSystemConfigStore.setState({
    config: {
      ...state.config,
      currency: { ...DEFAULT_CURRENCY_CONFIG, ...currency },
    },
  })
}

beforeEach(() => configure({}))

describe('dollars <-> quota, USD display', () => {
  test('one dollar is quotaPerUnit units, in both directions', () => {
    assert.equal(parseQuotaFromDollars(1), 500_000)
    assert.equal(parseQuotaFromDollars(100), 50_000_000)
    assert.equal(quotaUnitsToDollars(50_000_000), 100)
  })

  test('a non-default quotaPerUnit is honoured, not assumed', () => {
    configure({ quotaPerUnit: 1_000_000 })
    assert.equal(parseQuotaFromDollars(1), 1_000_000)
    assert.equal(quotaUnitsToDollars(1_000_000), 1)
  })
})

describe('dollars <-> quota, custom currency (production shape: RM at 4.09)', () => {
  beforeEach(() =>
    configure({
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: 'RM',
      customCurrencyExchangeRate: 4.09,
    })
  )

  test('an amount typed in the display currency is stored as its USD worth', () => {
    // RM 409 is USD 100 at 4.09, which is 50,000,000 quota. If the rate were
    // applied the other way this would come out 16.7x too large.
    assert.equal(parseQuotaFromDollars(409), 50_000_000)
  })

  test('a stored balance is displayed back in the display currency', () => {
    assert.equal(quotaUnitsToDollars(50_000_000), 409)
  })

  test('the round trip holds to within one quota unit, so an edit screen does not drift on reopen', () => {
    // Quota is an integer, so a display amount cannot always be represented
    // exactly: the quantum is one unit, worth rate / quotaPerUnit in the
    // display currency (RM 0.00000818 here). Half of that is the most a round
    // trip may lose, and asserting that bound rather than an invented epsilon
    // is what makes this test mean something -- RM 1 really does come back as
    // 0.9999968, and that is correct.
    const halfAQuotaUnit = 4.09 / 500_000 / 2
    for (const displayAmount of [1, 4.09, 10, 409, 1234.56]) {
      const roundTripped = quotaUnitsToDollars(
        parseQuotaFromDollars(displayAmount)
      )
      assert.ok(
        Math.abs(roundTripped - displayAmount) <= halfAQuotaUnit,
        `${displayAmount} -> ${roundTripped} (drift beyond one quota unit)`
      )
    }
  })
})

describe('dollars <-> quota, token display', () => {
  beforeEach(() => configure({ quotaDisplayType: 'TOKENS' }))

  test('tokens are quota units already, so neither direction scales', () => {
    assert.equal(parseQuotaFromDollars(1200), 1200)
    assert.equal(quotaUnitsToDollars(1200), 1200)
  })

  test('a fractional token count is rounded, never truncated to zero', () => {
    assert.equal(parseQuotaFromDollars(0.6), 1)
  })
})

describe('inputs that must not become a wrong balance', () => {
  test('a non-numeric amount is zero, not NaN', () => {
    // NaN would reach the API and, before any guard, could blank a balance.
    assert.equal(parseQuotaFromDollars(Number.NaN), 0)
    assert.equal(parseQuotaFromDollars(Number.POSITIVE_INFINITY), 0)
  })

  test('an unusable exchange rate falls back rather than dividing by zero', () => {
    configure({
      quotaDisplayType: 'CUSTOM',
      customCurrencyExchangeRate: 0,
    })
    // getConfig() rejects a non-positive rate and keeps the default of 1, so
    // the amount is treated as USD instead of becoming Infinity.
    assert.equal(parseQuotaFromDollars(100), 50_000_000)
  })
})
