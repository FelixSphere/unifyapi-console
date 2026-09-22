/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// The payout dialog renders the rails the SERVER sends, and the one thing it
// must never do is quietly move a seller off the account they are being paid
// on. That only shows up when the component is actually rendered: a string
// test would read the same either way.

import { afterAll, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'HTMLSelectElement',
  'HTMLOptionElement',
  'HTMLTextAreaElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'DOMRect',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  if (key in domWindow) {
    // Per key and guarded: on Linux CI some of these are already defined as
    // read-only, and a bulk Object.assign throws there while passing on macOS.
    try {
      Object.defineProperty(globalThis, key, {
        configurable: true,
        writable: true,
        value: (domWindow as unknown as Record<string, unknown>)[key],
      })
    } catch {
      /* already provided by the runtime */
    }
  }
}
for (const [key, value] of [
  [
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  ],
  [
    'matchMedia',
    () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
  ],
] as const) {
  if (!(key in globalThis)) {
    Object.defineProperty(globalThis, key, { configurable: true, value })
  }
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { PayoutAccountDialog } = await import('../payout-account-dialog')
const { splitOnChain } = await import('../../lib/payout')

const i18n = createInstance()
await i18n
  .use(initReactI18next)
  .init({ lng: 'en', resources: { en: { translation: {} } } })

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

// What the server sends when Binance.US has been switched off in Payment
// Settings since this seller filed their account.
const RAILS = [
  {
    id: 'platform_credit',
    label: 'Platform credit (added to your UnifyAPI balance)',
    needs_account: false,
    available: true,
  },
  {
    id: 'bank_transfer',
    label: 'Bank transfer',
    hint: 'Bank name, IBAN or account number, SWIFT/BIC.',
    needs_account: true,
    available: true,
  },
  {
    id: 'stripe',
    label: 'Stripe',
    needs_account: true,
    available: false,
    unavailable_reason:
      'Stripe takes payments in, it cannot send them out. Paying you through Stripe needs Stripe Connect, which this platform is not set up for.',
  },
  {
    id: 'binance_pay_us',
    label: 'Binance.US',
    needs_account: true,
    networks: ['TRX', 'BSC'],
    currency: 'USDT',
    available: false,
    unavailable_reason:
      'Binance.US is not configured in Payment Settings, so there is no account to send from.',
  },
]

const roots: Array<{ unmount: () => void }> = []

function render(
  current: {
    method: string
    holder: string
    details: string
    currency: string
  } | null
) {
  // The dialog renders into a portal on document.body, so a leftover render
  // from the previous test would be found first by every query below.
  while (roots.length > 0) act(() => roots.pop()?.unmount())
  domWindow.document.body.innerHTML = ''
  const container = domWindow.document.createElement(
    'div'
  ) as unknown as HTMLElement
  domWindow.document.body.appendChild(container as unknown as never)
  const root = createRoot(container)
  roots.push(root)
  act(() => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={new QueryClient()}>
          <PayoutAccountDialog
            open
            onOpenChange={() => {}}
            current={current}
            rails={RAILS}
          />
        </QueryClientProvider>
      </I18nextProvider>
    )
  })
  return container
}

// The dialog renders into a portal, so the whole document is the haystack.
function select(testId: string): HTMLSelectElement {
  const element = domWindow.document.querySelector(
    `[data-testid="${testId}"]`
  ) as unknown as HTMLSelectElement | null
  assert.ok(element, `no element with data-testid="${testId}"`)
  return element
}

function options(testId: string) {
  return [...select(testId).querySelectorAll('option')].map((option) => ({
    value: (option as unknown as HTMLOptionElement).value,
    disabled: (option as unknown as HTMLOptionElement).disabled,
  }))
}

afterAll(() => {
  for (const root of roots) act(() => root.unmount())
})

describe('payout account dialog', () => {
  test('a seller already on a switched-off rail stays on it', () => {
    render({
      method: 'binance_pay_us',
      holder: 'Acme Ltd',
      details: 'TRX:TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb',
      currency: 'USDT',
    })
    // Not reset to the first available rail: that would look harmless on
    // screen and throw the account away on the next save.
    assert.equal(select('payout-method').value, 'binance_pay_us')
    const byId = Object.fromEntries(
      options('payout-method').map((option) => [option.value, option])
    )
    assert.equal(
      byId.binance_pay_us?.disabled,
      false,
      'their own rail is selectable'
    )
    assert.equal(byId.stripe?.disabled, true, 'a rail they are not on is not')
    // And the stored value is split back into the two fields, not shown raw.
    assert.equal(select('payout-network').value, 'TRX')
    assert.equal(
      (
        domWindow.document.querySelector(
          '[data-testid="payout-address"]'
        ) as unknown as HTMLInputElement
      ).value,
      'TQn9Y2khDD95J42FQtQTdwVVRZqqArtzBb'
    )
  })

  test('a seller with nothing on file starts on an available rail', () => {
    render(null)
    assert.equal(select('payout-method').value, 'platform_credit')
    // Platform credit asks for nothing, so there is no address field at all.
    assert.equal(
      domWindow.document.querySelectorAll('[data-testid="payout-address"]')
        .length,
      0
    )
  })

  test("Stripe's absence is explained rather than hidden", () => {
    render({ method: 'stripe', holder: '', details: '', currency: 'USD' })
    assert.match(
      domWindow.document.body.textContent ?? '',
      /Stripe Connect/,
      'the reason is on screen, not just in a disabled option'
    )
  })

  test('splitOnChain matches what the server stores', () => {
    assert.deepEqual(splitOnChain('TRX:abc'), {
      network: 'TRX',
      address: 'abc',
    })
    assert.deepEqual(splitOnChain(' trx : abc '), {
      network: 'TRX',
      address: 'abc',
    })
    // A bank account is free text and has no network to read off it.
    assert.deepEqual(splitOnChain('IBAN GB33BUKB20201555555555'), {
      network: '',
      address: 'IBAN GB33BUKB20201555555555',
    })
  })
})
