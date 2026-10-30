/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
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
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  if (key in domWindow) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: (domWindow as unknown as Record<string, unknown>)[key],
    })
  }
}
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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { RatioSettingsCard } = await import('../ratio-settings-card')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: { 'Add group': 'Add group' } } },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function changeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

// The five customer groups production has today, exactly as the options API
// returns them. `default` is deliberately absent: that is the row the operator
// was trying to add.
const savedGroupRatio = JSON.stringify({
  Builder_hub_2026: 1,
  Chinhin: 1,
  GenAI: 1,
  UnifyAI: 1,
  'Vip User': 1,
})

// section-registry.tsx builds these props with `getGroupDefaults(settings)` on
// every render -- a fresh object each time, even when nothing changed. This
// helper reproduces that exactly.
const freshGroupDefaults = () => ({
  GroupRatio: savedGroupRatioOverride ?? savedGroupRatio,
  TopupGroupRatio: '{}',
  UserUsableGroups: JSON.stringify({
    Builder_hub_2026: '',
    Chinhin: '',
    GenAI: '',
    UnifyAI: '',
    'Vip User': '',
  }),
  GroupGroupRatio: '{}',
  AutoGroups: '[]',
  MaxTokenAutoGroups: 2,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
})

const freshModelDefaults = () => ({
  ModelPrice: '{}',
  ModelRatio: '{}',
  CacheRatio: '{}',
  CreateCacheRatio: '{}',
  CompletionRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  ExposeRatioEnabled: false,
  BillingMode: '{}',
  BillingExpr: '{}',
})

let rerenderParent: (() => void) | null = null
let savedGroupRatioOverride: string | null = null

// Parent stands in for settings-page + section-registry: it re-renders for
// reasons unrelated to this card (an options refetch after any save on the
// page, window focus once the 5-minute staleTime lapses) and, like the
// registry, hands the card brand-new defaults objects every time.
function Parent() {
  const [, setTick] = useState(0)
  rerenderParent = () => setTick((n) => n + 1)
  return (
    <RatioSettingsCard
      modelDefaults={freshModelDefaults()}
      groupDefaults={freshGroupDefaults()}
      toolPricesDefault='{}'
      visibleTabs={['groups']}
    />
  )
}

function groupNameInputs(container: HTMLElement): HTMLInputElement[] {
  return Array.from(
    container.querySelectorAll<HTMLInputElement>('input')
  ).filter(
    (input) =>
      input.getAttribute('type') !== 'checkbox' &&
      input.getAttribute('type') !== 'number'
  )
}

describe('Group Pricing keeps an unsaved row across unrelated re-renders', () => {
  afterAll(() => {
    domWindow.close()
  })

  test('a group typed but not yet saved survives the parent re-rendering with equal defaults', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          queryFn: async () => {
            throw new Error('no network in tests')
          },
        },
      },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <Parent />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })

    const addButton = Array.from(container.querySelectorAll('button')).find(
      (button) => button.textContent?.trim() === 'Add group'
    )
    assert.ok(addButton, 'the Group Pricing table renders its Add group button')

    const before = groupNameInputs(container).map((input) => input.value)
    assert.ok(before.includes('GenAI'), 'saved groups are listed')
    assert.ok(!before.includes('default'), 'precondition: no default row yet')

    await act(async () => {
      addButton.dispatchEvent(
        new domWindow.Event('click', { bubbles: true }) as unknown as Event
      )
    })
    const newRow = groupNameInputs(container).find((input) =>
      input.value.startsWith('group_')
    )
    assert.ok(newRow, 'Add group appends a row with a placeholder name')

    await act(async () => {
      changeInputValue(newRow, 'default')
    })
    assert.ok(
      groupNameInputs(container).some((input) => input.value === 'default'),
      'the operator has typed the new group name'
    )

    // Something else on the page re-renders. Nothing about the SAVED values
    // changed; the card merely receives an equal-but-new defaults object, the
    // way section-registry hands it one on every render.
    await act(async () => {
      rerenderParent?.()
    })

    const after = groupNameInputs(container).map((input) => input.value)
    assert.ok(
      after.includes('default'),
      `the unsaved \`default\` row was discarded by an unrelated re-render. Rows now: ${JSON.stringify(after)}. ` +
        'RatioSettingsCard resets the form whenever the groupDefaults object identity changes, ' +
        'and section-registry hands it a new object every render.'
    )

    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  // The other half of the contract: when the SAVED values genuinely change --
  // another admin added a group and the options query refetched -- the form
  // must still follow them. Keying the reset on the value, not the identity,
  // must not freeze a stale form in place.
  test('a genuine change in saved values still resets the form to them', async () => {
    const queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          retry: false,
          queryFn: async () => {
            throw new Error('no network in tests')
          },
        },
      },
    })
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)
    savedGroupRatioOverride = null

    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <Parent />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    assert.ok(
      !groupNameInputs(container).some((input) => input.value === 'Partner_X'),
      'precondition: the new group is not saved yet'
    )

    // Saved settings now include a sixth group. Same object shape, different value.
    savedGroupRatioOverride = JSON.stringify({
      ...JSON.parse(savedGroupRatio),
      Partner_X: 0.8,
    })
    await act(async () => {
      rerenderParent?.()
    })

    assert.ok(
      groupNameInputs(container).some((input) => input.value === 'Partner_X'),
      'a real change in the saved values must still reset the form to them'
    )

    savedGroupRatioOverride = null
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })
})
