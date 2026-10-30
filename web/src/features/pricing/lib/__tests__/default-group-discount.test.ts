/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { afterAll, beforeEach, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import type { PricingModel } from '../../types'
import {
  getDefaultGroupDiscount,
  getMaxDefaultGroupPercentOff,
} from '../model-helpers'
import { formatDefaultGroupPrice, formatPrice } from '../price'

/*
Model Square advertises the price a brand-new user pays as "xx% off by
default". The number comes from ONE public field, default_group_model_ratio,
which the server sets identically for every viewer. These tests pin the
client half of that contract:

  - the badge exists only for a genuine discount (0 < ratio < 1);
  - the new-user price is the published price times that ratio, through the
    same formatting pipeline, so the two numbers on the card agree;
  - the viewer's own contract (customer_group_model_ratio) plays no part.
*/

// formatCurrencyFromUSD reads the admin currency display from the system
// config store. Another test file switches it to TOKENS and leaves it there,
// so pin USD here and restore whatever was set afterwards -- the assertions
// below are about prices, not about which display mode ran before us.
const priorConfig = useSystemConfigStore.getState().config
beforeEach(() => {
  const state = useSystemConfigStore.getState()
  useSystemConfigStore.setState({
    config: { ...state.config, currency: { ...DEFAULT_CURRENCY_CONFIG } },
  })
})
afterAll(() => {
  useSystemConfigStore.setState({ config: priorConfig })
})

const model = (over: Partial<PricingModel> = {}): PricingModel =>
  ({
    id: 1,
    model_name: 'gpt-4o',
    quota_type: 0,
    model_ratio: 1.25, // $2.50 / 1M input
    completion_ratio: 4, // $10 / 1M output
    enable_groups: ['GenAI', 'UnifyAI'],
    group_ratio: { GenAI: 1, UnifyAI: 1 },
    ...over,
  }) as PricingModel

test('a 0.9 default multiplier is a 10% badge; anything else is no badge', () => {
  assert.deepEqual(
    getDefaultGroupDiscount(model({ default_group_model_ratio: 0.9 })),
    {
      ratio: 0.9,
      percentOff: 10,
    }
  )
  assert.deepEqual(
    getDefaultGroupDiscount(model({ default_group_model_ratio: 0.85 })),
    {
      ratio: 0.85,
      percentOff: 15,
    }
  )
  for (const ratio of [undefined, 1, 1.2, 0, -0.5, Number.NaN]) {
    assert.equal(
      getDefaultGroupDiscount(model({ default_group_model_ratio: ratio })),
      null,
      `ratio ${String(ratio)} must not produce a badge`
    )
  }
})

test('the new-user price is the published price times the default ratio, same pipeline', () => {
  const m = model({ default_group_model_ratio: 0.9 })
  // Published: $2.50 in, $10 out per 1M. New user: $2.25 in, $9 out.
  assert.equal(formatPrice(m, 'input', 'M'), '$2.5')
  assert.equal(formatDefaultGroupPrice(m, 'input', 'M'), '$2.25')
  assert.equal(formatPrice(m, 'output', 'M'), '$10')
  assert.equal(formatDefaultGroupPrice(m, 'output', 'M'), '$9')
  // Per-1K follows the same divisor.
  assert.equal(formatDefaultGroupPrice(m, 'input', 'K'), '$0.00225')
})

test('no default ratio means no new-user price, and the published price is untouched', () => {
  const m = model()
  assert.equal(formatDefaultGroupPrice(m, 'input', 'M'), null)
  assert.equal(formatPrice(m, 'input', 'M'), '$2.5')
})

test('per-request models never get a token new-user price', () => {
  const m = model({
    quota_type: 1,
    model_price: 0.1,
    default_group_model_ratio: 0.9,
  })
  assert.equal(formatDefaultGroupPrice(m, 'input', 'M'), null)
})

test("the viewer's own contract does not change the advertised new-user price", () => {
  const anonymous = model({ default_group_model_ratio: 0.9 })
  const contracted = model({
    default_group_model_ratio: 0.9,
    customer_group_model_ratio: 0.5, // this viewer's negotiated price
  })
  assert.equal(
    formatDefaultGroupPrice(contracted, 'input', 'M'),
    formatDefaultGroupPrice(anonymous, 'input', 'M'),
    'the badge is about the default group, not about whoever is looking'
  )
  assert.equal(
    formatPrice(contracted, 'input', 'M'),
    formatPrice(anonymous, 'input', 'M')
  )
})

test('the headline shows the largest discount across the catalogue, or nothing', () => {
  assert.equal(
    getMaxDefaultGroupPercentOff([
      model({ default_group_model_ratio: 0.9 }),
      model({ model_name: 'b', default_group_model_ratio: 0.8 }),
      model({ model_name: 'c' }),
    ]),
    20
  )
  assert.equal(
    getMaxDefaultGroupPercentOff([model(), model({ model_name: 'b' })]),
    0
  )
  assert.equal(getMaxDefaultGroupPercentOff([]), 0)
})
