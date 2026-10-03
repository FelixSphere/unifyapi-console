/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  UNIFYAI_COMPANY_ADDRESS,
  UNIFYAI_LEGAL_ENTITY_LINE,
  companyLine,
} from '../company'

describe('company line', () => {
  test('is © <computed year> UnifyAI, with no address in the footer', () => {
    assert.equal(
      companyLine(new Date('2031-06-01T00:00:00Z')),
      '© 2031 UnifyAI'
    )
  })

  test('defaults to this year, so it is never a hard-coded number', () => {
    assert.equal(companyLine(), `© ${new Date().getFullYear()} UnifyAI`)
  })

  test('the legal-entity line is FelixSphere LLC, United States, and nothing Malaysian', () => {
    assert.equal(
      UNIFYAI_LEGAL_ENTITY_LINE,
      'Operated by FelixSphere LLC · 6 Karen Ct, CA 94010, United States'
    )
    assert.equal(UNIFYAI_COMPANY_ADDRESS, UNIFYAI_LEGAL_ENTITY_LINE)
    assert.doesNotMatch(UNIFYAI_LEGAL_ENTITY_LINE, /Petaling|Malaysia/)
  })
})
