/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
// The DOM environment a pricing card needs, as an import so a new test file
// does not have to carry another copy of it.
//
// Import this BEFORE dynamically importing any component: ESM hoists imports,
// so the globals are in place by the time `await import(...)` runs.
import { Window } from 'happy-dom'

export const domWindow = new Window()

// Expose the whole happy-dom window as the global environment: the card's
// dependency tree reaches for customElements, matchMedia, HTMLElement
// subclasses and more, and enumerating them by hand breaks on every new
// import. Anything happy-dom lacks gets an inert stand-in below.
{
  const seen = new Set<string>(['undefined', 'NaN', 'Infinity', 'globalThis'])
  let proto: object | null = domWindow
  while (proto && proto !== Object.prototype) {
    for (const key of Object.getOwnPropertyNames(proto)) {
      if (seen.has(key) || key in globalThis) continue
      seen.add(key)
      try {
        const value = (domWindow as unknown as Record<string, unknown>)[key]
        Object.defineProperty(globalThis, key, { configurable: true, value })
      } catch {
        // accessor that throws off-window; skip
      }
    }
    proto = Object.getPrototypeOf(proto)
  }
  for (const key of ['window', 'document', 'navigator'] as const) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: (domWindow as unknown as Record<string, unknown>)[key],
    })
  }
}

const inertMatchMedia = () => ({
  matches: false,
  media: '',
  onchange: null,
  addEventListener() {},
  removeEventListener() {},
  addListener() {},
  removeListener() {},
  dispatchEvent: () => false,
})
Object.defineProperty(globalThis, 'matchMedia', {
  configurable: true,
  value: inertMatchMedia,
})
Object.defineProperty(domWindow, 'matchMedia', {
  configurable: true,
  value: inertMatchMedia,
})
if (!('ResizeObserver' in globalThis)) {
  Object.defineProperty(globalThis, 'ResizeObserver', {
    configurable: true,
    value: class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  })
}
;(
  globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
).IS_REACT_ACT_ENVIRONMENT = true
