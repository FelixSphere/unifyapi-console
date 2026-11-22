/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import {
  groupRenameProblem,
  hasUnsavedGroupChanges,
  savedGroupNames,
} from '../group-rename-logic'

describe('renaming a pricing group: what the dialog accepts', () => {
  const existing = ['default', 'friend solo', 'GenAI']

  test('a new, free name is accepted, surrounding spaces ignored', () => {
    assert.equal(
      groupRenameProblem('friend solo', 'Gade Union', existing),
      null
    )
    assert.equal(
      groupRenameProblem('friend solo', '  Gade Union ', existing),
      null
    )
  })

  test('each name the server would refuse is refused before the request', () => {
    assert.equal(
      groupRenameProblem('friend solo', '   ', existing),
      'Enter the new group name'
    )
    assert.equal(
      groupRenameProblem('friend solo', 'friend solo', existing),
      'The new name is the same as the current one'
    )
    assert.equal(
      groupRenameProblem('friend solo', 'GenAI', existing),
      'A group with this name already exists'
    )
    assert.equal(
      groupRenameProblem('friend solo', 'auto', existing),
      'This group name is reserved'
    )
    assert.equal(
      groupRenameProblem('friend solo', 'default', existing),
      'This group name is reserved'
    )
    assert.equal(
      groupRenameProblem('friend solo', 'Gade, Union', existing),
      'A group name cannot contain a comma'
    )
  })
})

describe('renaming a pricing group: what counts as saved', () => {
  test('saved groups are the keys of the stored Group Pricing', () => {
    assert.deepEqual(savedGroupNames('{"default":1,"friend solo":0.9}'), [
      'default',
      'friend solo',
    ])
    assert.deepEqual(savedGroupNames(''), [])
    assert.deepEqual(savedGroupNames('[1,2]'), [])
    assert.deepEqual(savedGroupNames('{not json'), [])
  })

  test('formatting is not a change; a value is', () => {
    const saved = {
      GroupRatio: '{"default":1,"GenAI":1}',
      AutoGroups: '[]',
      MaxTokenAutoGroups: 2,
      DefaultUseAutoGroup: false,
    }
    assert.equal(
      hasUnsavedGroupChanges(
        {
          GroupRatio: '{\n  "default": 1,\n  "GenAI": 1\n}',
          AutoGroups: '[]',
          MaxTokenAutoGroups: 2,
          DefaultUseAutoGroup: false,
        },
        saved
      ),
      false
    )
    assert.equal(
      hasUnsavedGroupChanges({ ...saved, GroupRatio: '{"default":1}' }, saved),
      true
    )
    assert.equal(
      hasUnsavedGroupChanges({ ...saved, DefaultUseAutoGroup: true }, saved),
      true
    )
    assert.equal(
      hasUnsavedGroupChanges({ ...saved, MaxTokenAutoGroups: 3 }, saved),
      true
    )
  })
})

// An untranslated key renders as its English text with no warning, and the
// refusal reasons are chosen at runtime, so a scan of t('…') calls alone would
// not see them.
describe('renaming a pricing group: every string is translated', () => {
  const here = new URL('.', import.meta.url).pathname
  const localesDir = join(here, '../../../../i18n/locales')
  const literal = /'((?:[^'\\]|\\.)*)'/

  const keys = new Set<string>([
    'Renamed pricing group {{from}} to {{to}}',
    'Use Rename to change the name of a saved group',
    'Save or discard your other changes before renaming a group',
  ])
  const dialog = readFileSync(join(here, '../group-rename-dialog.tsx'), 'utf8')
  for (const match of dialog.matchAll(
    new RegExp(`\\bt\\(\\s*${literal.source}`, 'g')
  )) {
    keys.add(match[1])
  }
  const logic = readFileSync(join(here, '../group-rename-logic.ts'), 'utf8')
  for (const match of logic.matchAll(
    new RegExp(`return ${literal.source}`, 'g')
  )) {
    keys.add(match[1])
  }

  test('the scan finds the dialog and the refusal reasons', () => {
    assert.ok(keys.has('Rename group'))
    assert.ok(keys.has('A group with this name already exists'))
    assert.ok(keys.size >= 14, `found ${keys.size} keys`)
  })

  for (const file of readdirSync(localesDir).filter((name) =>
    name.endsWith('.json')
  )) {
    test(`${file} has every rename string`, () => {
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
