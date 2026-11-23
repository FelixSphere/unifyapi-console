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
import { getDefaultGroupDiscount } from '../model-helpers'
import { formatPrice } from '../price'

test('Model Square quotes the public 115% rate and shows no discount', () => {
  const model: PricingModel = {
    id: 1,
    model_name: 'claude-mythos-5-1',
    quota_type: 0,
    model_ratio: 5,
    completion_ratio: 5,
    enable_groups: ['default', 'friend solo'],
    group_ratio: { default: 0.85, 'friend solo': 0.85 },
    default_group_model_ratio: 1.15,
    customer_group_model_ratio: 0.8,
  }

  const amount = (value: string): number =>
    Number(value.replaceAll(/[^0-9.]/g, ''))

  assert.equal(amount(formatPrice(model, 'input', 'M')), 11.5)
  assert.equal(
    amount(formatPrice(model, 'output', 'M', false, 1, 1, 'friend solo')),
    57.5
  )
  assert.equal(getDefaultGroupDiscount(model), null)
})
