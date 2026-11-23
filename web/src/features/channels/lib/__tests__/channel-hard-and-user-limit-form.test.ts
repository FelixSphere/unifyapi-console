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
  parseHardRateLimit,
  parseUserRateLimit,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
  transformFormDataToUpdatePayload,
  type ChannelFormValues,
} from '../channel-form'

const form = (over: Partial<ChannelFormValues> = {}): ChannelFormValues => ({
  ...CHANNEL_FORM_DEFAULT_VALUES,
  name: 'test',
  ...over,
})

// UNIFYAPI-FORK: the hard cap lives in the settings JSON, beside whatever the
// channel already keeps there.
describe('the hard cap switch round trips through settings', () => {
  const settingsOf = (values: ChannelFormValues) =>
    JSON.parse(transformFormDataToUpdatePayload(values, 42).settings ?? '{}')

  test('it is off by default and writes nothing', () => {
    expect(CHANNEL_FORM_DEFAULT_VALUES.hard_rate_limit).toBe(false)
    expect('hard_rate_limit' in settingsOf(form())).toBe(false)
  })

  test('turning it on reaches the create and the update payload', () => {
    const on = form({ rate_limit_rpm: 100, hard_rate_limit: true })
    expect(settingsOf(on).hard_rate_limit).toBe(true)
    const { channel } = transformFormDataToCreatePayload(on)
    expect(JSON.parse(channel.settings ?? '{}').hard_rate_limit).toBe(true)
  })

  test('turning it off removes the key and keeps the other settings', () => {
    const settings = settingsOf(
      form({
        settings: '{"hard_rate_limit":true,"excluded_groups":["a"]}',
        excluded_groups: ['a'],
        hard_rate_limit: false,
      })
    )
    expect('hard_rate_limit' in settings).toBe(false)
    expect(settings.excluded_groups).toEqual(['a'])
  })

  test('an existing channel opens with the switch as stored', () => {
    expect(parseHardRateLimit('{"hard_rate_limit":true}')).toBe(true)
    expect(parseHardRateLimit('{"hard_rate_limit":"yes"}')).toBe(false)
    expect(parseHardRateLimit('{"excluded_groups":["a"]}')).toBe(false)
    expect(parseHardRateLimit('not json')).toBe(false)
    expect(parseHardRateLimit(null)).toBe(false)
    const opened = transformChannelToFormDefaults({
      id: 1,
      name: 'c',
      type: 1,
      settings: '{"hard_rate_limit":true}',
      channel_info: {},
    } as Parameters<typeof transformChannelToFormDefaults>[0])
    expect(opened.hard_rate_limit).toBe(true)
  })
})

// UNIFYAPI-FORK: the per-user limit is one object in the settings JSON.
describe('the per-user limit round trips through settings', () => {
  const settingsOf = (values: ChannelFormValues) =>
    JSON.parse(transformFormDataToUpdatePayload(values, 42).settings ?? '{}')

  test('it is off by default and writes nothing', () => {
    const settings = settingsOf(form())
    expect('user_rate_limit' in settings).toBe(false)
    expect('user_limit_bucket' in settings).toBe(false)
  })

  test('only the dimensions that are set are written', () => {
    const settings = settingsOf(
      form({
        user_limit_rpm: 20,
        user_limit_daily_tokens: 20_000_000,
        user_limit_bucket: ' mythos-direct ',
      })
    )
    expect(settings.user_rate_limit).toEqual({
      rpm: 20,
      daily_tokens: 20_000_000,
    })
    expect(settings.user_limit_bucket).toBe('mythos-direct')
  })

  test('clearing every dimension removes the limit and the bucket', () => {
    const settings = settingsOf(
      form({
        settings:
          '{"user_rate_limit":{"rpm":20},"user_limit_bucket":"m","hard_rate_limit":true}',
        hard_rate_limit: true,
      })
    )
    expect('user_rate_limit' in settings).toBe(false)
    expect('user_limit_bucket' in settings).toBe(false)
    expect(settings.hard_rate_limit).toBe(true)
  })

  test('an existing channel opens with its limit', () => {
    expect(
      parseUserRateLimit(
        '{"user_rate_limit":{"rpm":20,"tpm":400000,"daily_requests":2000,"daily_tokens":20000000},"user_limit_bucket":"mythos-direct"}'
      )
    ).toEqual({
      user_limit_rpm: 20,
      user_limit_tpm: 400000,
      user_limit_daily_requests: 2000,
      user_limit_daily_tokens: 20000000,
      user_limit_bucket: 'mythos-direct',
    })
    expect(
      parseUserRateLimit('{"user_rate_limit":{"rpm":-1}}').user_limit_rpm
    ).toBe(0)
    expect(parseUserRateLimit('not json').user_limit_rpm).toBe(0)
    expect(parseUserRateLimit(null).user_limit_bucket).toBe('')
  })
})
