/*
UNIFYAPI-FORK: the pricing tabs must not leak English into a Chinese console.

Both of these shipped wrong once and were only caught by eye:

  * the whole "官方报价与折扣" tab rendered in English because the keys had no
    zh entries at all;
  * the upstream-cost count column reused the generic `Models` key, whose
    existing translation is 「模型」 — so a column of counts was headed "模型"
    and "3" read as a model named 3.

An untranslated key falls back to the English key itself, silently and without
any console warning, so nothing but a test catches it.
*/
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, test } from 'node:test'

const HERE = new URL('.', import.meta.url).pathname
const MODELS_DIR = join(HERE, '..')
const LOCALES_DIR = join(HERE, '../../../../i18n/locales')

/** The fork-owned pricing tabs. */
const TAB_SOURCES = [
  'baseline-pricing-tab.tsx',
  'channel-cost-tab.tsx',
]

/** Locales that must carry a real translation, not the English fallback. */
const TRANSLATED_LOCALES = ['zh.json', 'zh-TW.json']

/**
 * extractKeys pulls the literal `t('…')` arguments out of a source file.
 *
 * Literals only: a computed key cannot be checked statically, and neither of
 * these files uses one. If that changes the test will simply not cover it,
 * which is why the tabs keep their strings inline.
 */
function extractKeys(source: string): string[] {
  const keys = new Set<string>()
  // t('…') and t(\n  '…'\n) — the formatter wraps long strings onto their own line.
  for (const match of source.matchAll(/\bt\(\s*'((?:[^'\\]|\\.)*)'/g)) {
    keys.add(match[1].replaceAll("\\'", "'"))
  }
  return [...keys]
}

function loadNamespace(file: string): Record<string, string> {
  const raw = JSON.parse(readFileSync(join(LOCALES_DIR, file), 'utf8'))
  // The locale files are namespaced; the pricing strings live in whichever
  // namespace already holds the sibling pricing keys.
  for (const value of Object.values(raw)) {
    if (value && typeof value === 'object' && 'Model prices' in (value as object)) {
      return value as Record<string, string>
    }
  }
  throw new Error(`no pricing namespace found in ${file}`)
}

describe('pricing tab translations', () => {
  test('the locale directory and tab sources are where the test expects', () => {
    const locales = readdirSync(LOCALES_DIR)
    assert.ok(locales.includes('zh.json'), `zh.json not found in ${LOCALES_DIR}`)
    for (const file of TAB_SOURCES) {
      assert.ok(
        readFileSync(join(MODELS_DIR, file), 'utf8').length > 0,
        `${file} is empty or missing`
      )
    }
  })

  for (const file of TAB_SOURCES) {
    const keys = extractKeys(readFileSync(join(MODELS_DIR, file), 'utf8'))

    test(`${file} uses translatable string literals`, () => {
      // A tab with no extracted keys means the regex stopped matching how the
      // file is written, and every assertion below would vacuously pass.
      assert.ok(keys.length >= 8, `only ${keys.length} keys found in ${file}`)
    })

    for (const locale of TRANSLATED_LOCALES) {
      test(`${file} is fully translated in ${locale}`, () => {
        const ns = loadNamespace(locale)
        const missing = keys.filter((key) => !(key in ns))
        assert.deepEqual(
          missing,
          [],
          `these keys have no ${locale} entry and would render as English:\n  ${missing.join('\n  ')}`
        )

        // A key present but left equal to itself is the same bug wearing a
        // disguise -- except for keys that are legitimately identical across
        // languages, i.e. ones carrying no letters (an interpolation like
        // "-{{percent}}%").
        const untranslated = keys.filter(
          (key) => ns[key] === key && /[A-Za-z]{2,}/.test(key.replaceAll(/\{\{.*?\}\}/g, ''))
        )
        assert.deepEqual(
          untranslated,
          [],
          `these keys are still the English source in ${locale}:\n  ${untranslated.join('\n  ')}`
        )
      })
    }
  }

  // The specific mistake that shipped: a count column reusing the generic
  // `Models` key, which already meant 「模型」.
  test('the channel count column does not reuse the generic Models key', () => {
    const source = readFileSync(join(MODELS_DIR, 'channel-cost-tab.tsx'), 'utf8')
    assert.ok(
      !/t\('Models'\)/.test(source),
      "the upstream-cost table shows a COUNT of models; `t('Models')` translates to 「模型」 " +
        "and heads the column as if it listed them. Use `t('Model count')`."
    )
    assert.ok(source.includes("t('Model count')"), "expected t('Model count')")

    const zh = loadNamespace('zh.json')
    assert.notEqual(zh['Model count'], zh['Models'], 'the two must read differently')
  })

  // The discount column's header is what the runbook and every support answer
  // point at, so it is pinned rather than left to drift.
  test('the discount column header is stable', () => {
    const zh = loadNamespace('zh.json')
    assert.equal(zh['Discount'], '优惠')
    assert.equal(zh['Official in/out'], '官方价 入/出')
    assert.equal(zh['Official price & discount'], '官方报价与折扣')
    assert.equal(zh['Upstream cost'], '上游采购成本')
    assert.equal(zh['Max safe customer discount'], '客户折扣下限')
  })
})
