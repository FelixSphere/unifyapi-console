/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { afterAll, beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { Window } from 'happy-dom'

const domWindow = new Window()
// Expose the whole happy-dom window as the global environment: the card's
// dependency tree reaches for customElements, matchMedia, HTMLElement
// subclasses and more, and enumerating them by hand breaks on every new
// import. Anything happy-dom lacks gets an inert stand-in below.
{
  const seen = new Set<string>(['undefined', 'NaN', 'Infinity', 'globalThis'])
  let proto: object | null = domWindow
  while (proto && proto !== Object.prototype) {
    for (const key of Object.getOwnPropertyNames(proto)) {
      if (seen.has(key) || key in globalThis) continue
      seen.add(key)
      try {
        const value = (domWindow as unknown as Record<string, unknown>)[key]
        Object.defineProperty(globalThis, key, { configurable: true, value })
      } catch {
        // accessor that throws off-window; skip
      }
    }
    proto = Object.getPrototypeOf(proto)
  }
  for (const key of ['window', 'document', 'navigator'] as const) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: (domWindow as unknown as Record<string, unknown>)[key],
    })
  }
}
const inertMatchMedia = () => ({
  matches: false,
  media: '',
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: inertMatchMedia,
})
Object.defineProperty(domWindow, 'matchMedia', {
  configurable: true,
  value: inertMatchMedia,
})
if (!('ResizeObserver' in globalThis)) {
  Object.defineProperty(globalThis, 'ResizeObserver', {
    configurable: true,
    value: class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  })
}

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
;(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true

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
    model_ratio: 1.25,
    completion_ratio: 4,
    enable_groups: ['GenAI'],
    group_ratio: { GenAI: 1 },
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

// Model Square is the public catalogue. The badge advertises what the
// `default` group pays; it must be loud when present, absent when not, and
// never depend on the viewer.
describe('ModelCard new-user discount', () => {
  afterAll(() => domWindow.close())

  test('a 0.9 default ratio shows a 10% badge and the new-user price beside the intact list price', async () => {
    const r = await render(model({ default_group_model_ratio: 0.9 }))
    assert.ok(r.badge, 'badge rendered')
    assert.match(r.badge!.textContent ?? '', /10% off by default/)
    assert.equal(
      r.newUserPrices.length,
      2,
      'input and output carry a new-user price'
    )
    assert.ok(r.text.includes('$2.25'), 'new-user input price $2.25 is shown')
    assert.ok(r.text.includes('$9'), 'new-user output price $9 is shown')
    assert.equal(
      r.struck.length,
      0,
      'the list price is the baseline and must never be struck through'
    )
    assert.equal(
      r.listPrices.length,
      2,
      'the list price stays beside the new-user price for input and output'
    )
    assert.ok(
      r.text.includes('$2.5'),
      'the published list price is still on the card, intact'
    )
    await r.cleanup()
  })

  test('no default ratio: no badge, no new-user price, list price as before', async () => {
    const r = await render(model())
    assert.equal(r.badge, null)
    assert.equal(r.newUserPrices.length, 0)
    assert.equal(r.struck.length, 0)
    assert.ok(r.text.includes('$2.5'))
    await r.cleanup()
  })

  test("the viewer's own contract does not change what the card advertises", async () => {
    const plain = await render(model({ default_group_model_ratio: 0.9 }))
    const contracted = await render(
      model({ default_group_model_ratio: 0.9, customer_group_model_ratio: 0.5 })
    )
    assert.equal(contracted.badge?.textContent, plain.badge?.textContent)
    assert.equal(
      contracted.text,
      plain.text,
      'identical card for a contracted viewer'
    )
    await plain.cleanup()
    await contracted.cleanup()
  })

  // Production's actual shape, and the one the fixture above does not cover:
  // an anonymous visitor sees only `default`, and the default group's ratio is
  // the same 0.85 as the per-model override. The reference price used to be
  // "the cheapest group you can see", which IS 0.85 here, so both figures came
  // out $2.55, the card collapsed to one number, and the badge advertised 15%
  // off a price that appeared nowhere. Live on 49 models on 2026-09-28.
  test('a visitor who can only see `default` still gets the official list price beside the discount', async () => {
    const r = await render(
      model({
        model_ratio: 1.5,
        completion_ratio: 4,
        enable_groups: ['default'],
        group_ratio: { default: 0.85 },
        default_group_model_ratio: 0.85,
      })
    )
    assert.ok(r.badge, 'badge rendered')
    assert.match(r.badge!.textContent ?? '', /15% off by default/)
    assert.equal(
      r.newUserPrices.length,
      2,
      'input and output carry a new-user price'
    )
    assert.equal(
      r.listPrices.length,
      2,
      'the badge is a percentage off something, so that something must be shown'
    )
    assert.ok(r.text.includes('$2.55'), 'the new-user input price is shown')
    assert.ok(
      r.text.includes('$3'),
      'the OFFICIAL list input price is the reference, not the default-group price'
    )
    assert.equal(r.struck.length, 0, 'the list price is never struck through')
    await r.cleanup()
  })

  // A model on a channel that the catalog does not price. /api/pricing used to
  // publish GetModelRatio's 37.5 sentinel for these -- $75 per 1M, ~250x a real
  // model, for something the relay refuses. It now sends `unpriced` and no
  // number, and the card must show a dash rather than compute one from the 0.
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

  // Math.round turns a 0.999 ratio into "0% off by default". A badge that
  // claims nothing is worse than no badge.
  test('a discount too small to state as one percent shows no badge', async () => {
    const r = await render(model({ default_group_model_ratio: 0.999 }))
    assert.equal(r.badge, null)
    assert.equal(r.newUserPrices.length, 0)
    await r.cleanup()
  })
})
