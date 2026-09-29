/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { afterAll, beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { domWindow } from './dom-env'

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ModelCard } = await import('../model-card')
const { DEFAULT_CURRENCY_CONFIG, useSystemConfigStore } =
  await import('@/stores/system-config-store')
type PricingModel = import('../../types').PricingModel

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        '{{percent}}% off by default': '{{percent}}% off by default',
        Input: 'Input',
        Output: 'Output',
        Cached: 'Cached',
        Details: 'Details',
      },
    },
  },
})

// formatCurrencyFromUSD reads the admin currency display from a global store
// that other test files mutate and leave mutated. Pin it per test.
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
    model_name: 'claude-opus-5',
    quota_type: 0,
    model_ratio: 1.5,
    completion_ratio: 4,
    enable_groups: ['default'],
    group_ratio: { default: 1 },
    ...over,
  }) as PricingModel

async function render(m: PricingModel) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <ModelCard model={m} onClick={() => {}} />
      </I18nextProvider>
    )
  })
  return {
    container,
    text: container.textContent ?? '',
    badge: container.querySelector('[data-default-discount-badge]'),
    newUserPrices: container.querySelectorAll('[data-new-user-price]'),
    struck: container.querySelectorAll('.line-through'),
    listPrices: container.querySelectorAll('[data-list-price]'),
    cleanup: async () => {
      await act(async () => root.unmount())
      container.remove()
    },
  }
}

// The badge says "N% off by default". N is a percentage OFF SOMETHING, so the
// something has to be on the card, and it has to be official list.
describe('ModelCard discount reference price', () => {
  afterAll(() => domWindow.close())

  // Production's shape, and the one the older card test does not cover: an
  // anonymous visitor sees only `default`, and that group's ratio is the same
  // 0.85 as the per-model override. The reference used to come from
  // formatPrice, which prices at the cheapest group the viewer can see -- which
  // IS 0.85 here. Both figures came out $2.55, the card collapsed to one
  // number, and the badge advertised 15% off a price shown nowhere. Live on 49
  // models until 2026-09-28.
  test('a visitor who can only see `default` still gets the official list price', async () => {
    const r = await render(
      model({
        group_ratio: { default: 0.85 },
        default_group_model_ratio: 0.85,
      })
    )
    assert.ok(r.badge, 'badge rendered')
    assert.match(r.badge!.textContent ?? '', /15% off by default/)
    assert.equal(r.newUserPrices.length, 2, 'input and output carry one')
    assert.equal(
      r.listPrices.length,
      2,
      'the badge is a percentage off something, so that something must be shown'
    )
    assert.ok(r.text.includes('$2.55'), 'the new-user input price')
    assert.ok(
      r.text.includes('$3'),
      'the OFFICIAL list input price is the reference, not the default-group price'
    )
    assert.equal(r.struck.length, 0, 'list is never struck through')
    await r.cleanup()
  })

  // A model on a channel that the catalog does not price. /api/pricing used to
  // publish GetModelRatio's 37.5 sentinel for these -- $75 per 1M, ~250x a real
  // model, for something the relay refuses. It now sends `unpriced` and no
  // number, and the card must not turn the resulting 0 into "free".
  test('an unpriced model shows a dash, not a fabricated price', async () => {
    const r = await render(
      model({ unpriced: true, model_ratio: 0, completion_ratio: 0 })
    )
    assert.equal(r.badge, null, 'nothing to discount')
    assert.equal(r.newUserPrices.length, 0)
    assert.ok(r.text.includes('-'), 'the price slot renders a dash')
    assert.ok(!r.text.includes('$0'), 'an unpriced model must not read as free')
    assert.ok(!r.text.includes('$75'), 'and must never read as the sentinel')
    await r.cleanup()
  })

  // A stale GroupModelDiscount row can name an unpriced model -- production
  // carries six such rows today. Badge and price have to agree.
  test('an unpriced model shows no badge even when a discount names it', async () => {
    const r = await render(
      model({ unpriced: true, model_ratio: 0, default_group_model_ratio: 0.85 })
    )
    assert.equal(r.badge, null)
    await r.cleanup()
  })

  // Math.round turns a 0.999 ratio into "0% off by default". A badge that
  // claims nothing is worse than no badge.
  test('a discount too small to state as one percent shows no badge', async () => {
    const r = await render(model({ default_group_model_ratio: 0.999 }))
    assert.equal(r.badge, null)
    assert.equal(r.newUserPrices.length, 0)
    await r.cleanup()
  })
})
