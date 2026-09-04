/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
/*
UNIFYAPI-FORK: tests for the profit view's logic.

This screen answers "am I making money, and how". Two classes of bug matter:
showing a healthy margin on a line that is losing money, and showing a confident
margin when there is no cost basis to compute one from. Both are here.
*/
import assert from 'node:assert/strict'

import type { ReconcileLine } from '../../types'
import {
  PERIOD_PRESETS,
  THIN_MARGIN_PCT,
  cacheHitRate,
  deriveMargin,
  formatPct,
  formatTokens,
  formatUSD,
  marginHealth,
  resolvePeriod,
  toISODate,
} from '../profit-logic'

function line(over: Partial<ReconcileLine> = {}): ReconcileLine {
  return {
    key: 'gpt-4o',
    label: 'gpt-4o',
    requests: 10,
    prompt_tokens: 1_000_000,
    cached_tokens: 0,
    completion_tokens: 100_000,
    revenue_usd: 10,
    cost_usd: 5,
    margin_usd: 5,
    margin_pct: 50,
    unpriced_requests: 0,
    ...over,
  }
}

describe('toISODate', () => {
  test('formats in local time, not UTC', () => {
    // 23:30 local on the 15th. toISOString() would roll this to the 16th
    // anywhere east of Greenwich, producing a window with no data in it.
    const d = new Date(2026, 7, 15, 23, 30, 0)
    assert.equal(toISODate(d), '2026-08-15')
  })

  test('zero-pads month and day', () => {
    assert.equal(toISODate(new Date(2026, 0, 5)), '2026-01-05')
  })
})

/** presetById fails loudly if a preset is renamed, rather than passing undefined
 *  into resolvePeriod and asserting against whatever falls out. */
function presetById(id: string) {
  const preset = PERIOD_PRESETS.find((p) => p.id === id)
  assert.ok(preset, `no period preset with id ${id}`)
  return preset
}

describe('resolvePeriod', () => {
  const today = new Date(2026, 7, 30) // 2026-08-30

  test('today is a single-day window', () => {
    const p = presetById('today')
    assert.deepEqual(resolvePeriod(p, today), {
      start: '2026-08-30',
      end: '2026-08-30',
    })
  })

  test('last 7 days is inclusive of both ends', () => {
    // 24th..30th is seven days, not eight and not six.
    const p = presetById('7d')
    assert.deepEqual(resolvePeriod(p, today), {
      start: '2026-08-24',
      end: '2026-08-30',
    })
  })

  test('month to date starts at the first', () => {
    const p = presetById('mtd')
    assert.deepEqual(resolvePeriod(p, today), {
      start: '2026-08-01',
      end: '2026-08-30',
    })
  })

  test('a window crossing a month boundary walks back correctly', () => {
    const p = presetById('7d')
    assert.deepEqual(resolvePeriod(p, new Date(2026, 8, 3)), {
      start: '2026-08-28',
      end: '2026-09-03',
    })
  })
})

describe('marginHealth', () => {
  test('a negative margin is a loss', () => {
    assert.equal(
      marginHealth(line({ margin_usd: -1, margin_pct: -20 }), true),
      'loss'
    )
  })

  test('below the floor is thin, at the floor is healthy', () => {
    assert.equal(
      marginHealth(line({ margin_pct: THIN_MARGIN_PCT - 0.1 }), true),
      'thin'
    )
    assert.equal(
      marginHealth(line({ margin_pct: THIN_MARGIN_PCT }), true),
      'healthy'
    )
  })

  test('the floor matches the server alert rule', () => {
    // service/reconcile_alerts.go uses 10; if the two drift, the screen and the
    // nightly alert disagree about what counts as thin.
    assert.equal(THIN_MARGIN_PCT, 10)
  })

  test('without a cost basis every line is unmeasured, never a loss', () => {
    // Costed at list price, margin is zero or negative by construction. Painting
    // the whole table red would be precise and useless.
    assert.equal(
      marginHealth(line({ margin_usd: -3, margin_pct: -30 }), false),
      'unmeasured'
    )
    assert.equal(marginHealth(line(), false), 'unmeasured')
  })
})

describe('deriveMargin', () => {
  test('states revenue as read and cost as modelled, then the difference', () => {
    const steps = deriveMargin(
      line({ revenue_usd: 10, cost_usd: 4, margin_usd: 6 })
    )
    const [billed, cost, margin] = steps
    assert.equal(billed.amountUSD, 10)
    assert.ok(billed.noteKey, 'the ledger step must explain itself')
    assert.match(billed.noteKey, /quota actually deducted/)
    assert.equal(
      cost.amountUSD,
      -4,
      'cost is shown as a subtraction, not a bare number'
    )
    assert.ok(cost.noteKey)
    assert.match(cost.noteKey, /official price/)
    assert.equal(margin.amountUSD, 6)
    assert.equal(margin.emphasis, true)
  })

  test('the steps add up to the margin', () => {
    const l = line({ revenue_usd: 12.5, cost_usd: 4.25, margin_usd: 8.25 })
    const [billed, cost, margin] = deriveMargin(l)
    const sum = (billed.amountUSD ?? 0) + (cost.amountUSD ?? 0)
    assert.ok(Math.abs(sum - (margin.amountUSD ?? 0)) < 1e-9)
  })

  test('uncostable traffic is called out as overstating the margin', () => {
    const steps = deriveMargin(
      line({ unpriced_requests: 4, unpriced_models: ['glm-5.3'] })
    )
    const note = steps.at(-1)
    assert.ok(note, 'an uncostable line must add a caveat step')
    // The message is a key with interpolation, so the model names live in
    // noteParams -- a template that baked them into the string could not be
    // translated.
    assert.ok(note.noteKey)
    assert.match(note.noteKey, /overstated/)
    assert.deepEqual(note.noteParams, { count: 4, models: 'glm-5.3' })
  })

  test('a fully costed line has no such caveat', () => {
    assert.equal(deriveMargin(line()).length, 3)
  })
})

describe('formatUSD', () => {
  test('keeps small amounts distinguishable', () => {
    assert.equal(formatUSD(0.0064), '$0.0064')
    assert.equal(formatUSD(0.425), '$0.425')
    assert.equal(formatUSD(12.5), '$12.50')
  })

  test('renders a loss with its sign', () => {
    assert.equal(formatUSD(-5.22), '-$5.22')
    assert.equal(formatUSD(-0.0017), '-$0.0017')
  })

  test('zero is not a dash — a real zero is information', () => {
    assert.equal(formatUSD(0), '$0.00')
  })
})

describe('formatPct', () => {
  test('undefined without revenue', () => {
    // A line with cost and no revenue has no meaningful margin percentage;
    // -100% and Infinity would both be lies.
    assert.equal(formatPct(line({ revenue_usd: 0, margin_pct: 0 })), '—')
  })

  test('one decimal place', () => {
    assert.equal(formatPct(line({ margin_pct: 19.72 })), '19.7%')
    assert.equal(formatPct(line({ margin_pct: -511.76 })), '-511.8%')
  })
})

describe('formatTokens', () => {
  test('abbreviates so columns stay scannable', () => {
    assert.equal(formatTokens(950), '950')
    assert.equal(formatTokens(1_500), '1.5K')
    assert.equal(formatTokens(2_400_000), '2.40M')
    assert.equal(formatTokens(3_100_000_000), '3.10B')
  })
})

describe('cacheHitRate', () => {
  test('is the cached share of prompt tokens', () => {
    assert.equal(
      cacheHitRate(line({ prompt_tokens: 1000, cached_tokens: 800 })),
      0.8
    )
  })

  test('is undefined with no prompt tokens rather than dividing by zero', () => {
    assert.equal(
      cacheHitRate(line({ prompt_tokens: 0, cached_tokens: 0 })),
      null
    )
  })
})
