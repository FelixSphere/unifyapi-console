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

  test('submission carries the supplier attestation and a write-only key', () => {
    assert.match(dialog, /transfer_rights_confirmed: true/)
    assert.match(dialog, /PasswordInput/)
    assert.match(
      dialog,
      /disabled=\{mutation\.isPending \|\| !form\.confirmed\}/
    )
  })

  test('a non-supplier login gets an explanation, not an error toast', () => {
    assert.match(api, /skipErrorHandler: true/)
    // A non-supplier is shown the invitation card instead of a dead end.
    assert.match(page, /me\.isError \|\|/)
    assert.match(page, /<SellCreditsCard \/>/)
  })

  test('every customer is offered the way in', () => {
    // The Wallet card and the dashboard action are for all logins; only the
    // wording changes once the login is an approved supplier.
    assert.match(dashboard, /Sell unused credits/)
    assert.doesNotMatch(dashboard, /supplierOnly/)
    const wallet = readFileSync(join(HERE, '../../wallet/index.tsx'), 'utf8')
    assert.match(wallet, /<SellCreditsCard compact \/>/)
    assert.doesNotMatch(wallet, /credit-contributions/)
    const card = readFileSync(
      join(HERE, '../components/sell-credits-card.tsx'),
      'utf8'
    )
    assert.match(card, /applyForSupplier/)
    assert.doesNotMatch(card, /upstream_key/)
  })

  test('the duplicate contribution module is gone', () => {
    const ops = readFileSync(join(HERE, '../../ops/index.tsx'), 'utf8')
    assert.doesNotMatch(ops, /CreditContributions/)
  })
})
