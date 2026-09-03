import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, test } from 'node:test'

const HERE = new URL('.', import.meta.url).pathname
const registry = readFileSync(join(HERE, '../section-registry.tsx'), 'utf8')
const editor = readFileSync(
  join(HERE, '../../models/group-ratio-visual-editor.tsx'),
  'utf8'
)
const form = readFileSync(
  join(HERE, '../../models/group-ratio-form.tsx'),
  'utf8'
)
const customerPricingEditor = readFileSync(
  join(HERE, '../../models/group-model-pricing-editor.tsx'),
  'utf8'
)
const ratioSettingsCard = readFileSync(
  join(HERE, '../../models/ratio-settings-card.tsx'),
  'utf8'
)

describe('group pricing operator surface', () => {
  test('Group Pricing remains the only section for customer model contracts', () => {
    assert.match(registry, /id: 'group-pricing'/)
    assert.doesNotMatch(registry, /id: 'customer-model-pricing'/)
  })

  test('the unsafe cross-group override editor is no longer mounted', () => {
    const mounts = [...editor.matchAll(/<GroupOverrideRules/g)]
    assert.equal(mounts.length, 0)
    assert.doesNotMatch(editor, /GroupOverrideRules/)
    assert.doesNotMatch(editor, /Special ratio rules/)
    assert.match(editor, /<GroupModelPricingEditor/)
    assert.doesNotMatch(form, /name='GroupGroupRatio'/)
    assert.doesNotMatch(form, /Inter-group overrides/)
  })

  test('draft groups flow into customer pricing and saved groups refresh it', () => {
    assert.match(editor, /draftGroupRatios=/)
    assert.match(customerPricingEditor, /visibleCustomerPricingGroups/)
    assert.match(ratioSettingsCard, /queryKey: \['group-model-pricing'\]/)
  })

  test('customer pricing starts with every model instead of an add-one picker', () => {
    assert.match(customerPricingEditor, /visibleCustomerModelNames/)
    assert.match(customerPricingEditor, /fallbackCustomerMultiplier/)
    assert.doesNotMatch(customerPricingEditor, /<Select/)
    assert.doesNotMatch(customerPricingEditor, /Add model/)
  })
})
