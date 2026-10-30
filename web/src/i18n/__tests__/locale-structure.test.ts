/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: a locale file is one `translation` namespace and nothing else.

i18next only resolves keys inside that namespace. A key written at the JSON
root is syntactically valid, parses, passes every per-screen translation test
that does not happen to scan its component -- and renders as English, silently,
with no console warning. Four keys shipped that way in September 2026 because
a line-based insert treated the `"translation": {` wrapper as an entry and
placed everything that sorted before "translation" above it.
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const LOCALES_DIR = join(new URL('.', import.meta.url).pathname, '../locales')

const localeFiles = readdirSync(LOCALES_DIR).filter(
  (name) => name.endsWith('.json') && !name.startsWith('_')
)

describe('locale file structure', () => {
  test('there are locale files to check', () => {
    assert.ok(localeFiles.length >= 7, `found ${localeFiles.length}`)
  })

  for (const file of localeFiles) {
    test(`${file} has exactly one root key, "translation"`, () => {
      const parsed = JSON.parse(readFileSync(join(LOCALES_DIR, file), 'utf8'))
      assert.deepEqual(
        Object.keys(parsed),
        ['translation'],
        `${file}: keys outside the translation namespace are invisible to i18next and render as English`
      )
      assert.equal(typeof parsed.translation, 'object')
    })
  }
})
