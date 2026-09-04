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
import { getDisplayGroupRatio } from '../model-helpers'

const model: PricingModel = {
  id: 1,
  model_name: 'claude-opus-5',
  quota_type: 0,
  model_ratio: 2.5,
  completion_ratio: 5,
  enable_groups: ['GenAI', 'UnifyAI'],
  group_ratio: { GenAI: 0.7, UnifyAI: 1 },
  customer_group_model_ratio: 0.8,
}

test('customer model price wins over selected, minimum, and fallback group ratios', () => {
  assert.equal(getDisplayGroupRatio(model, 'GenAI'), 0.8)
  assert.equal(getDisplayGroupRatio(model, 'UnifyAI'), 0.8)
  assert.equal(getDisplayGroupRatio(model), 0.8)
})
