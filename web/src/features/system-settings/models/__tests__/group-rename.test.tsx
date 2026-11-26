/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: a group's name is changed by the server, not by the form.

On 2026-11-22 an operator typed "Gade Union" over "friend solo" in Group
Pricing and saved. The form sent the same table with one key replaced. Nothing
else followed: the customer's logins, keys, wallet, per-model prices and
channel exclusions stayed on a name that no longer had a price, and its keys
were refused. These tests hold the three things that let it happen:

  * a saved group's name was a free-text field;
  * a new group was offered to every login unless the operator unticked it;
  * a save the server refused part of carried on and recorded itself as saved.
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

// Every request the screen makes ends here instead of on the network.
api.defaults.adapter = async (config) => {
  const method = (config.method ?? 'get').toLowerCase()
  const request: Sent = {
    method,
    url: config.url ?? '',
    body: config.data ? JSON.parse(String(config.data)) : {},
  }
  if (method !== 'get') sent.push(request)
  return {
    data: method === 'get' ? { success: true, data: [] } : respond(request),
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  }
}

const groupDefaults = () => ({
  GroupRatio: JSON.stringify({ default: 1, 'friend solo': 1, GenAI: 1 }),
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

describe('Group Pricing renames a saved group on the server', () => {
  beforeEach(() => {
    sent = []
    respond = () => ({ success: true })
  })
  afterAll(() => {
    domWindow.close()
  })

  test('the name of a saved group cannot be typed over', async () => {
    const screen = await mount()

    for (const name of ['default', 'friend solo', 'GenAI']) {
      assert.equal(
        nameInput(screen.container, name).readOnly,
        true,
        `${name} is saved: logins, keys and a wallet may carry its name, so the field is read-only`
      )
    }

    await screen.unmount()
  })

  test('Rename sends the old and new name to the rename endpoint and saves no option', async () => {
    const screen = await mount()
    respond = () => ({
      success: true,
      data: { from: 'friend solo', to: 'Gade Union', options: [], rows: {} },
    })

    await act(async () => {
      click(
        buttonIn(
          rowOf(nameInput(screen.container, 'friend solo')),
          'Rename group'
        )
      )
    })
    await settle()

    const dialog = document.querySelector('[role="alertdialog"]')
    assert.ok(dialog, 'Rename opens a dialog')
    const field = textInputs(dialog as unknown as ParentNode)[0]
    assert.ok(field, 'the dialog asks for the new name')
    assert.equal(field.value, 'friend solo', 'starting from the current name')

    await act(async () => {
      changeInputValue(field, 'Gade Union')
    })
    await act(async () => {
      click(buttonIn(dialog as unknown as ParentNode, 'Rename'))
    })
    await settle()

    assert.deepEqual(
      sent,
      [
        {
          method: 'post',
          url: '/api/pricing/group/rename',
          body: { from: 'friend solo', to: 'Gade Union' },
        },
      ],
      'one rename request, and no option saved with the key replaced'
    )
    assert.ok(
      screen.invalidated.includes('system-options') &&
        screen.invalidated.includes('group-model-pricing'),
      `the stored settings and customer prices are read again; invalidated: ${JSON.stringify(screen.invalidated)}`
    )
    assert.equal(
      document.querySelector('[role="alertdialog"]'),
      null,
      'the dialog closes once the rename is done'
    )

    await screen.unmount()
  })

  test('a name that is already a group is refused in the dialog and nothing is sent', async () => {
    const screen = await mount()

    await act(async () => {
      click(
        buttonIn(
          rowOf(nameInput(screen.container, 'friend solo')),
          'Rename group'
        )
      )
    })
    await settle()
    const dialog = document.querySelector('[role="alertdialog"]')
    assert.ok(dialog)
    const field = textInputs(dialog as unknown as ParentNode)[0]

    await act(async () => {
      changeInputValue(field, 'GenAI')
    })

    const confirm = buttonIn(dialog as unknown as ParentNode, 'Rename')
    assert.equal(confirm.disabled, true, 'GenAI already exists')
    assert.ok(
      dialog.textContent?.includes('A group with this name already exists'),
      'and the dialog says why'
    )
    await act(async () => {
      click(confirm)
    })
    await settle()
    assert.deepEqual(sent, [])

    await screen.unmount()
  })

  test('a new group is a free-text row that is not offered to every login', async () => {
    const screen = await mount()

    await act(async () => {
      click(buttonIn(screen.container, 'Add group'))
    })
    const added = nameInput(screen.container, 'group_1')
    assert.equal(added.readOnly, false, 'a group not saved yet is named freely')
    await act(async () => {
      changeInputValue(added, 'Kingdee')
    })

    const row = rowOf(nameInput(screen.container, 'Kingdee'))
    const selectable = row.querySelector('[role="checkbox"]')
    assert.ok(selectable, 'the row has its User selectable box')
    assert.equal(
      selectable.getAttribute('aria-checked'),
      'false',
      'a new group is not user selectable unless the operator ticks it'
    )
    assert.equal(
      row.querySelector('[aria-label="Rename group"]'),
      null,
      'there is nothing on the server to rename yet'
    )
    assert.equal(
      buttonIn(rowOf(nameInput(screen.container, 'GenAI')), 'Rename group')
        .disabled,
      true,
      'a rename reloads the saved settings, so it waits for the unsaved row'
    )

    await act(async () => {
      click(buttonIn(screen.actions, 'Save group ratios'))
    })
    await settle()

    const saved = Object.fromEntries(
      sent.map((request) => [request.body.key, request.body.value])
    )
    assert.deepEqual(JSON.parse(String(saved.GroupRatio)), {
      default: 1,
      'friend solo': 1,
      GenAI: 1,
      Kingdee: 1,
    })
    assert.equal(
      'UserUsableGroups' in saved,
      false,
      `adding a group must not publish it to every login; saved: ${JSON.stringify(sent)}`
    )
    assert.equal(
      nameInput(screen.container, 'Kingdee').readOnly,
      true,
      'once saved, the new group is renamed like any other'
    )

    await screen.unmount()
  })

  test('a save the server refuses stops there and is not recorded as saved', async () => {
    const screen = await mount()
    respond = (request) =>
      request.body.key === 'GroupRatio'
        ? {
            success: false,
            message: 'group "friend solo" is still used by 1 login(s)',
          }
        : { success: true }

    const row = rowOf(nameInput(screen.container, 'friend solo'))
    await act(async () => {
      click(buttonIn(row, 'Delete'))
    })
    const defaultRow = rowOf(nameInput(screen.container, 'default'))
    const description = textInputs(defaultRow).find(
      (input) => input.value === 'Default'
    )
    assert.ok(description, 'the default group has a description field')
    await act(async () => {
      changeInputValue(description, 'Standard')
    })
    await act(async () => {
      click(buttonIn(screen.actions, 'Save group ratios'))
    })
    await settle()

    assert.deepEqual(
      sent.map((request) => request.body.key),
      ['GroupRatio'],
      'the refused price table is not followed by the maps that depend on it'
    )

    // The operator saves again. Everything is still unsaved, so everything is
    // sent again -- it was not quietly recorded as stored the first time.
    sent = []
    await act(async () => {
      click(buttonIn(screen.actions, 'Save group ratios'))
    })
    await settle()
    assert.deepEqual(
      sent.map((request) => request.body.key),
      ['GroupRatio']
    )

    await screen.unmount()
  })
})
