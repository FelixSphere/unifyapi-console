/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { quotaToUsd, usdToQuota } from './api'
import {
  contributionStatusTone,
  parseExpiry,
  STATUS_LABELS,
  validateSupplierOffer,
} from './logic'

describe('credit contribution UI logic', () => {
  test('uses the backend quota conversion without float drift', () => {
    assert.equal(usdToQuota(12.34), 6_170_000)
    assert.equal(quotaToUsd(6_170_000), 12.34)
  })

  test('requires a positive offer, bounded purchase rate, and attestation', () => {
    assert.match(
      validateSupplierOffer({
        faceValue: 0,
        purchaseRatePercent: 20,
        attested: true,
      }) || '',
      /greater than zero/
    )
    assert.match(
      validateSupplierOffer({
        faceValue: 100,
        purchaseRatePercent: 101,
        attested: true,
      }) || '',
      /between/
    )
    assert.match(
      validateSupplierOffer({
        faceValue: 100,
        purchaseRatePercent: 20,
        attested: false,
      }) || '',
      /attestation/
    )
    assert.equal(
      validateSupplierOffer({
        faceValue: 100,
        purchaseRatePercent: 20,
        attested: true,
      }),
      null
    )
  })

  test('labels derived exhausted and expired states explicitly', () => {
    assert.equal(STATUS_LABELS.exhausted, 'Exhausted')
    assert.equal(STATUS_LABELS.expired, 'Expired')
    assert.equal(contributionStatusTone('active'), 'success')
    assert.equal(contributionStatusTone('revoked'), 'danger')
  })

  test('expiry is submitted at the end of the selected local day', () => {
    const value = parseExpiry('2026-09-30')
    assert.equal(new Date(value * 1000).getHours(), 23)
    assert.equal(parseExpiry(''), 0)
  })
})
