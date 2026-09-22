/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import type { Channel } from '../../types'
import {
  CHANNEL_FORM_DEFAULT_VALUES,
  transformChannelToFormDefaults,
  transformFormDataToCreatePayload,
} from '../channel-form'

// The System One path tells the relay where an aggregator mounts TypeSafe's
// evaluation endpoint. A setting that loads into the form but never saves, or
// saves but never loads, is silent data loss: the operator types a path, sees
// it accepted, and the channel keeps calling the wrong URL.

const TYPESAFE = 61

// transformChannelToFormDefaults reads channel_info, so a bare literal is not
// enough of a Channel to hand it.
const asChannel = (settings: string) =>
  ({ type: TYPESAFE, settings, channel_info: {} }) as unknown as Channel

// The settings JSON a channel is saved with. buildSettingsJSON, where the
// type-specific fields live, is not exported, so go through the payload the
// drawer actually submits.
const savedSettings = (
  over: Partial<typeof CHANNEL_FORM_DEFAULT_VALUES>
): string =>
  transformFormDataToCreatePayload({ ...CHANNEL_FORM_DEFAULT_VALUES, ...over })
    .channel.settings ?? '{}'

describe('TypeSafe System One endpoint path', () => {
  test('round-trips through the form for a TypeSafe channel', () => {
    const saved = savedSettings({
      type: TYPESAFE,
      system_one_path: '/decide/v1/systemone',
    })
    assert.equal(JSON.parse(saved).system_one_path, '/decide/v1/systemone')

    const form = transformChannelToFormDefaults(asChannel(saved))
    assert.equal(form.system_one_path, '/decide/v1/systemone')
  })

  test('an empty path is not written, so the relay uses the vendor default', () => {
    const saved = savedSettings({ type: TYPESAFE, system_one_path: '' })
    assert.equal('system_one_path' in JSON.parse(saved), false)
  })

  test('the setting does not leak onto another channel type', () => {
    const saved = savedSettings({
      type: 1,
      system_one_path: '/decide/v1/systemone',
    })
    assert.equal('system_one_path' in JSON.parse(saved), false)
  })

  test('a channel saved with a path keeps it when the type is switched away and back', () => {
    const saved = savedSettings({
      type: TYPESAFE,
      system_one_path: '/v2/systemone',
    })
    const form = transformChannelToFormDefaults(asChannel(saved))
    const resaved = savedSettings(form)
    assert.equal(JSON.parse(resaved).system_one_path, '/v2/systemone')
  })
})
