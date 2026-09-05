/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: tests for the extra-models form.

This form replaced one whose save discarded the entire code catalog, so the
rules here are the difference between the two. The one that matters most is that
a catalogued model cannot be entered — that is what keeps this table additive
rather than another way to shadow a vetted price.
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import type { ExtraModelDraft } from '../../types'
import {
  MAX_PRICE_USD,
  USD_PER_MILLION_PER_RATIO_UNIT,
  completionRatioFromUSD,
  draftToPayload,
  emptyDraft,
  formatUSDPrice,
  ratioFromUSD,
  validateDraft,
} from '../extra-models-logic'

function draft(over: Partial<ExtraModelDraft> = {}): ExtraModelDraft {
  return {
    ...emptyDraft(),
    model: 'new-model',
    input_usd: '1.4',
    output_usd: '4.4',
    ...over,
  }
}

const CATALOGUED = ['claude-opus-4-8', 'gpt-4o']

function fieldsWithErrors(d: ExtraModelDraft, existing: string[] = []) {
  return validateDraft(d, CATALOGUED, existing).map((e) => e.field)
}

describe('validateDraft', () => {
  test('a well-formed new model passes', () => {
    assert.deepEqual(validateDraft(draft(), CATALOGUED, []), [])
  })

  test('a catalogued model is refused, and says where to go instead', () => {
    // The rule that keeps this table from becoming the raw-ratio editor.
    const errors = validateDraft(draft({ model: 'gpt-4o' }), CATALOGUED, [])
    assert.equal(errors.length, 1)
    assert.equal(errors[0].field, 'model')
    assert.match(errors[0].message, /官方报价与折扣/)
  })

  test('a duplicate in this table is refused', () => {
    assert.deepEqual(fieldsWithErrors(draft({ model: 'dup' }), ['dup']), [
      'model',
    ])
  })

  test('an untrimmed name is refused rather than silently trimmed', () => {
    // Trimming for the user would create a model whose name does not match what
    // they typed, and API calls match exactly.
    assert.deepEqual(fieldsWithErrors(draft({ model: 'spaced ' })), ['model'])
  })

  test('prices must be positive', () => {
    assert.deepEqual(fieldsWithErrors(draft({ input_usd: '0' })), ['input_usd'])
    assert.deepEqual(fieldsWithErrors(draft({ output_usd: '-1' })), [
      'output_usd',
    ])
    assert.deepEqual(fieldsWithErrors(draft({ input_usd: 'abc' })), [
      'input_usd',
    ])
  })

  test('an absurd price is caught as a misplaced decimal', () => {
    const errors = validateDraft(draft({ input_usd: '5000' }), CATALOGUED, [])
    assert.equal(errors[0].field, 'input_usd')
    assert.match(errors[0].message, /decimal/)
  })

  test('the ceiling matches the server', () => {
    // maxExtraPriceUSD in setting/ratio_setting/unifyapi_extra_models.go.
    assert.equal(MAX_PRICE_USD, 1000)
  })

  test('cache read above input is caught — it is backwards, and it hides', () => {
    // Wrong this way round it overstates cost in reconciliation rather than
    // undercharging, so nothing complains and the margin quietly reads low.
    const errors = validateDraft(draft({ cache_read_usd: '9' }), CATALOGUED, [])
    assert.equal(errors[0].field, 'cache_read_usd')
    assert.match(errors[0].message, /wrong way round/)
  })

  test('an empty cache field is allowed — unset is not zero', () => {
    assert.deepEqual(
      validateDraft(draft({ cache_read_usd: '' }), CATALOGUED, []),
      []
    )
  })

  test('every bad field is reported, not just the first', () => {
    assert.deepEqual(
      fieldsWithErrors(draft({ input_usd: '0', output_usd: '0' })).sort(),
      ['input_usd', 'output_usd']
    )
  })
})

describe('draftToPayload', () => {
  test('sends the prices as numbers', () => {
    const payload = draftToPayload(draft())
    assert.equal(payload.input_usd, 1.4)
    assert.equal(payload.output_usd, 4.4)
  })

  test('omits blank optional fields rather than sending zero', () => {
    // A zero cache price means "cached reads are free", which is a real and
    // very different claim from "the vendor publishes no cache price".
    const payload = draftToPayload(draft())
    assert.equal('cache_read_usd' in payload, false)
    assert.equal('vendor' in payload, false)
    assert.equal('note' in payload, false)
  })

  test('includes optional fields when given', () => {
    const payload = draftToPayload(
      draft({ cache_read_usd: '0.26', note: 'from the vendor page' })
    )
    assert.equal(payload.cache_read_usd, 0.26)
    assert.equal(payload.note, 'from the vendor page')
  })

  test('trims free text', () => {
    assert.equal(
      draftToPayload(draft({ vendor: '  zhipuai ' })).vendor,
      'zhipuai'
    )
  })
})

describe('ratio derivation', () => {
  test('the convention matches the catalog', () => {
    // usdPerMillionPerRatioUnit in setting/ratio_setting/unifyapi_baseline.go.
    assert.equal(USD_PER_MILLION_PER_RATIO_UNIT, 2)
    assert.equal(ratioFromUSD(1.4), 0.7)
    assert.equal(ratioFromUSD(2), 1)
  })

  test('the completion ratio is output over input', () => {
    assert.equal(completionRatioFromUSD(1.4, 4.4), 4.4 / 1.4)
    assert.equal(
      completionRatioFromUSD(0, 5),
      1,
      'no input price means no multiplier, not Infinity'
    )
  })
})

describe('formatUSDPrice', () => {
  test('keeps cheap models distinguishable', () => {
    // Several catalogued models cost cents per 1M; rounding to two decimals
    // would render them all as $0.00.
    assert.equal(formatUSDPrice(0.0028), '$0.0028')
    assert.equal(formatUSDPrice(0.26), '$0.260')
    assert.equal(formatUSDPrice(4.4), '$4.40')
    assert.equal(formatUSDPrice(0), '$0')
  })
})
