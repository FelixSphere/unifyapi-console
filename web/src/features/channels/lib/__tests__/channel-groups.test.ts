/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { test } from 'bun:test'
import assert from 'node:assert/strict'

import { effectiveChannelGroups, splitChannelGroups } from '../channel-groups'

// The backend routes every pricing group through every channel but leaves the
// channel's stored group list as the operator wrote it. The screens must show
// the operator's list first, then everything the channel inherits, so a card
// that says only "UnifyAI" is never read as "only UnifyAI can use this".

const pricing = [
  'default',
  'GenAI',
  'UnifyAI',
  'Kingdee',
  'Chinhin',
  'Vip User',
  'Builder_hub_2026',
]

test('own groups keep their order; every other pricing group follows, sorted', () => {
  assert.deepEqual(splitChannelGroups('UnifyAI', pricing), {
    own: ['UnifyAI'],
    inherited: [
      'Builder_hub_2026',
      'Chinhin',
      'default',
      'GenAI',
      'Kingdee',
      'Vip User',
    ],
  })
  assert.deepEqual(
    effectiveChannelGroups('Chinhin,GenAI,UnifyAI,Vip User', pricing),
    [
      'Chinhin',
      'GenAI',
      'UnifyAI',
      'Vip User',
      'Builder_hub_2026',
      'default',
      'Kingdee',
    ]
  )
})

test('a channel that already names every pricing group inherits nothing', () => {
  assert.deepEqual(splitChannelGroups(pricing.join(','), pricing).inherited, [])
})

test('the editor form hands over an array; whitespace and duplicates are dropped', () => {
  assert.deepEqual(
    splitChannelGroups([' UnifyAI ', 'UnifyAI', '', 'GenAI'], ['default']),
    {
      own: ['UnifyAI', 'GenAI'],
      inherited: ['default'],
    }
  )
})

test('before the pricing groups have loaded, only the own list is shown', () => {
  assert.deepEqual(splitChannelGroups('UnifyAI', undefined), {
    own: ['UnifyAI'],
    inherited: [],
  })
  assert.deepEqual(splitChannelGroups('', []), { own: [], inherited: [] })
})
