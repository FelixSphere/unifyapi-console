/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * The language detector runs every stored or browser-reported code through
 * convertDetectedLanguage. Found in the v0.1.149 release (2026-10-14): the
 * detector caches the CHOSEN language in localStorage as 'zhTW', and on the
 * next load that code was mapped to 'zhCN' -- a Traditional Chinese user was
 * switched to Simplified on every reload. Each code the app itself stores must
 * map back to itself.
 */
import { describe, expect, test } from 'bun:test'

import {
  convertDetectedLanguage,
  INTERFACE_LANGUAGE_OPTIONS,
} from '../languages'

describe('convertDetectedLanguage', () => {
  test('every interface language code survives a reload unchanged', () => {
    for (const { code } of INTERFACE_LANGUAGE_OPTIONS) {
      expect(convertDetectedLanguage(code)).toBe(code)
    }
  })

  test('browser tags for Traditional Chinese map to zhTW', () => {
    for (const tag of [
      'zh-TW',
      'zh-HK',
      'zh-MO',
      'zh-Hant',
      'zh-Hant-TW',
      'zh_TW',
    ]) {
      expect(convertDetectedLanguage(tag)).toBe('zhTW')
    }
  })

  test('other Chinese tags map to zhCN, non-Chinese pass through', () => {
    for (const tag of ['zh', 'zh-CN', 'zh-Hans', 'zh-SG']) {
      expect(convertDetectedLanguage(tag)).toBe('zhCN')
    }
    expect(convertDetectedLanguage('fr-FR')).toBe('fr-FR')
    expect(convertDetectedLanguage('ja')).toBe('ja')
  })
})
