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
import { parseExcludedGroups, splitChannelGroups } from '../channel-groups'

// Every customer group can use every channel, so blocking one customer from
// one channel is its own setting. The screens must stop listing a blocked
// group as having access, and the form must both save the block and load it
// back -- a block that saves but does not load is silently removed by the next
// unrelated edit to the channel.

const pricing = ['default', 'friend solo', 'GenAI', 'Kingdee']

const asChannel = (settings: string) =>
  ({ type: 1, settings, channel_info: {} }) as unknown as Channel

const savedSettings = (
  over: Partial<typeof CHANNEL_FORM_DEFAULT_VALUES>
): Record<string, unknown> =>
  JSON.parse(
    transformFormDataToCreatePayload({
      ...CHANNEL_FORM_DEFAULT_VALUES,
      ...over,
    }).channel.settings ?? '{}'
  )

describe('blocked customer groups on a channel', () => {
  test('a blocked group is shown neither as own nor as inherited', () => {
    assert.deepEqual(splitChannelGroups('default', pricing, ['friend solo']), {
      own: ['default'],
      inherited: ['GenAI', 'Kingdee'],
    })
    assert.deepEqual(
      splitChannelGroups('default,friend solo', pricing, ['friend solo']),
      { own: ['default'], inherited: ['GenAI', 'Kingdee'] },
      'the server drops a blocked group even from the own list'
    )
  })

  test('the block list is read from the channel settings', () => {
    assert.deepEqual(
      parseExcludedGroups('{"excluded_groups":[" friend solo ","",7]}'),
      ['friend solo']
    )
    assert.deepEqual(parseExcludedGroups('{}'), [])
    assert.deepEqual(parseExcludedGroups('not json'), [])
    assert.deepEqual(parseExcludedGroups(undefined), [])
  })

  test('the form saves the block list into settings', () => {
    assert.deepEqual(
      savedSettings({ excluded_groups: ['friend solo', 'Kingdee'] })
        .excluded_groups,
      ['friend solo', 'Kingdee']
    )
  })

  test('the form loads a saved block list back', () => {
    const form = transformChannelToFormDefaults(
      asChannel('{"excluded_groups":["friend solo"]}')
    )
    assert.deepEqual(form.excluded_groups, ['friend solo'])
  })

  test('clearing the block list removes it from settings', () => {
    const settings = savedSettings({
      settings: '{"excluded_groups":["friend solo"]}',
      excluded_groups: [],
    })
    assert.equal('excluded_groups' in settings, false)
  })

  test('saving a block keeps the settings the channel already had', () => {
    const settings = savedSettings({
      settings:
        '{"system_one_path":"/x","upstream_model_update_ignored_models":["a"]}',
      type: 61,
      system_one_path: '/x',
      excluded_groups: ['friend solo'],
    })
    assert.equal(settings.system_one_path, '/x')
    assert.deepEqual(settings.excluded_groups, ['friend solo'])
  })
})
