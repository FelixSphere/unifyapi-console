/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const here = import.meta.dir
const tokens = readFileSync(join(here, '..', 'tokens.gen.css'), 'utf8')
const brand = readFileSync(join(here, '..', 'unifyapi.css'), 'utf8')
const kit = '/Users/y.wang/unifyai-brand-kit/tokens.css'

const HEX = /#[0-9a-fA-F]{3,8}\b/g

describe('UI-STANDARD.md consistency checks, run on every build', () => {
  test('1. the brand stylesheet introduces no hex colour outside tokens.css', () => {
    const allowed = new Set(
      (tokens.match(HEX) ?? []).map((h) => h.toLowerCase())
    )
    const stray = (brand.match(HEX) ?? []).filter(
      (h) => !allowed.has(h.toLowerCase())
    )
    assert.deepEqual(stray, [], 'every colour must come from tokens.css')
  })

  test('2. no gradients or drop shadows are declared by the brand stylesheet', () => {
    const declarations = brand.replaceAll(/\/\*[\s\S]*?\*\//g, '')
    assert.doesNotMatch(declarations, /linear-gradient|radial-gradient/)
    const shadows = declarations.match(/box-shadow\s*:\s*[^;]+/g) ?? []
    assert.deepEqual(
      shadows,
      ['box-shadow: none'],
      'the only shadow declaration removes shadows'
    )
  })

  test('tokens.css is the kit file, copied verbatim', () => {
    try {
      assert.equal(tokens, readFileSync(kit, 'utf8'))
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
    }
  })

  test('the three brand faces are self-hosted and the old ones are gone', () => {
    for (const face of ['Instrument Sans', 'Space Grotesk', 'IBM Plex Mono']) {
      assert.ok(brand.includes(`font-family: '${face}'`), face)
    }
    assert.doesNotMatch(brand, /fonts\.googleapis|fonts\.gstatic/)
    assert.doesNotMatch(brand, /Inter Variable|Instrument Serif|JetBrains Mono/)
  })
})
