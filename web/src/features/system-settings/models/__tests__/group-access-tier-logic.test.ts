/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: which rows of Group Pricing can carry the access tier mark, and
that everything the operator reads about it is translated.
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import {
  ACCESS_TIER_GROUPS_QUERY_KEY,
  accessTierBlocker,
} from '../group-access-tier-logic'

describe('which groups can be marked as an access tier', () => {
  test('a saved group can', () => {
    assert.equal(accessTierBlocker({ name: 'Vip User', draft: false }), null)
  })

  test('default cannot: its logins already keep their own wallets', () => {
    assert.equal(
      accessTierBlocker({ name: 'default', draft: false }),
      'Logins in default already keep their own wallets'
    )
    assert.equal(
      accessTierBlocker({ name: ' default ', draft: true }),
      'Logins in default already keep their own wallets'
    )
  })

  test('a row that is not saved yet cannot: the server has no such group', () => {
    assert.equal(
      accessTierBlocker({ name: 'Kingdee', draft: true }),
      'Save the group before marking it as an access tier'
    )
  })

  test('the marks are re-read whenever the stored settings are', () => {
    // A rename invalidates ['system-options'] and also renames the mark.
    assert.equal(ACCESS_TIER_GROUPS_QUERY_KEY[0], 'system-options')
    assert.equal(ACCESS_TIER_GROUPS_QUERY_KEY.length, 2)
  })
})

// Translations are keyed by the English text. The keys are read from the code
// that shows them, so a string added there cannot ship untranslated.
describe('marking an access tier: every string is translated', () => {
  const here = new URL('.', import.meta.url).pathname
  const localesDir = join(here, '../../../../i18n/locales')
  const literal = /'((?:[^'\\]|\\.)*)'/

  const keys = new Set<string>([
    // The audit log labels (usage-logs/lib/format.ts).
    'Marked pricing group {{group}} as an access tier',
    'Removed the access tier mark from pricing group {{group}}',
  ])
  for (const file of [
    '../group-access-tier-dialog.tsx',
    '../group-ratio-visual-editor.tsx',
  ]) {
    const source = readFileSync(join(here, file), 'utf8')
    for (const match of source.matchAll(
      new RegExp(`\\bt\\(\\s*${literal.source}`, 'g')
    )) {
      if (file.endsWith('dialog.tsx') || /access tier/i.test(match[1])) {
        keys.add(match[1])
      }
    }
  }
  const logic = readFileSync(
    join(here, '../group-access-tier-logic.ts'),
    'utf8'
  )
  for (const match of logic.matchAll(
    new RegExp(`return ${literal.source}`, 'g')
  )) {
    keys.add(match[1])
  }

  test('the scan finds the column, the dialog and the reasons', () => {
    for (const expected of [
      'Access tier',
      'Access tier: members keep their own wallets',
      'Mark as an access tier',
      'Remove the access tier mark',
      'Logins in default already keep their own wallets',
      'Save the group before marking it as an access tier',
    ]) {
      assert.ok(keys.has(expected), `missing "${expected}"`)
    }
    assert.ok(keys.size >= 15, `found ${keys.size} keys`)
  })

  for (const file of readdirSync(localesDir).filter((name) =>
    name.endsWith('.json')
  )) {
    test(`${file} has every access tier string`, () => {
      const translation = JSON.parse(
        readFileSync(join(localesDir, file), 'utf8')
      ).translation as Record<string, string>
      for (const key of keys) {
        assert.ok(translation[key], `${file} is missing "${key}"`)
        if (file !== 'en.json') {
          assert.notEqual(
            translation[key],
            key,
            `${file} still shows English for "${key}"`
          )
        }
      }
    })
  }
})
