/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * The operator-facing quota settings are denominated in US DOLLARS.
 *
 * QuotaForNewUser was set to 5 in production by an operator who meant five
 * dollars. The field took raw quota units, so new users were granted 5 units
 * -- $0.00001, not enough for one request -- and nobody noticed, because at
 * that size a broken grant and a working one look identical.
 *
 * Dollars, specifically, and not the console's display currency: every top-up
 * path on the server computes `quota = USD * QuotaPerUnit` (model/topup.go)
 * with no exchange rate. If these inputs followed the display setting, then
 * switching the console to RM would silently redefine how much credit a new
 * user receives, while the money actually taken at top-up stayed in dollars.
 */
import { beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
  type CurrencyConfig,
} from '@/stores/system-config-store'

import { quotaUnitsToUsd, usdToQuotaUnits } from '../format'

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

describe('operator quota settings are in US dollars', () => {
  test('the incident: typing 5 means five dollars, not five units', () => {
    assert.equal(usdToQuotaUnits(5), 2_500_000)
    // What production actually held, and what it was worth.
    assert.equal(quotaUnitsToUsd(5), 0.00001)
  })

  test('one dollar is quotaPerUnit units, in both directions', () => {
    assert.equal(usdToQuotaUnits(1), 500_000)
    assert.equal(quotaUnitsToUsd(500_000), 1)
    assert.equal(usdToQuotaUnits(100), 50_000_000)
    assert.equal(quotaUnitsToUsd(50_000_000), 100)
  })

  test('quotaPerUnit is read, not assumed', () => {
    configure({ quotaPerUnit: 1_000_000 })
    assert.equal(usdToQuotaUnits(1), 1_000_000)
    assert.equal(quotaUnitsToUsd(1_000_000), 1)
  })

  test('agrees with the server: quota = USD * QuotaPerUnit', () => {
    // model/topup.go computes exactly this for a $5 top-up.
    const serverSide = 5 * DEFAULT_CURRENCY_CONFIG.quotaPerUnit
    assert.equal(usdToQuotaUnits(5), serverSide)
  })
})

describe('the display currency must not move these numbers', () => {
  const displays: Array<Partial<CurrencyConfig>> = [
    { quotaDisplayType: 'USD' },
    { quotaDisplayType: 'CNY', usdExchangeRate: 7.1 },
    // Production's shape.
    {
      quotaDisplayType: 'CUSTOM',
      customCurrencySymbol: 'RM',
      customCurrencyExchangeRate: 4.09,
    },
    { quotaDisplayType: 'TOKENS' },
  ]

  test('five dollars is 2,500,000 units under every display setting', () => {
    for (const display of displays) {
      configure(display)
      assert.equal(
        usdToQuotaUnits(5),
        2_500_000,
        `display ${display.quotaDisplayType} changed what $5 means`
      )
      assert.equal(quotaUnitsToUsd(2_500_000), 5)
    }
  })
})

describe('round trip and edges', () => {
  test('units survive a round trip through the form', () => {
    for (const units of [0, 5, 500_000, 2_500_000, 50_000_000]) {
      assert.equal(usdToQuotaUnits(quotaUnitsToUsd(units)), units)
    }
  })

  test('non-finite input is zero, never NaN written to an option', () => {
    assert.equal(usdToQuotaUnits(Number.NaN), 0)
    assert.equal(usdToQuotaUnits(Number.POSITIVE_INFINITY), 0)
    assert.equal(quotaUnitsToUsd(Number.NaN), 0)
  })

  test('a missing or zero quotaPerUnit falls back to the default', () => {
    // The config layer substitutes the default rather than letting a zero
    // through, so there is no division by zero to guard against here.
    configure({ quotaPerUnit: 0 })
    assert.equal(quotaUnitsToUsd(2_500_000), 5)
    assert.equal(usdToQuotaUnits(5), 2_500_000)
  })
})
