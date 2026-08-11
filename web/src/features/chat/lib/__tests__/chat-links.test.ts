/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { parseChatConfig } from '../chat-links'

describe('parseChatConfig', () => {
  test('drops a scheme-less value instead of offering a same-origin link', () => {
    // "ccswitch" shipped as a bare token: the browser resolved it against the
    // console's own origin and served the SPA 404 at /ccswitch.
    const presets = parseChatConfig([{ 'CC Switch': 'ccswitch' }])
    assert.deepEqual(presets, [])
  })

  test('keeps real custom protocols', () => {
    const presets = parseChatConfig([
      { 'Cherry Studio': 'cherrystudio://providers/api-keys?v=1' },
      { OpenCat: 'opencat://team/join?domain={address}&token={key}' },
    ])
    assert.deepEqual(
      presets.map((p) => [p.name, p.type]),
      [
        ['Cherry Studio', 'custom-protocol'],
        ['OpenCat', 'custom-protocol'],
      ]
    )
  })

  test('keeps the fluent entry, which is handled by its own branch', () => {
    // fluentread is also scheme-less but is never navigated to directly, so the
    // guard must not take it out.
    const presets = parseChatConfig([{ 流畅阅读: 'fluentread' }])
    assert.deepEqual(
      presets.map((p) => [p.name, p.type]),
      [['流畅阅读', 'fluent']]
    )
  })

  test('surviving presets keep the id of their configured position', () => {
    // The sidebar links with preset.id and the /chat/$chatId route matches on
    // it, so an id must stay pinned to its entry when something ahead of it is
    // dropped. Renumbering here would send a click to a different client.
    const presets = parseChatConfig([
      { 'Cherry Studio': 'cherrystudio://x' },
      { 'CC Switch': 'ccswitch' },
      { DeepChat: 'deepchat://provider/install' },
      { OpenCat: 'opencat://team/join' },
    ])
    assert.deepEqual(
      presets.map((p) => [p.id, p.name]),
      [
        ['0', 'Cherry Studio'],
        ['2', 'DeepChat'],
        ['3', 'OpenCat'],
      ]
    )
  })

  test('keeps web links', () => {
    const presets = parseChatConfig([
      { 'Lobe Chat': 'https://chat-preview.lobehub.com/?settings={}' },
    ])
    assert.equal(presets[0]?.type, 'web')
  })
})
