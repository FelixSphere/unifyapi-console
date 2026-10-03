/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, expect, it } from 'bun:test'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

// UI-STANDARD.md: the tab icon is the UnifyAI mark from the kit, for every
// product, and is never derived from the `Logo` option. Production shipped the
// "U" tile twice: once because upstream's applyFaviconToDom swapped the kit
// icons for the option on every load, and once more because the cached
// /favicon.ico had no version on its link. Both rules are pinned here.
const webRoot = join(import.meta.dir, '..', '..', '..')
const indexHtml = readFileSync(join(webRoot, 'index.html'), 'utf8')

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    if (statSync(full).isDirectory()) return sourceFiles(full)
    return /\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name) ? [full] : []
  })
}

describe('tab icon comes from the kit only', () => {
  it('index.html links the kit icons with a cache-busting version', () => {
    // UI-STANDARD.md "Tab icon and cache busting": ?v=YYYY-MM-DD[letter],
    // on the SVG, the 32px PNG, the ICO (sizes="any"), apple-touch and the
    // manifest. Whitespace inside a tag is free because the formatter wraps.
    const v = String.raw`\?v=\d{4}-\d{2}-\d{2}[a-z]?`
    const tag = (body: string) =>
      new RegExp(
        String.raw`<link\s+${body.replace(/ /g, String.raw`\s+`)}\s*/>`
      )
    expect(indexHtml).toMatch(
      tag(String.raw`rel="icon" type="image/svg\+xml" href="/icon\.svg${v}"`)
    )
    expect(indexHtml).toMatch(
      tag(
        String.raw`rel="icon" type="image/png" sizes="32x32" href="/favicon-32\.png${v}"`
      )
    )
    expect(indexHtml).toMatch(
      tag(String.raw`rel="icon" sizes="any" href="/favicon\.ico${v}"`)
    )
    expect(indexHtml).toMatch(
      new RegExp(String.raw`/apple-touch-icon-180\.png${v}`)
    )
    expect(indexHtml).toMatch(new RegExp(String.raw`/site\.webmanifest${v}`))
  })

  it('index.html does not point an icon at the Logo option file', () => {
    expect(indexHtml).not.toMatch(/rel="icon"[^>]*logo\.png/)
  })

  it('nothing in src/ defines or calls a runtime favicon rewrite', () => {
    const offenders = sourceFiles(join(webRoot, 'src')).filter((f) =>
      readFileSync(f, 'utf8').includes('applyFaviconToDom(')
    )
    expect(offenders).toEqual([])
  })
})
