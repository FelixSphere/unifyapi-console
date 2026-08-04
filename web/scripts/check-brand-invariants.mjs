/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * Fails the build if this fork's AGPLv3 s.7(b) attribution obligations, or the
 * brand overrides that depend on them, have been broken.
 *
 * This exists because the attribution is unusually easy to destroy by accident:
 *   - Upstream mounts <Footer /> on exactly one page and skips it whenever the
 *     HomePageContent option is set, so we carry the notice ourselves in two
 *     layouts. A merge that drops either mount silently removes attribution
 *     from every page.
 *   - The i18n key is deliberately obfuscated (built at runtime from
 *     'new' + 'api', stored unicode-escaped in the locale files) so a bulk
 *     brand rename cannot find it. A prettifier that normalises the escape
 *     makes it findable, and the next rename deletes a licence-required string.
 *
 * Run: node scripts/check-brand-invariants.mjs
 */
import { readFileSync, readdirSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const UPSTREAM_URL = 'https://github.com/QuantumNous/new-api'

const failures = []

function read(relativePath) {
  return readFileSync(join(webRoot, relativePath), 'utf8')
}

function check(description, condition) {
  if (!condition) failures.push(description)
}

// 1. The notice and the required upstream link still live in upstream's footer,
//    which is the single source of truth our bar re-uses.
const footer = read('src/components/layout/components/footer.tsx')
check(
  `footer.tsx must contain a visible link to ${UPSTREAM_URL}`,
  footer.includes(UPSTREAM_URL)
)
check(
  'footer.tsx must still build the attribution i18n key from split literals',
  /\[\s*'footer'\s*,\s*'new'\s*\+\s*'api'\s*,\s*'projectAttributionSuffix'\s*,?\s*\]/.test(
    footer
  )
)
check(
  'footer.tsx must export ProjectAttribution for the always-mounted bar',
  /export\s+function\s+ProjectAttribution/.test(footer)
)

// 2. Our carrier renders upstream's component rather than re-wording the notice.
const carrier = read('src/brand/upstream-attribution.tsx')
check(
  'upstream-attribution.tsx must render upstream ProjectAttribution',
  carrier.includes('ProjectAttribution') &&
    /from\s+'@\/components\/layout\/components\/footer'/.test(carrier)
)

// 3. Both layouts must mount it, or whole route trees lose attribution.
for (const layout of ['authenticated-layout', 'public-layout']) {
  const source = read(`src/components/layout/components/${layout}.tsx`)
  check(
    `${layout}.tsx must mount <UpstreamAttribution />`,
    source.includes('<UpstreamAttribution />') &&
      source.includes('@/brand/upstream-attribution')
  )
}

// 4. Every locale must keep the escaped key. A normalised escape is itself a
//    failure: it means something rewrote these files and the next bulk rename
//    would strip the notice.
const localeDir = 'src/i18n/locales'
const locales = readdirSync(join(webRoot, localeDir)).filter((f) =>
  f.endsWith('.json')
)
check(
  `expected 7 locale files in ${localeDir}, found ${locales.length}`,
  locales.length === 7
)
for (const locale of locales) {
  const source = read(`${localeDir}/${locale}`)
  check(
    `${locale} must keep the unicode-escaped attribution key`,
    source.includes('footer.new\\u0061pi.projectAttributionSuffix')
  )
  check(
    `${locale} must not contain a normalised attribution key (something rewrote it)`,
    !source.includes('footer.newapi.projectAttributionSuffix')
  )
}

// 5. Brand overrides that silently degrade rather than error if lost.
const main = read('src/main.tsx')
check(
  "main.tsx must import ./styles/unifyapi.css (not index.css) or the rebrand won't load",
  main.includes("import './styles/unifyapi.css'")
)
check(
  "main.tsx must pin ThemeProvider to defaultTheme='light' or charts/toasts render dark",
  /defaultTheme='light'/.test(main)
)

// 6. The attribution may be de-emphasised but never made unreadable.
//
// AGPLv3 s.7(b) lets us present the notice quietly; it does not let us hide it.
// unifyapi.css scopes a rule to the required upstream link, so this asserts that
// rule only ever shrinks text -- never removes it from the page or drops its
// contrast to nothing. Catching this in CI matters because the visual result of
// crossing the line is indistinguishable from "the footer looks tidier now".
const brandCss = read('src/styles/unifyapi.css')
const attributionRule =
  brandCss.match(
    /:has\(> a\[href='https:\/\/github\.com\/QuantumNous\/new-api'\]\)\s*\{([^}]*)\}/
  )?.[1] ?? ''
for (const forbidden of [
  'display:',
  'visibility:',
  'opacity:',
  'clip-path:',
  'text-indent:',
  'position:',
]) {
  check(
    `unifyapi.css attribution rule must not set ${forbidden} -- de-emphasising is allowed, hiding is not`,
    !attributionRule.includes(forbidden)
  )
}
const fontSize = attributionRule.match(/font-size:\s*([\d.]+)px/)?.[1]
check(
  'unifyapi.css attribution rule must keep font-size at 9px or larger to stay readable',
  fontSize === undefined || Number(fontSize) >= 9
)

if (failures.length > 0) {
  console.error('brand-invariants: FAILED\n')
  for (const failure of failures) console.error(`  - ${failure}`)
  console.error(
    '\nSee BRANDING.md. The attribution checks are licence obligations under' +
      '\nAGPLv3 s.7(b) and the upstream NOTICE file -- do not satisfy them by' +
      '\nremoving the check.'
  )
  process.exit(1)
}

console.log(
  `brand-invariants: OK (${locales.length} locales, 2 layout mounts, upstream link intact)`
)
