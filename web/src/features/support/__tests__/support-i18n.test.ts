/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * The Support page must read in every language the console offers
 * (operator request 2026-10-13: "界面也有支持多语言，不只是中文，英文").
 *
 * Its strings live in features/support/i18n.ts, not the locale JSON files, so
 * the usual sync tooling does not see them. Two ways that goes wrong silently:
 *   1. someone adds t('New text') to the page and no table has it -- every
 *      non-English user sees English for that one string;
 *   2. a translation drops the {{id}} placeholder and the user number vanishes.
 * Both are checked here against the page source and against i18next itself.
 */
import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import i18n from '@/i18n/config'
import { resources } from '@/i18n/config'

import { SUPPORT_KEYS, SUPPORT_TRANSLATIONS } from '../i18n'

const PAGE = readFileSync(join(import.meta.dir, '..', 'index.tsx'), 'utf8')

// Every string the page translates: t('literal') plus the label constants it
// passes to t() by variable (status pills and inbox filters).
function translatedStrings(src: string): string[] {
  const found = new Set<string>()
  for (const m of src.matchAll(/\bt\(\s*'((?:[^'\\]|\\.)*)'/g)) found.add(m[1])
  for (const m of src.matchAll(
    /^\s*(?:open|answered|closed|label): '((?:[^'\\]|\\.)*)'/gm
  ))
    found.add(m[1])
  return [...found]
}

describe('Support page translations', () => {
  test('the page translates the strings it should', () => {
    const strings = translatedStrings(PAGE)
    expect(strings.length).toBeGreaterThan(30)
    expect(strings).toContain('Waiting on customer') // via a label constant
  })

  test('every string the page translates has a table entry', () => {
    const missing = translatedStrings(PAGE).filter(
      (s) => !(SUPPORT_KEYS as readonly string[]).includes(s)
    )
    expect(missing).toEqual([])
  })

  test('every console language except English has a table', () => {
    const languages = Object.keys(resources).filter((l) => l !== 'en')
    expect(Object.keys(SUPPORT_TRANSLATIONS).sort()).toEqual(languages.sort())
  })

  for (const [lng, table] of Object.entries(SUPPORT_TRANSLATIONS)) {
    test(`${lng}: every key is translated and keeps its placeholders`, () => {
      for (const key of SUPPORT_KEYS) {
        const value = table[key]
        expect(value?.trim().length ?? 0).toBeGreaterThan(0)
        for (const ph of key.match(/{{\w+}}/g) ?? []) {
          expect(value).toContain(ph)
        }
      }
    })
  }

  test('translated server messages match the Go source word for word', () => {
    // If the server's wording changes, the translation silently stops
    // matching and the customer sees English again.
    const repo = join(import.meta.dir, '..', '..', '..', '..', '..')
    const go =
      readFileSync(join(repo, 'controller/unifyapi_support.go'), 'utf8') +
      readFileSync(join(repo, 'model/unifyapi_support.go'), 'utf8')
    const serverKeys = SUPPORT_KEYS.filter(
      (k) => /^[a-z]/.test(k) && !k.includes('{{')
    )
    expect(serverKeys.length).toBe(4)
    for (const k of serverKeys) expect(go).toContain(`"${k}"`)
  })

  test('i18next serves the tables, and upstream translations still win', () => {
    expect(i18n.t('Support', { lng: 'zhCN' })).toBe('客服支持')
    expect(i18n.t('Support', { lng: 'ja' })).toBe('サポート')
    expect(i18n.t('user #{{id}}', { lng: 'ru', id: 7 })).toBe('пользователь №7')
    expect(i18n.t('Support', { lng: 'en' })).toBe('Support')
    // A key upstream already translates keeps upstream's text.
    expect(i18n.t('Close', { lng: 'zhCN' })).toBe(
      (resources.zhCN as { translation: Record<string, string> }).translation
        .Close
    )
  })
})
