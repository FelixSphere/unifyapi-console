/*
UNIFYAPI-FORK: tests for the discount field's logic.

This is the part of the pricing UI that decides money, so it is tested directly
rather than through the DOM. The cases pin the three behaviours a mistyped
discount must never have: silently becoming 1 (un-discounting a model), silently
becoming 0 (making it free), and being persisted as 1.0 forever once cleared.
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import {
  discountLabel,
  discountPayload,
  formatUSD,
  invalidDiscountModels,
  parseDiscount,
} from '../baseline-pricing-logic'

describe('parseDiscount', () => {
  test('an empty field means "sell at the official price"', () => {
    assert.equal(parseDiscount(''), null)
    assert.equal(parseDiscount('   '), null)
  })

  test('accepts valid multipliers, including a markup', () => {
    assert.equal(parseDiscount('0.85'), 0.85)
    assert.equal(parseDiscount('1'), 1)
    assert.equal(parseDiscount(' 0.5 '), 0.5)
    assert.equal(parseDiscount('2'), 2)
  })

  test('rejects zero and negatives instead of coercing them', () => {
    // A zero would make the model free. Reachable by a typo, so it must fail
    // loudly rather than parse.
    assert.ok(Number.isNaN(parseDiscount('0')))
    assert.ok(Number.isNaN(parseDiscount('-0.5')))
  })

  test('rejects text and infinities', () => {
    assert.ok(Number.isNaN(parseDiscount('abc')))
    assert.ok(Number.isNaN(parseDiscount('0.8x')))
    assert.ok(Number.isNaN(parseDiscount('Infinity')))
  })
})

describe('discountPayload', () => {
  test('only deviations from the official price are persisted', () => {
    assert.deepEqual(
      discountPayload({
        'claude-opus-5': '0.85',
        'gpt-4o': '1',
        'gpt-4o-mini': '',
        'claude-sonnet-5': '0.7',
      }),
      { 'claude-opus-5': 0.85, 'claude-sonnet-5': 0.7 }
    )
  })

  test('clearing a field removes the discount rather than pinning 1.0', () => {
    // The stored table is replaced, not merged, so an omitted model goes back
    // to its official price. If the payload carried 1.0 instead, the exception
    // list would grow forever and stop being reviewable.
    assert.deepEqual(discountPayload({ 'gpt-4o': '' }), {})
    assert.deepEqual(discountPayload({ 'gpt-4o': '1' }), {})
    assert.deepEqual(discountPayload({}), {})
  })

  test('an invalid field is never silently persisted', () => {
    assert.deepEqual(discountPayload({ 'gpt-4o': '0', 'o': 'abc' }), {})
  })
})

describe('invalidDiscountModels', () => {
  test('names every unusable row so the admin knows which to fix', () => {
    assert.deepEqual(
      invalidDiscountModels({
        'gpt-4o': '0.9',
        'claude-opus-5': '0',
        'gpt-4o-mini': 'oops',
        'claude-sonnet-5': '',
      }),
      ['claude-opus-5', 'gpt-4o-mini']
    )
  })

  test('an all-valid table blocks nothing', () => {
    assert.deepEqual(invalidDiscountModels({ 'gpt-4o': '0.9', x: '' }), [])
  })
})

describe('discountLabel', () => {
  test('reads as the discount an operator would say', () => {
    assert.equal(discountLabel(1), 'list')
    assert.equal(discountLabel(0.85), '-15%')
    assert.equal(discountLabel(0.7), '-30%')
  })

  test('a multiplier above 1 is named a markup, not a discount', () => {
    // Four models on production were priced above vendor list. That is a valid
    // decision but must never read as if it were a discount.
    assert.equal(discountLabel(2), '+100% markup')
    assert.equal(discountLabel(1.2), '+20% markup')
  })
})

describe('formatUSD', () => {
  test('keeps cheap models distinguishable from each other', () => {
    // deepseek-v4-flash is $0.14/1M; at two decimals several models collapse
    // into "$0.06" and a wrong price stops looking wrong.
    assert.equal(formatUSD(0.0625), '$0.0625')
    assert.equal(formatUSD(0.14), '$0.140')
    assert.equal(formatUSD(0.9), '$0.900')
  })

  test('uses cents for prices where cents are the meaningful unit', () => {
    assert.equal(formatUSD(5), '$5.00')
    assert.equal(formatUSD(120), '$120.00')
  })

  test('renders an absent price as a dash, not as free', () => {
    // A vendor publishing no cache price is not the same claim as charging
    // zero for it.
    assert.equal(formatUSD(0), '—')
  })
})
