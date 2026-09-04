/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, it } from 'bun:test'
import assert from 'node:assert/strict'

import {
  customerModelPricePayload,
  effectiveCustomerMultiplier,
  fallbackCustomerMultiplier,
  formatCustomerMultiplier,
  invalidCustomerModelPrices,
  mergeCustomerModelDrafts,
  normalizeCustomerPricingGroupRatios,
  priceAtMultiplier,
  visibleCustomerModelNames,
  visibleCustomerPricingGroups,
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

  it('shows draft pricing groups immediately without reviving deleted groups', () => {
    assert.deepEqual(
      visibleCustomerPricingGroups(['Deleted', 'GenAI'], {
        UnifyAI: 1,
        GenAI: 0.9,
      }),
      ['GenAI', 'UnifyAI']
    )
  })

  it('normalizes draft group names before both listing and default pricing', () => {
    const normalized = normalizeCustomerPricingGroupRatios({
      ' Acme ': 0.8,
      '': 0.5,
      'Not a number': Number.NaN,
    })
    assert.deepEqual(normalized, { Acme: 0.8 })
    assert.deepEqual(visibleCustomerPricingGroups([], normalized), ['Acme'])
    assert.equal(
      fallbackCustomerMultiplier('claude-opus-5', 'Acme', {}, normalized),
      0.8
    )
  })

  it('uses the later ratio when two draft names trim to the same group', () => {
    assert.deepEqual(
      normalizeCustomerPricingGroupRatios({ ' Acme': 0.8, 'Acme ': 0.7 }),
      { Acme: 0.7 }
    )
  })

  it('falls back to saved groups when no pricing-group draft is supplied', () => {
    assert.deepEqual(
      visibleCustomerPricingGroups(['UnifyAI', 'GenAI', 'GenAI']),
      ['GenAI', 'UnifyAI']
    )
  })

  it('shows every model at its inherited default until an override is typed', () => {
    assert.ok(
      Math.abs(
        Number(
          effectiveCustomerMultiplier(
            'claude-opus-5',
            'GenAI',
            {},
            { 'claude-opus-5': 0.9 },
            { GenAI: 0.8 }
          )
        ) - 0.72
      ) < 1e-12
    )
    assert.equal(
      effectiveCustomerMultiplier(
        'claude-opus-5',
        'GenAI',
        { 'claude-opus-5': '0.65' },
        { 'claude-opus-5': 0.9 },
        { GenAI: 0.8 }
      ),
      0.65
    )
    assert.equal(formatCustomerMultiplier(0.9 * 0.8), '0.72')
  })

  it('lists every catalog model by default and filters without mutating it', () => {
    const models = ['qwen3.5-flash', 'claude-opus-5', 'gemini-3.1-pro']
    assert.deepEqual(visibleCustomerModelNames(models, ''), [
      'claude-opus-5',
      'gemini-3.1-pro',
      'qwen3.5-flash',
    ])
    assert.deepEqual(visibleCustomerModelNames(models, 'OPUS'), [
      'claude-opus-5',
    ])
    assert.deepEqual(models, [
      'qwen3.5-flash',
      'claude-opus-5',
      'gemini-3.1-pro',
    ])
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
