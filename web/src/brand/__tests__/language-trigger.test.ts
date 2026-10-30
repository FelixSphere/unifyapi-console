/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { INTERFACE_LANGUAGE_OPTIONS } from '@/i18n/languages'

import { LANGUAGE_TRIGGER_LABEL } from '../language-trigger'

describe('language switcher trigger', () => {
  test('every interface language has a short, distinct label', () => {
    const labels = INTERFACE_LANGUAGE_OPTIONS.map(
      (lang) => LANGUAGE_TRIGGER_LABEL[lang.code]
    )
    for (const label of labels) {
      expect(label?.trim().length ?? 0).toBeGreaterThan(0)
      expect(label.length).toBeLessThanOrEqual(3)
    }
    expect(new Set(labels).size).toBe(labels.length)
  })

  // The trigger must name the current language in text and must not use the
  // lucide Languages glyph (文/A), which depicts two specific scripts.
  test('the trigger shows the label and a globe, not the 文/A glyph', () => {
    const source = readFileSync(
      join(import.meta.dir, '..', '..', 'components', 'language-switcher.tsx'),
      'utf8'
    )
    expect(source).toContain('LANGUAGE_TRIGGER_LABEL[')
    expect(source).toContain('<Globe')
    expect(source).not.toMatch(/\bLanguages\b.*from 'lucide-react'/)
  })
})
