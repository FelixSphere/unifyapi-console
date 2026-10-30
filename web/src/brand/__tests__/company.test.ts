/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { UNIFYAI_COMPANY_ADDRESS, companyLine } from '../company'

describe('company line', () => {
  test('is © <computed year> UnifyAI · the standard address', () => {
    assert.equal(
      companyLine(new Date('2031-06-01T00:00:00Z')),
      '© 2031 UnifyAI · Lebuh Bandar Utama PJU 6, 47800 Petaling Jaya, Selangor, Malaysia'
    )
    assert.equal(
      UNIFYAI_COMPANY_ADDRESS,
      'Lebuh Bandar Utama PJU 6, 47800 Petaling Jaya, Selangor, Malaysia'
    )
  })

  test('defaults to this year, so it is never a hard-coded number', () => {
    assert.ok(
      companyLine().startsWith(`© ${new Date().getFullYear()} UnifyAI · `)
    )
  })
})
