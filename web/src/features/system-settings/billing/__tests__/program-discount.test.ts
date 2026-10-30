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

import {
  parseProgramDiscount,
  programDiscountPercent,
} from '../program-discount'

describe('partnership program discount input', () => {
  // Empty is a real state, not a missing value: it means the program sets no
  // discount, and saving it removes the rows the program owns. Failing
  // validation on an empty box would make "no discount" unreachable once one
  // had ever been set.
  test('an empty box means no program discount, not an error', () => {
    assert.deepEqual(parseProgramDiscount(''), { ok: true, value: 0 })
    assert.deepEqual(parseProgramDiscount('   '), { ok: true, value: 0 })
    assert.deepEqual(parseProgramDiscount('0'), { ok: true, value: 0 })
  })

  test('a multiplier in range is taken as typed', () => {
    assert.deepEqual(parseProgramDiscount('0.85'), { ok: true, value: 0.85 })
    assert.deepEqual(parseProgramDiscount('1'), { ok: true, value: 1 })
    assert.deepEqual(parseProgramDiscount(' 0.9 '), { ok: true, value: 0.9 })
  })

  // Above 1 is a surcharge. Every screen renders this beside the word
  // discount, so 1.2 would read as "120% of the official price" and look like
  // a deal while charging more.
  test('a surcharge is refused, and so is a negative price', () => {
    assert.deepEqual(parseProgramDiscount('1.2'), {
      ok: false,
      reason: 'out-of-range',
    })
    assert.deepEqual(parseProgramDiscount('-0.1'), {
      ok: false,
      reason: 'out-of-range',
    })
  })

  test('something that is not a number is refused rather than coerced', () => {
    assert.deepEqual(parseProgramDiscount('abc'), {
      ok: false,
      reason: 'not-a-number',
    })
    // Number('') is 0 and Number(' ') is 0 -- handled above as "no discount" --
    // but Number('1e999') is Infinity, which would pass a naive range check
    // written as `value > 1` on a NaN-unaware path.
    assert.deepEqual(parseProgramDiscount('1e999'), {
      ok: false,
      reason: 'not-a-number',
    })
  })

  test('the hint describes a real discount and stays quiet otherwise', () => {
    assert.equal(programDiscountPercent('0.85'), 85)
    assert.equal(programDiscountPercent('0.925'), 92.5)
    assert.equal(programDiscountPercent(''), null)
    assert.equal(programDiscountPercent('0'), null)
    assert.equal(programDiscountPercent('1.5'), null)
  })

  test('the program form parses through this module rather than inline', () => {
    const source = readFileSync(
      join(import.meta.dir, '..', 'partnership-programs-section.tsx'),
      'utf8'
    )
    assert.match(source, /parseProgramDiscount\(form\.discount\)/)
    assert.match(source, /discount: parsedDiscount\.value/)
    assert.match(
      source,
      /value=\{form\.discount\}/,
      'the input must be bound to the form field'
    )
  })
})
