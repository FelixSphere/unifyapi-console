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
    expect(indexHtml).toMatch(
      /<link rel="icon" type="image\/svg\+xml" href="\/icon\.svg\?v=\d{4}-\d{2}-\d{2}" \/>/
    )
    expect(indexHtml).toMatch(
      /<link rel="icon" href="\/favicon\.ico\?v=\d{4}-\d{2}-\d{2}" sizes="32x32" \/>/
    )
    expect(indexHtml).toMatch(
      /\/apple-touch-icon-180\.png\?v=\d{4}-\d{2}-\d{2}/
    )
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
