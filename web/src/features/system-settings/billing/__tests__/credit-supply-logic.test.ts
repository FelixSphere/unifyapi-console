/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import type { CreditLot } from '../credit-supply-api'
import {
  availableTransitions,
  consumedPct,
  formatRate,
  formatUSD,
  lotHealth,
  payableUSD,
  remainingUSD,
  timestampFromInput,
  dateTimeInputValue,
} from '../credit-supply-logic'

const NOW = 1_800_000_000

function lot(overrides: Partial<CreditLot> = {}): CreditLot {
  return {
    id: 1,
    supplier_id: 1,
    vendor: 'anthropic',
    channel_id: 7,
    face_value_usd: 1000,
    acquisition_rate: 0.45,
    consumed_usd: 250,
    unpriced_requests: 0,
    low_water_usd: 100,
    low_water_notified_at: 0,
    expires_at: 0,
    status: 'active',
    source: 'admin',
    note: '',
    status_reason: '',
    attestation_version: '2026-09-credit-supply-v1',
    attested_at: NOW - 86400,
    attested_by: 'root',
    approved_by: '',
    approved_at: 0,
    retired_at: 0,
    created_at: NOW - 86400,
    updated_at: NOW,
    ...overrides,
  }
}

describe('credit supply arithmetic', () => {
  test('remaining never goes negative and payable follows the rate', () => {
    assert.equal(remainingUSD(lot()), 750)
    assert.equal(remainingUSD(lot({ consumed_usd: 1200 })), 0)
    assert.ok(Math.abs(payableUSD(lot()) - 112.5) < 1e-9)
    assert.equal(consumedPct(lot()), 25)
    assert.equal(consumedPct(lot({ consumed_usd: 5000 })), 100)
    assert.equal(consumedPct(lot({ face_value_usd: 0 })), 0)
  })

  test('money and rates format the way they are quoted', () => {
    assert.equal(formatUSD(1234.5), '$1,234.50')
    assert.equal(formatUSD(0.0042), '$0.0042')
    assert.equal(formatUSD(-3), '-$3.00')
    assert.equal(formatRate(0.45), '45¢ / $1')
    assert.equal(formatRate(0.333), '33.3¢ / $1')
    assert.equal(formatRate(1), '100¢ / $1')
  })
})

describe('lot health', () => {
  test('status wins over balance', () => {
    assert.equal(lotHealth(lot({ status: 'pending' }), NOW), 'pending')
    assert.equal(lotHealth(lot({ status: 'suspended' }), NOW), 'suspended')
    assert.equal(lotHealth(lot({ status: 'exhausted' }), NOW), 'retired')
    assert.equal(lotHealth(lot({ status: 'expired' }), NOW), 'retired')
    assert.equal(lotHealth(lot({ status: 'rejected' }), NOW), 'rejected')
  })

  test('active lots are low at the low-water mark and expiring inside a week', () => {
    assert.equal(lotHealth(lot(), NOW), 'healthy')
    assert.equal(lotHealth(lot({ consumed_usd: 900 }), NOW), 'low')
    assert.equal(
      lotHealth(lot({ low_water_usd: 0, consumed_usd: 999 }), NOW),
      'healthy'
    )
    assert.equal(
      lotHealth(lot({ expires_at: NOW + 3 * 86400 }), NOW),
      'expiring'
    )
    assert.equal(
      lotHealth(lot({ expires_at: NOW + 30 * 86400 }), NOW),
      'healthy'
    )
  })
})

describe('available transitions mirror the server lifecycle', () => {
  test('pending can be approved only once bound, or rejected', () => {
    const bound = availableTransitions(lot({ status: 'pending' }), NOW)
    assert.deepEqual(
      bound.map((t) => t.to),
      ['active', 'rejected']
    )
    assert.equal(bound[0]?.blockedKey, undefined)
    const unbound = availableTransitions(
      lot({ status: 'pending', channel_id: 0 }),
      NOW
    )
    assert.match(unbound[0]?.blockedKey ?? '', /channel/)
  })

  test('active suspends, suspended reactivates, rejected is terminal', () => {
    assert.deepEqual(
      availableTransitions(lot(), NOW).map((t) => t.to),
      ['suspended']
    )
    assert.deepEqual(
      availableTransitions(lot({ status: 'suspended' }), NOW).map((t) => t.to),
      ['active']
    )
    assert.deepEqual(availableTransitions(lot({ status: 'rejected' }), NOW), [])
  })

  test('retired lots reactivate only after the blocking fact is fixed', () => {
    const exhausted = availableTransitions(
      lot({ status: 'exhausted', consumed_usd: 1000 }),
      NOW
    )
    assert.match(exhausted[0]?.blockedKey ?? '', /face value/)
    const toppedUp = availableTransitions(
      lot({ status: 'exhausted', consumed_usd: 1000, face_value_usd: 1500 }),
      NOW
    )
    assert.equal(toppedUp[0]?.blockedKey, undefined)

    const expired = availableTransitions(
      lot({ status: 'expired', expires_at: NOW - 10 }),
      NOW
    )
    assert.match(expired[0]?.blockedKey ?? '', /expiry/)
    const extended = availableTransitions(
      lot({ status: 'expired', expires_at: NOW + 86400 }),
      NOW
    )
    assert.equal(extended[0]?.blockedKey, undefined)
  })
})

describe('datetime-local round trip', () => {
  test('empty means no expiry, otherwise whole seconds in local time', () => {
    assert.equal(timestampFromInput(''), 0)
    assert.equal(timestampFromInput('not a date'), 0)
    const value = dateTimeInputValue(NOW)
    assert.match(value ?? '', /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    // datetime-local carries no seconds, so the round trip is minute-exact.
    assert.equal(timestampFromInput(value), NOW - (NOW % 60))
    assert.equal(dateTimeInputValue(0), '')
  })
})
