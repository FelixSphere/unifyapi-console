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

const HERE = new URL('.', import.meta.url).pathname
const registry = readFileSync(join(HERE, '../section-registry.tsx'), 'utf8')
const section = [
  readFileSync(join(HERE, '../credit-supply-section.tsx'), 'utf8'),
  readFileSync(join(HERE, '../credit-supply-lots.tsx'), 'utf8'),
].join('\n')

describe('credit supply operator surface', () => {
  test('the section is mounted under Billing, right after Settlement', () => {
    const settlement = registry.indexOf("id: 'settlement'")
    const creditPool = registry.indexOf("id: 'credit-supply'")
    assert.notEqual(settlement, -1)
    assert.notEqual(creditPool, -1)
    assert.ok(creditPool > settlement)
    const partnerships = registry.indexOf("id: 'partnerships'")
    assert.ok(creditPool < partnerships)
  })

  test('the screen never posts consumption or status directly', () => {
    // Status changes go through the transition endpoint so the server's
    // lifecycle table is the only one; consumed_usd is server-owned.
    assert.match(section, /transitionCreditLot/)
    assert.doesNotMatch(section, /consumed_usd:\s*Number/)
  })

  test('approval is the moment the compliance question is asked', () => {
    assert.match(section, /right to transfer/i)
    assert.match(section, /transfer_rights_confirmed: true/)
  })

  test('supplier applications can be approved or rejected with a reason', () => {
    const suppliers = readFileSync(
      join(HERE, '../credit-supply-suppliers.tsx'),
      'utf8'
    )
    assert.match(suppliers, /decide\(supplier, 'active'\)/)
    assert.match(
      suppliers,
      /decide\(rejecting, 'rejected', rejectReason\.trim\(\)\)/
    )
  })

  test('rejection and suspension carry a reason the supplier will read', () => {
    assert.match(section, /ReasonDialog/)
    assert.match(section, /reason: reason\.trim\(\)/)
  })
})
