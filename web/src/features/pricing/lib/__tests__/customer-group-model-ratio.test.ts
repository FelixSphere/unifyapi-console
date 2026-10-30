/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { test } from 'bun:test'
import assert from 'node:assert/strict'

import type { PricingModel } from '../../types'
import { filterByQuotaType } from '../filters'
import { getDisplayGroupRatio } from '../model-helpers'

/*
Model Square is the PUBLIC catalogue: it prices from the model's own ratio
(official list x the global ModelDiscount) times a group ratio.

This file used to assert the opposite -- "customer model price wins over
selected, minimum, and fallback group ratios" -- and that assertion was the
regression, written down and locked in. Two things went wrong because of it:

  1. one customer's negotiated rate was shown on a public page;
  2. the contract was returned BEFORE any other branch, so the group filter was
     inert -- filtering to a group with no contract still showed the viewer's
     own discount.

The visible symptom: ModelDiscount was reset to 1 so the page would quote true
list prices, and the page kept quoting 0.9x, making it look as though the reset
had not taken effect at all.
*/

const model = (over: Partial<PricingModel> = {}): PricingModel =>
  ({
    id: 1,
    model_name: 'claude-opus-5',
    quota_type: 0,
    model_ratio: 2.5,
    completion_ratio: 5,
    enable_groups: ['GenAI', 'UnifyAI'],
    group_ratio: { GenAI: 0.7, UnifyAI: 1 },
    ...over,
  }) as PricingModel

test('a viewer contract never sets the public price', () => {
  const withContract = model({ customer_group_model_ratio: 0.8 })

  // Selected group wins, contract ignored.
  assert.equal(getDisplayGroupRatio(withContract, 'UnifyAI'), 1)
  assert.equal(getDisplayGroupRatio(withContract, 'GenAI'), 0.7)

  // With no filter: the best publicly available group ratio, still not 0.8.
  assert.equal(getDisplayGroupRatio(withContract), 0.7)
})

test('the contract does not change what a contract-free viewer sees', () => {
  const plain = model()
  const withContract = model({ customer_group_model_ratio: 0.8 })

  for (const group of [undefined, 'GenAI', 'UnifyAI']) {
    assert.equal(
      getDisplayGroupRatio(withContract, group),
      getDisplayGroupRatio(plain, group),
      `group ${group}: two viewers must see the same public price`
    )
  }
})

test('the group filter is not inert', () => {
  // The specific failure: filtering to a group whose ratio is 1 must show 1,
  // not the viewer's discount. This is what made "why is Model Square still
  // showing 0.9 when every model discount is 1" impossible to explain.
  const m = model({
    customer_group_model_ratio: 0.9,
    enable_groups: ['GenAI', 'Builder_hub_2026'],
    group_ratio: { GenAI: 1, Builder_hub_2026: 1 },
  })
  assert.equal(getDisplayGroupRatio(m, 'Builder_hub_2026'), 1)
  assert.equal(getDisplayGroupRatio(m, 'GenAI'), 1)
  assert.equal(getDisplayGroupRatio(m), 1)
})

test('with every group at 1, the page quotes official list price', () => {
  // The state production is in: ModelDiscount cleared to 1, group ratios 1.
  // model_ratio 2.5 is $5/1M, so the displayed multiplier must be exactly 1
  // and the page must show the vendor's published number.
  const m = model({
    customer_group_model_ratio: 0.9,
    group_ratio: { GenAI: 1, UnifyAI: 1 },
  })
  assert.equal(getDisplayGroupRatio(m), 1)
})

test('per-second videos are separate from fixed request prices', () => {
  const token = model()
  const request = model({ quota_type: 1, model_price: 0.1 })
  const second = model({
    quota_type: 1,
    model_price: 0.14,
    price_unit: 'second',
  })
  const models = [token, request, second]
  assert.deepEqual(filterByQuotaType(models, 'token'), [token])
  assert.deepEqual(filterByQuotaType(models, 'request'), [request])
  assert.deepEqual(filterByQuotaType(models, 'second'), [second])
})
