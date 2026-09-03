/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { CustomerPriceTable } = await import('../group-model-pricing-editor')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        Model: 'Model',
        Vendor: 'Vendor',
        'Official in/out': 'Official in/out',
        'Final multiplier': 'Final multiplier',
        'Customer in/out': 'Customer in/out',
        Override: 'Override',
        default: 'default',
        'Reset to default': 'Reset to default',
      },
    },
  },
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

describe('customer model pricing table', () => {
  after(() => {
    domWindow.close()
  })

  test('renders every model at the inherited default and edits only overrides', async () => {
    const changes: Array<[string, string]> = []
    const resets: string[] = []
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <CustomerPriceTable
            group='GenAI'
            modelNames={['claude-opus-5', 'qwen3.5-flash']}
            byName={
              new Map([
                [
                  'claude-opus-5',
                  {
                    model: 'claude-opus-5',
                    vendor: 'anthropic',
                    official_input_usd: 5,
                    official_output_usd: 25,
                  },
                ],
                [
                  'qwen3.5-flash',
                  {
                    model: 'qwen3.5-flash',
                    vendor: 'alibaba',
                    official_input_usd: 0.1,
                    official_output_usd: 0.4,
                  },
                ],
              ])
            }
            drafts={{ 'claude-opus-5': '0.65' }}
            modelDiscounts={{ 'qwen3.5-flash': 0.9 }}
            groupRatios={{ GenAI: 0.8 }}
            onChange={(model, value) => changes.push([model, value])}
            onReset={(model) => resets.push(model)}
          />
        </I18nextProvider>
      )
    })

    const rows = [...container.querySelectorAll('tbody tr')]
    assert.equal(rows.length, 2)
    const opusInput = rows[0]?.querySelector<HTMLInputElement>('input')
    const qwenInput = rows[1]?.querySelector<HTMLInputElement>('input')
    assert.ok(opusInput)
    assert.ok(qwenInput)
    assert.equal(opusInput.value, '0.65')
    assert.equal(qwenInput.value, '0.72')
    assert.match(rows[0]?.textContent ?? '', /Override/)
    assert.match(rows[1]?.textContent ?? '', /default/)

    const resetButtons = container.querySelectorAll<HTMLButtonElement>(
      'button[aria-label="Reset to default"]'
    )
    assert.equal(resetButtons.length, 2)
    assert.equal(resetButtons[0]?.disabled, false)
    assert.equal(resetButtons[1]?.disabled, true)

    await act(async () => {
      changeInputValue(qwenInput, '0.7')
      resetButtons[0]?.click()
    })
    assert.deepEqual(changes, [['qwen3.5-flash', '0.7']])
    assert.deepEqual(resets, ['claude-opus-5'])

    await act(async () => root.unmount())
    container.remove()
  })
})
