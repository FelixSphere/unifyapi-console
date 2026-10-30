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

  test('approval of an operator-entered lot still asks the compliance question', () => {
    assert.match(section, /right to transfer/i)
    assert.match(section, /transfer_rights_confirmed: true/)
  })

  test('buying credits outright is gone from the operator screen', () => {
    // One deal. Nothing on this screen pays a seller up front, quotes a buy
    // rate, or offers the platform-credit bonus that only a purchase had.
    assert.doesNotMatch(
      section,
      /payCreditLot|Pay & activate|buy_rates|platform_credit_bonus/
    )
    const lots = readFileSync(join(HERE, '../credit-supply-lots.tsx'), 'utf8')
    assert.doesNotMatch(lots, /payCreditLot|Pay & activate|acquisition_rate/)
    const logic = readFileSync(join(HERE, '../credit-supply-logic.ts'), 'utf8')
    assert.doesNotMatch(logic, /payableUSD|purchasePriceUSD|isRevenueShare/)
  })

  test('a verified key is accepted, and accepting still asks the compliance question', () => {
    const logic = readFileSync(join(HERE, '../credit-supply-logic.ts'), 'utf8')
    const verifiedCase = logic.slice(
      logic.indexOf("case 'verified':", logic.indexOf('availableTransitions')),
      logic.indexOf("case 'active':", logic.indexOf('availableTransitions'))
    )
    assert.match(verifiedCase, /to: 'active'/)
    const lots = readFileSync(join(HERE, '../credit-supply-lots.tsx'), 'utf8')
    assert.match(
      lots,
      /lot\.status === 'pending' \|\| lot\.status === 'verified'/,
      'activating a submission opens the right-to-transfer dialog either way'
    )
  })

  test('a seller can be paid their share from the suppliers tab', () => {
    const suppliers = readFileSync(
      join(HERE, '../credit-supply-suppliers.tsx'),
      'utf8'
    )
    assert.match(suppliers, /paySupplierShare/)
    assert.match(suppliers, /unpaidShareUSD/)
    // The operator posts the offer the seller is paid under.
    assert.match(section, /revenue_share_rates/)
  })

  test('the supply terms are posted from the operator screen', () => {
    assert.match(section, /TermsCard/)
    assert.match(section, /key: 'CreditSupplyTerms'/)
    // Share per vendor, the two thresholds, channel priority and manual
    // review are all operator-editable, and that is the whole offer.
    assert.match(section, /revenue-share-/)
    assert.match(section, /manual-review/)
    // The operator screen reads the full terms from its own root endpoint.
    const adminApi = readFileSync(join(HERE, '../credit-supply-api.ts'), 'utf8')
    assert.match(adminApi, /\/api\/credit-supply\/terms/)
    // No application queue any more: suppliers are created by their first sale.
    const suppliers = readFileSync(
      join(HERE, '../credit-supply-suppliers.tsx'),
      'utf8'
    )
    assert.doesNotMatch(suppliers, /decide\(supplier, 'active'\)/)
  })

  test('rejection and suspension carry a reason the supplier will read', () => {
    assert.match(section, /ReasonDialog/)
    assert.match(section, /reason: reason\.trim\(\)/)
  })
})
