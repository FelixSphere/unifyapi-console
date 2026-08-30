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

/** The fork-owned pricing, profit and settlement screens. */
const TAB_SOURCES = [
  'baseline-pricing-tab.tsx',
  'channel-cost-tab.tsx',
  'profit-section.tsx',
  'settlement-section.tsx',
]

/**
 * Logic modules whose string literals are ALSO translation keys.
 *
 * The screens render `t(step.noteKey)` and `t(LABELS[state])` -- keys chosen at
 * runtime, which a scan of `t('…')` calls cannot see. Left uncovered, an
 * untranslated derivation step or status badge renders as English with no
 * warning, which is exactly how the first version of the pricing tab shipped.
 */
const LOGIC_SOURCES = ['profit-logic.ts', 'settlement-logic.ts']

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

/**
 * extractDynamicKeys pulls keys out of a logic module: the `labelKey`/`noteKey`
 * fields of a derivation step, and the values of the exhaustive `…_LABELS`
 * records the screens index by state.
 */
function extractDynamicKeys(source: string): string[] {
  const keys = new Set<string>()
  for (const match of source.matchAll(
    /(?:labelKey|noteKey):\s*\n?\s*'((?:[^'\\]|\\.)*)'/g
  )) {
    keys.add(match[1].replaceAll("\\'", "'"))
  }
  for (const block of source.matchAll(/_LABELS: Record<[^>]+> = \{([\s\S]*?)\n\}/g)) {
    for (const entry of block[1].matchAll(/:\s*'((?:[^'\\]|\\.)*)'/g)) {
      keys.add(entry[1].replaceAll("\\'", "'"))
    }
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

  for (const file of LOGIC_SOURCES) {
    const keys = extractDynamicKeys(readFileSync(join(MODELS_DIR, file), 'utf8'))

    test(`${file} exposes its runtime-chosen keys`, () => {
      // Zero keys means the extractor stopped matching how the file is
      // written, and every assertion below would vacuously pass.
      assert.ok(keys.length >= 3, `only ${keys.length} dynamic keys found in ${file}`)
    })

    for (const locale of TRANSLATED_LOCALES) {
      test(`${file}'s runtime keys are translated in ${locale}`, () => {
        const ns = loadNamespace(locale)
        const missing = keys.filter((key) => !(key in ns))
        assert.deepEqual(
          missing,
          [],
          `these runtime-chosen keys have no ${locale} entry:\n  ${missing.join('\n  ')}`
        )
      })
    }
  }

  /*
   * A runtime-chosen key must come from a `…_LABELS` record or a derivation
   * step, never from a helper that returns string literals.
   *
   * This is the general form of a bug found by eye during local verification:
   * three button labels were refactored into `primaryActionKey()`, which made
   * them invisible to BOTH extractors above — the `t('…')` scan no longer saw a
   * literal, and the `_LABELS` scan had nothing to find. They rendered in
   * English. Forbidding `t(someHelper(...))` outright keeps every runtime key
   * somewhere a scan can reach.
   */
  for (const file of TAB_SOURCES) {
    test(`${file} routes runtime keys through a record, not a helper`, () => {
      const source = readFileSync(join(MODELS_DIR, file), 'utf8')
      const offenders = [...source.matchAll(/\bt\(\s*([a-z][A-Za-z0-9_]*)\(/g)].map(
        (match) => match[1]
      )
      assert.deepEqual(
        offenders,
        [],
        `${file} calls t() on the result of ${offenders.join(', ')}. ` +
          'String literals returned from a helper cannot be checked for a translation. ' +
          'Move them into an exhaustive `…_LABELS: Record<…>` and index it at the call site.'
      )
    })
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
