/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
/*
UNIFYAPI-FORK: tests for the upstream purchasing-cost field.

The number typed here becomes the cost basis of a reconciliation report finance
diffs against a vendor invoice, so the cases pin what must never happen quietly:
a zero cost (which would make every margin look infinite and hide real spend), a
cost above list slipping through unremarked, and a cleared field being persisted
as 1.0 forever.
*/
import assert from 'node:assert/strict'

import {
  MAX_CHANNEL_COST_RATIO,
  costLabel,
  costPayload,
  invalidCostChannels,
  maxSafeDiscount,
  parseCostRatio,
} from '../channel-cost-logic'

describe('parseCostRatio', () => {
  test('an empty field means "we pay this vendor list price"', () => {
    assert.equal(parseCostRatio(''), null)
    assert.equal(parseCostRatio('  '), null)
  })

  test('accepts a normal reseller contract', () => {
    assert.equal(parseCostRatio('0.85'), 0.85)
    assert.equal(parseCostRatio('0.7'), 0.7)
    assert.equal(parseCostRatio('1'), 1)
  })

  test('rejects a zero cost rather than treating the upstream as free', () => {
    // A free upstream makes every margin infinite and hides real spend, which
    // is worse than no report at all.
    assert.ok(Number.isNaN(parseCostRatio('0')))
    assert.ok(Number.isNaN(parseCostRatio('-0.8')))
  })

  test('rejects a cost beyond the sanity bound', () => {
    // Paying more than 5x a vendor public price is not a contract, it is a typo.
    assert.ok(
      Number.isNaN(parseCostRatio(String(MAX_CHANNEL_COST_RATIO + 0.01)))
    )
    assert.ok(Number.isNaN(parseCostRatio('99')))
    assert.equal(
      parseCostRatio(String(MAX_CHANNEL_COST_RATIO)),
      MAX_CHANNEL_COST_RATIO
    )
  })

  test('rejects text', () => {
    assert.ok(Number.isNaN(parseCostRatio('list minus 15%')))
    assert.ok(Number.isNaN(parseCostRatio('0.85x')))
  })
})

describe('costLabel', () => {
  test('classifies the contract without baking in a language', () => {
    // Structured, so a Chinese UI is not forced to render English.
    assert.deepEqual(costLabel(1), { kind: 'list' })
    // Percentages are compared with a tolerance: (1 - 0.7) * 100 is
    // 30.000000000000004 in binary floating point, and asserting that literal
    // would be pinning an artifact rather than the behaviour.
    for (const [ratio, percent] of [
      [0.85, 15],
      [0.7, 30],
      [0.5, 50],
    ] as const) {
      const label = costLabel(ratio)
      assert.equal(label.kind, 'discount')
      assert.ok(
        label.kind === 'discount' && Math.abs(label.percent - percent) < 1e-9,
        `costLabel(${ratio}) should be ${percent}% off list`
      )
    }
  })

  test('paying above list is its own kind, not a discount', () => {
    // Above 1 means we pay more than the vendor public price. Rare enough that
    // it is almost always a mistake, so it must never classify as a discount.
    const label = costLabel(1.1)
    assert.equal(label.kind, 'above-list')
    assert.ok(
      label.kind === 'above-list' && Math.abs(label.percent - 10) < 1e-9
    )
  })
})

describe('maxSafeDiscount', () => {
  test('equals the purchasing discount, because margin at list is zero', () => {
    // Selling at official list while buying at official list is exactly break
    // even, so the purchasing discount IS the floor on a customer discount.
    assert.equal(maxSafeDiscount(0.7), 0.7)
    assert.equal(maxSafeDiscount(1), 1)
  })
})

describe('costPayload', () => {
  test('only channels with a negotiated rate are persisted', () => {
    assert.deepEqual(
      costPayload({ '1': '0.7', '2': '1', '3': '', '4': '0.85' }),
      { '1': 0.7, '4': 0.85 }
    )
  })

  test('clearing a field removes the override instead of pinning 1.0', () => {
    assert.deepEqual(costPayload({ '1': '' }), {})
    assert.deepEqual(costPayload({ '1': '1' }), {})
  })

  test('an invalid field is never silently persisted', () => {
    assert.deepEqual(costPayload({ '1': '0', '2': '99', '3': 'abc' }), {})
  })
})

describe('invalidCostChannels', () => {
  test('names the channels blocking a save', () => {
    assert.deepEqual(
      invalidCostChannels({ '1': '0.7', '2': '0', '3': '', '4': '99' }),
      ['2', '4']
    )
  })

  test('an all-valid table blocks nothing', () => {
    assert.deepEqual(invalidCostChannels({ '1': '0.7', '2': '' }), [])
  })
})
