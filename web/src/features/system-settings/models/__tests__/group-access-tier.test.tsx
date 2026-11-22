/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: a pricing group is marked as an access tier in Group Pricing.

`Vip User` is a group whose members may call a model the other groups are
blocked from. On 2026-11-22 four unrelated logins were moved into it and,
because a pricing group is a customer with one wallet, were pooled into one
balance. The mark tells the server the group's logins are strangers. These
tests hold what the table does with it:

  * it shows what the server has stored, not what the form holds;
  * a tick asks first, and sends one request to the operation that checks the
    wallets -- never an option save;
  * a refusal stays on screen, because it names the wallet and the logins.
*/
import { afterAll, beforeEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'http://localhost/' })
const domGlobals = [
  'window',
  'document',
  'navigator',
  'location',
  'localStorage',
  'sessionStorage',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'KeyboardEvent',
  'MouseEvent',
  'PointerEvent',
  'FocusEvent',
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { api } = await import('@/lib/api')
const { SettingsPageProvider } =
  await import('../../components/settings-page-context')
const { RatioSettingsCard } = await import('../ratio-settings-card')

const i18n = createInstance()
await i18n.use(initReactI18next).init({ lng: 'en', resources: { en: {} } })

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type Sent = { method: string; url: string; body: Record<string, unknown> }
let sent: Sent[] = []
let respond: (request: Sent) => unknown = () => ({ success: true })

const TIER_ROUTE = '/api/pricing/group/access-tier'
let storedTiers: string[] = []
let tierReads = 0

// Every request the screen makes ends here instead of on the network.
api.defaults.adapter = async (config) => {
  const method = (config.method ?? 'get').toLowerCase()
  const request: Sent = {
    method,
    url: config.url ?? '',
    body: config.data ? JSON.parse(String(config.data)) : {},
  }
  if (method !== 'get') sent.push(request)
  let data: unknown = { success: true, data: [] }
  if (method !== 'get') {
    data = respond(request)
  } else if (request.url === TIER_ROUTE) {
    tierReads += 1
    data = { success: true, data: { groups: storedTiers } }
  }
  return { data, status: 200, statusText: 'OK', headers: {}, config }
}

const groupDefaults = () => ({
  GroupRatio: JSON.stringify({ default: 1, 'Vip User': 1, GenAI: 1 }),
  TopupGroupRatio: '{}',
  UserUsableGroups: JSON.stringify({ default: 'Default' }),
  GroupGroupRatio: '{}',
  AutoGroups: '[]',
  MaxTokenAutoGroups: 2,
  DefaultUseAutoGroup: false,
  GroupSpecialUsableGroup: '{}',
})

const modelDefaults = () => ({
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

function click(element: Element) {
  element.dispatchEvent(
    new domWindow.MouseEvent('click', {
      bubbles: true,
      cancelable: true,
    }) as unknown as Event
  )
}

async function settle() {
  for (let i = 0; i < 5; i += 1) {
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }
}

function textInputs(root: ParentNode): HTMLInputElement[] {
  return [...root.querySelectorAll<HTMLInputElement>('input')].filter(
    (input) =>
      input.getAttribute('type') !== 'checkbox' &&
      input.getAttribute('type') !== 'number'
  )
}

function nameInput(container: HTMLElement, name: string): HTMLInputElement {
  const input = textInputs(container).find((entry) => entry.value === name)
  assert.ok(input, `the table lists a group named ${name}`)
  return input
}

function buttonIn(root: ParentNode, label: string): HTMLButtonElement {
  const button = [...root.querySelectorAll<HTMLButtonElement>('button')].find(
    (entry) =>
      entry.getAttribute('aria-label') === label ||
      entry.textContent?.trim() === label
  )
  assert.ok(button, `a "${label}" button is shown`)
  return button
}

function rowOf(input: HTMLInputElement): HTMLElement {
  const row = input.closest('tr')
  assert.ok(row, 'the name sits in a table row')
  return row as unknown as HTMLElement
}

async function mount() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const invalidated: string[] = []
  const invalidate = queryClient.invalidateQueries.bind(queryClient)
  queryClient.invalidateQueries = ((filters?: { queryKey?: unknown[] }) => {
    invalidated.push(String(filters?.queryKey?.[0]))
    return invalidate(filters)
  }) as typeof queryClient.invalidateQueries

  const container = document.createElement('div')
  const actions = document.createElement('div')
  document.body.append(container, actions)
  const root = createRoot(container)
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <SettingsPageProvider
            actionsContainer={actions as unknown as HTMLDivElement}
          >
            <RatioSettingsCard
              modelDefaults={modelDefaults()}
              groupDefaults={groupDefaults()}
              toolPricesDefault='{}'
              visibleTabs={['groups']}
            />
          </SettingsPageProvider>
        </I18nextProvider>
      </QueryClientProvider>
    )
  })
  await settle()
  return {
    container,
    actions,
    invalidated,
    unmount: async () => {
      await act(async () => {
        root.unmount()
      })
      container.remove()
      actions.remove()
    },
  }
}

const TIER_LABEL = 'Access tier: members keep their own wallets'

function tierBox(container: HTMLElement, name: string): HTMLElement {
  const box = rowOf(nameInput(container, name)).querySelector(
    `[role="checkbox"][aria-label="${TIER_LABEL}"]`
  )
  assert.ok(box, `the ${name} row has an access tier box`)
  return box as unknown as HTMLElement
}

function isDisabled(box: HTMLElement): boolean {
  return (
    box.hasAttribute('disabled') ||
    box.getAttribute('aria-disabled') === 'true' ||
    box.hasAttribute('data-disabled')
  )
}

function openDialog(): ParentNode {
  const dialog = document.querySelector('[role="alertdialog"]')
  assert.ok(dialog, 'ticking the box asks before it changes anything')
  return dialog as unknown as ParentNode
}

describe('Group Pricing marks a saved group as an access tier', () => {
  beforeEach(() => {
    sent = []
    respond = () => ({ success: true })
    storedTiers = ['Vip User']
    tierReads = 0
  })
  afterAll(() => {
    domWindow.close()
  })

  test('the table shows which groups the server has marked', async () => {
    const screen = await mount()

    assert.equal(
      tierBox(screen.container, 'Vip User').getAttribute('aria-checked'),
      'true'
    )
    assert.equal(
      tierBox(screen.container, 'GenAI').getAttribute('aria-checked'),
      'false',
      'a group is a customer with one wallet unless it is marked'
    )
    assert.equal(isDisabled(tierBox(screen.container, 'Vip User')), false)
    assert.equal(isDisabled(tierBox(screen.container, 'GenAI')), false)
    assert.equal(
      isDisabled(tierBox(screen.container, 'default')),
      true,
      'default already gives every login its own wallet'
    )

    await screen.unmount()
  })

  test('ticking the box asks, then sends one request and saves no option', async () => {
    const screen = await mount()
    respond = () => ({
      success: true,
      data: {
        group: 'GenAI',
        access_tier: true,
        changed: true,
        groups: ['Vip User', 'GenAI'],
      },
    })

    await act(async () => {
      click(tierBox(screen.container, 'GenAI'))
    })
    await settle()
    assert.deepEqual(sent, [], 'nothing is sent until the operator confirms')
    const dialog = openDialog()
    assert.ok(
      dialog.textContent?.includes('GenAI') &&
        dialog.textContent?.includes('will each keep their own wallet'),
      `the dialog says what the mark means: ${dialog.textContent}`
    )

    const readsBefore = tierReads
    storedTiers = ['Vip User', 'GenAI']
    await act(async () => {
      click(buttonIn(dialog, 'Mark as access tier'))
    })
    await settle()

    assert.deepEqual(
      sent,
      [
        {
          method: 'post',
          url: TIER_ROUTE,
          body: { group: 'GenAI', access_tier: true },
        },
      ],
      'one request to the operation that checks the wallets, and no option saved'
    )
    assert.equal(
      document.querySelector('[role="alertdialog"]'),
      null,
      'the dialog closes once the group is marked'
    )
    assert.ok(tierReads > readsBefore, 'the stored marks are read again')
    assert.equal(
      tierBox(screen.container, 'GenAI').getAttribute('aria-checked'),
      'true',
      'and the table shows what is now stored'
    )

    await screen.unmount()
  })

  test('unticking a marked group asks to remove the mark', async () => {
    const screen = await mount()

    await act(async () => {
      click(tierBox(screen.container, 'Vip User'))
    })
    await settle()
    const dialog = openDialog()
    assert.ok(
      dialog.textContent?.includes('becomes a customer group again'),
      `the dialog says what removing the mark means: ${dialog.textContent}`
    )
    await act(async () => {
      click(buttonIn(dialog, 'Remove mark'))
    })
    await settle()

    assert.deepEqual(sent, [
      {
        method: 'post',
        url: TIER_ROUTE,
        body: { group: 'Vip User', access_tier: false },
      },
    ])

    await screen.unmount()
  })

  test('a refusal stays in the dialog and the box stays as it was', async () => {
    const screen = await mount()
    const refusal =
      'group "GenAI" has a shared wallet (tenant 7, balance 5000) that 2 logins spend from: alice, bob.'
    respond = () => ({ success: false, message: refusal })

    await act(async () => {
      click(tierBox(screen.container, 'GenAI'))
    })
    await settle()
    await act(async () => {
      click(buttonIn(openDialog(), 'Mark as access tier'))
    })
    await settle()

    const dialog = openDialog()
    assert.ok(
      dialog.querySelector('[role="alert"]')?.textContent?.includes(refusal),
      `the server's reason is shown where the operator is looking: ${dialog.textContent}`
    )
    assert.equal(
      tierBox(screen.container, 'GenAI').getAttribute('aria-checked'),
      'false',
      'a refused mark is not shown as made'
    )
    assert.equal(sent.length, 1, 'and nothing else was sent')

    await screen.unmount()
  })

  test('a group that is not saved yet cannot be marked', async () => {
    const screen = await mount()

    await act(async () => {
      click(buttonIn(screen.container, 'Add group'))
    })
    const box = tierBox(screen.container, 'group_1')
    assert.equal(
      isDisabled(box),
      true,
      'the server has no such group until the form is saved'
    )
    await act(async () => {
      click(box)
    })
    await settle()
    assert.equal(document.querySelector('[role="alertdialog"]'), null)

    await screen.unmount()
  })

  test('saving the form never sends the list of access tiers', async () => {
    const screen = await mount()
    const description = textInputs(
      rowOf(nameInput(screen.container, 'default'))
    ).find((input) => input.value === 'Default')
    assert.ok(description, 'the default group has a description field')
    await act(async () => {
      changeInputValue(description, 'Standard')
    })
    await act(async () => {
      click(buttonIn(screen.actions, 'Save group ratios'))
    })
    await settle()

    assert.ok(sent.length > 0, 'the save went out')
    assert.deepEqual(
      sent.filter(
        (request) =>
          request.url === TIER_ROUTE || request.body.key === 'AccessTierGroups'
      ),
      [],
      'the marks are not part of the form'
    )

    await screen.unmount()
  })
})
