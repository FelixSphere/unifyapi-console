/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { beforeEach, describe, expect, it } from 'bun:test'

import { Window as HappyDomWindow } from 'happy-dom'

const domWindow = new HappyDomWindow({
  url: 'https://console.unifyapi.ai/dashboard',
})
for (const key of ['window', 'document', 'HTMLElement', 'Node'] as const) {
  try {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      writable: true,
      value: (domWindow as unknown as Record<string, unknown>)[key],
    })
  } catch {
    /* already provided by the runtime */
  }
}

const { DEFAULT_LOGO } = await import('@/lib/constants')
const { applyFaviconToDom } = await import('@/lib/dom-utils')

function iconHrefs() {
  return [
    ...document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]'),
  ].map((l) => l.getAttribute('href'))
}

// The tab icon shipped as the "U" tile in production even though index.html
// pointed at the kit's icon.svg: the runtime swapped every <link rel=icon>
// for the Logo option, whose value was the default /logo.png. This pins the
// rule that the default logo leaves the kit icons alone, while a logo an
// admin actually set still wins.
describe('applyFaviconToDom', () => {
  beforeEach(() => {
    document.head.innerHTML =
      '<link rel="icon" type="image/svg+xml" href="/icon.svg" />' +
      '<link rel="apple-touch-icon" href="/apple-touch-icon-180.png" />'
  })

  it('keeps the kit icons when the logo is the default', () => {
    applyFaviconToDom(DEFAULT_LOGO)
    expect(iconHrefs()).toEqual(['/icon.svg'])
  })

  it('still lets a customised logo replace them', () => {
    applyFaviconToDom('/custom-tenant-logo.png')
    expect(iconHrefs()).toEqual(['/custom-tenant-logo.png'])
  })

  it('ignores an empty value', () => {
    applyFaviconToDom('')
    expect(iconHrefs()).toEqual(['/icon.svg'])
  })
})
