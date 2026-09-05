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
    assert.match(page, /This account is not a credit supplier/)
  })

  test('the dashboard offers the portal only to supplier logins', () => {
    assert.match(dashboard, /supplierOnly: true/)
    assert.match(dashboard, /isSupplierLogin/)
  })
})
