/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/*
OPERATOR RULE, stated as absolute: the discount the bill uses and the discount
the UI shows must be the same number.

This file pins the arithmetic behind it. The per-model `default` override
REPLACES the group ratio -- HandleGroupRatio assigns it in
relay/helper/price.go, and the fork comment there explains why multiplying
would make a negotiated rate unanswerable. The card multiplied instead, and
the numbers matched the bug exactly: with the default group at 0.85 and a 0.85
override, claude-sonnet-4-5 ($3 list) was advertised at $2.1675 while the relay
billed $2.55 -- a published price 15% under what we charge, on every model
carrying a discount badge.
*/

import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import type { PricingModel } from '../../types'
import { formatDefaultGroupPrice, formatPrice } from '../price'

// These assertions are about ARITHMETIC, not about which currency the admin
// display happens to be set to. formatCurrencyFromUSD reads that from a global
// store another test file mutates and does not restore, so pinning the store
// here would be a race with test ordering. Reading the number back out is
// immune to it, and keeps a failure pointing at the price rather than at a
// currency symbol.
const amount = (rendered: string | null): number | null =>
  rendered === null ? null : Number(rendered.replace(/[^0-9.]/g, ''))

// $3 in / $15 out, visible only to `default` -- the shape an anonymous visitor
// sees after the customer-group disclosure fix.
const model = (defaultRatio?: number, groupRatio = 0.85) =>
  ({
    id: 1,
    model_name: 'claude-sonnet-4-5',
    quota_type: 0,
    model_ratio: 1.5,
    completion_ratio: 5,
    enable_groups: ['default'],
    group_ratio: { default: groupRatio },
    ...(defaultRatio === undefined
      ? {}
      : { default_group_model_ratio: defaultRatio }),
  }) as unknown as PricingModel

describe('the advertised new-user price is the billed one', () => {
  test('a per-model override replaces the group ratio rather than stacking', () => {
    // Billed: $3 x 0.85 = $2.55. The bug rendered $3 x 0.85 x 0.85 = $2.1675.
    assert.equal(
      amount(formatDefaultGroupPrice(model(0.85), 'input', 'M')),
      2.55
    )
    assert.equal(
      amount(formatDefaultGroupPrice(model(0.85), 'output', 'M')),
      12.75
    )
  })

  test('a deeper override is still a multiple of LIST, not of the group price', () => {
    assert.equal(amount(formatDefaultGroupPrice(model(0.5), 'input', 'M')), 1.5)
    assert.equal(
      amount(formatDefaultGroupPrice(model(0.5, 0.6), 'input', 'M')),
      1.5,
      'the group ratio must not move the new-user price at all'
    )
  })

  test('the group ratio never leaks into the advertised figure', () => {
    for (const groupRatio of [1, 0.9, 0.85, 0.6]) {
      assert.equal(
        amount(formatDefaultGroupPrice(model(0.85, groupRatio), 'input', 'M')),
        2.55,
        `group ratio ${groupRatio} leaked into the new-user price`
      )
    }
  })

  test('no override means there is no new-user price to advertise', () => {
    assert.equal(formatDefaultGroupPrice(model(undefined), 'input', 'M'), null)
  })

  // An override of exactly 1 is the operator saying "this model is at list".
  // getDefaultGroupDiscount refuses to call that a discount, so nothing is
  // advertised -- the card must not then quote a group-discounted figure as
  // though it were what a new user pays.
  test('an override of exactly 1 advertises nothing', () => {
    assert.equal(formatDefaultGroupPrice(model(1), 'input', 'M'), null)
  })

  // formatPrice is the other number on the card and is deliberately left
  // alone: it still shows the best price across the groups the viewer can see,
  // which is what its own tests pin.
  test('the list-side figure keeps its existing meaning', () => {
    assert.equal(amount(formatPrice(model(0.85), 'input', 'M')), 2.55)
  })
})
