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
const page = readFileSync(join(HERE, '../index.tsx'), 'utf8')
const dialog = readFileSync(
  join(HERE, '../components/submit-lot-dialog.tsx'),
  'utf8'
)
const api = readFileSync(join(HERE, '../api.ts'), 'utf8')
const dashboard = readFileSync(
  join(HERE, '../../dashboard/components/overview/overview-dashboard.tsx'),
  'utf8'
)

describe('supplier portal surface', () => {
  test('the supplier can submit but never activate a lot', () => {
    assert.match(dialog, /submitSupplierLot/)
    assert.doesNotMatch(page, /transitionCreditLot/)
    assert.doesNotMatch(dialog, /transitionCreditLot/)
    assert.doesNotMatch(api, /credit-pool\/lots/)
  })

  test('a sale carries the attestation, a write-only key and a payout choice; the rate is posted, not typed', () => {
    assert.match(dialog, /transfer_rights_confirmed: true/)
    assert.match(dialog, /PasswordInput/)
    assert.match(dialog, /payout_method: form\.payoutMethod/)
    // The seller never sets their own price: no acquisition_rate is sent.
    assert.doesNotMatch(dialog, /acquisition_rate:/)
  })

  test('a login that has not sold yet sees the posted terms, not an error', () => {
    assert.match(api, /skipErrorHandler: true/)
    assert.match(page, /getSupplierTerms/)
    // No application step exists any more.
    assert.doesNotMatch(page, /applyForSupplier|SellCreditsCard|EmptyState/)
    assert.doesNotMatch(api, /\/api\/supplier\/apply/)
  })

  test('the way in is the sidebar entry, routed by role', () => {
    const sidebar = readFileSync(
      join(HERE, '../../../hooks/use-sidebar-data.ts'),
      'utf8'
    )
    assert.match(sidebar, /url: '\/credit-supply'/)
    const hub = readFileSync(join(HERE, '../hub.tsx'), 'utf8')
    assert.match(hub, /SUPER_ADMIN/)
    assert.match(hub, /<SupplierPortal \/>/)
    assert.match(hub, /<CreditSupplySection \/>/)
    // The dashboard action points at the same entry; Wallet no longer carries it.
    assert.match(dashboard, /to: '\/credit-supply'/)
    const wallet = readFileSync(join(HERE, '../../wallet/index.tsx'), 'utf8')
    assert.doesNotMatch(
      wallet,
      /SellCreditsCard|credit-contributions|supplier-portal/
    )
  })

  test('the duplicate contribution module is gone', () => {
    const ops = readFileSync(join(HERE, '../../ops/index.tsx'), 'utf8')
    assert.doesNotMatch(ops, /CreditContributions/)
  })
})
