/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, expect, test } from 'bun:test'

import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../channel-form'

// The backend stores these as *int so that an explicit 0 clears the limit and
// an absent value is preserved (GORM Updates skips zero-value struct fields).
// The form has to respect that distinction or the UI silently cannot remove a
// limit -- the field would accept 0, save, and come back unchanged.

const form = (over: Partial<ChannelFormValues> = {}): ChannelFormValues => ({
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'test',
  ...over,
})

describe('channel rate limit fields survive the form round trip', () => {
  test('a limit set in the form reaches the create payload', () => {
    const { channel } = transformFormDataToCreatePayload(
      form({ rate_limit_rpm: 4000, rate_limit_tpm: 60_000_000 })
    )
    expect(channel.rate_limit_rpm).toBe(4000)
    expect(channel.rate_limit_tpm).toBe(60_000_000)
  })

  test('a limit set in the form reaches the UPDATE payload', () => {
    // The path that matters: an operator opens an existing channel and types a
    // limit. Omitting the fields here would render a working-looking form whose
    // value is never sent.
    const payload = transformFormDataToUpdatePayload(
      form({ rate_limit_rpm: 1234, rate_limit_tpm: 5678 }),
      42
    )
    expect(payload.rate_limit_rpm).toBe(1234)
    expect(payload.rate_limit_tpm).toBe(5678)
  })

  test('clearing a limit to 0 is sent as 0, not dropped', () => {
    // `|| null` here instead of `?? 0` would make 0 indistinguishable from
    // "not sent", and GORM would preserve the previous value: the limit could
    // never be removed.
    const payload = transformFormDataToUpdatePayload(
      form({ rate_limit_rpm: 0, rate_limit_tpm: 0 }),
      42
    )
    expect(payload.rate_limit_rpm).toBe(0)
    expect(payload.rate_limit_tpm).toBe(0)
  })

  test('an existing channel loads its limits into the form', () => {
    const defaults = transformChannelToFormDefaults({
      id: 1,
      name: 'c',
      type: 1,
      channel_info: {},
      rate_limit_rpm: 4000,
      rate_limit_tpm: 60_000_000,
    } as never)
    expect(defaults.rate_limit_rpm).toBe(4000)
    expect(defaults.rate_limit_tpm).toBe(60_000_000)
  })

  test('a channel with no limit loads as 0, meaning unlimited', () => {
    const defaults = transformChannelToFormDefaults({
      id: 1,
      name: 'c',
      type: 1,
      channel_info: {},
    } as never)
    expect(defaults.rate_limit_rpm).toBe(0)
    expect(defaults.rate_limit_tpm).toBe(0)
  })
})
