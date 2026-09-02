import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import {
  customerModelPricePayload,
  fallbackCustomerMultiplier,
  invalidCustomerModelPrices,
  mergeCustomerModelDrafts,
  priceAtMultiplier,
} from '../group-model-pricing-logic'

describe('customer model pricing', () => {
  it('uses the existing global price as a no-op default for a new rule', () => {
    assert.equal(
      fallbackCustomerMultiplier(
        'claude-opus-5',
        'GenAI',
        { 'claude-opus-5': 0.9 },
        { GenAI: 0.7 }
      ),
      0.63
    )
  })

  it('derives customer dollars directly from official price', () => {
    assert.deepEqual(priceAtMultiplier(5, 25, 0.8), {
      input: 4,
      output: 20,
    })
  })

  it('rejects zero, negative, non-numeric and implausibly large values', () => {
    assert.deepEqual(
      invalidCustomerModelPrices({
        good: '0.8',
        zero: '0',
        negative: '-1',
        text: 'eight',
        huge: '10.1',
      }),
      ['huge', 'negative', 'text', 'zero']
    )
  })

  it('only serializes present valid rules', () => {
    assert.deepEqual(customerModelPricePayload({ a: '0.8', b: '', c: 'bad' }), {
      a: 0.8,
    })
  })

  it('does not erase unsaved work in another customer after a refetch', () => {
    const merged = mergeCustomerModelDrafts(
      {
        GenAI: { 'claude-opus-5': 0.9 },
        UnifyAI: { 'claude-opus-5': 0.8 },
      },
      {
        GenAI: { 'claude-opus-5': '0.9' },
        UnifyAI: { 'claude-opus-5': '0.73' },
      },
      new Set(['UnifyAI'])
    )

    assert.deepEqual(merged, {
      GenAI: { 'claude-opus-5': '0.9' },
      UnifyAI: { 'claude-opus-5': '0.73' },
    })
  })
})
