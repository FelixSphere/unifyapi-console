import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import type { PricingModel } from '../types'
import { getDisplayGroupRatio } from './model-helpers'

describe('customer contract pricing in model square', () => {
  it('uses the company-model contract instead of a legacy group ratio', () => {
    const model = {
      id: 1,
      model_name: 'claude-opus-5',
      quota_type: 0,
      model_ratio: 2.5,
      completion_ratio: 5,
      enable_groups: ['default', 'vip'],
      group_ratio: { default: 0.9, vip: 0.8 },
      customer_contract_discount: 0.7,
    } as PricingModel

    assert.equal(getDisplayGroupRatio(model), 0.7)
    assert.equal(getDisplayGroupRatio(model, 'vip'), 0.7)
  })
})
