/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * SectionPageLayout renders ONLY its four named slots and silently discards
 * every other child. That is a quiet trap: a dialog parked next to the slots
 * type-checks, renders no warning, and simply never mounts -- its trigger sets
 * state that nothing consumes, so the button is dead and the console is clean.
 *
 * It shipped exactly once, in the supplier portal, where it made "Submit
 * credits" do nothing and a supplier could not sell us credits at all. The
 * string-matching surface tests never noticed, because the dialog file still
 * contained every string they grep for.
 *
 * Two tests, deliberately: the first proves the discard is real, so the second
 * is not cargo cult; the second enforces the rule across every page.
 */
import { describe, expect, test } from 'bun:test'
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { Window } from 'happy-dom'

const win = new Window({ url: 'http://localhost' })
// Installed one by one with defineProperty: on Linux Bun some of these
// (navigator, for one) are read-only accessors on globalThis, and a plain
// Object.assign throws "Attempted to assign to readonly property" before a
// single test runs. A global that truly cannot be redefined is skipped --
// react-dom only needs window/document and the element constructors.
const domGlobals: Record<string, unknown> = {
  window: win,
  document: win.document,
  navigator: win.navigator,
  HTMLElement: win.HTMLElement,
  Node: win.Node,
  Element: win.Element,
  IS_REACT_ACT_ENVIRONMENT: true,
  getComputedStyle: win.getComputedStyle.bind(win),
}
for (const [key, value] of Object.entries(domGlobals)) {
  try {
    Object.defineProperty(globalThis, key, {
      value,
      configurable: true,
      writable: true,
    })
  } catch {
    // read-only and non-configurable on this runtime; leave it alone
  }
}

const { createRoot } = await import('react-dom/client')
const React = (await import('react')).default
const { act } = await import('react')
const { SectionPageLayout } = await import('../components/section-page-layout')

describe('SectionPageLayout drops children that are not slots', () => {
  test('a child outside the named slots never reaches the DOM', async () => {
    const host = document.createElement('div')
    document.body.appendChild(host)
    const root = createRoot(host)
    await act(async () => {
      root.render(
        React.createElement(
          SectionPageLayout,
          null,
          React.createElement(SectionPageLayout.Title, null, 'Title'),
          React.createElement(SectionPageLayout.Content, null, 'in-content'),
          // The trap: a plain child, exactly how the supplier portal's
          // SubmitLotDialog was mounted.
          React.createElement('div', null, 'outside-the-slots')
        )
      )
    })

    expect(host.textContent).toContain('in-content')
    // If this ever passes, the layout started rendering stray children and the
    // structural guard below can be relaxed.
    expect(host.textContent).not.toContain('outside-the-slots')
  })
})

// --- structural guard over every page ---

const SRC = join(new URL('.', import.meta.url).pathname, '../../..')

// __tests__ is skipped: this file's own regression fixtures are JSX in a
// string, and a scanner that flags its own examples is a scanner nobody keeps.
function tsxFiles(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir)) {
    if (entry === 'node_modules' || entry === '__tests__') continue
    const path = join(dir, entry)
    if (statSync(path).isDirectory()) out.push(...tsxFiles(path))
    else if (entry.endsWith('.tsx')) out.push(path)
  }
  return out
}

/**
 * strayChildren returns the tag names rendered as DIRECT children of a
 * <SectionPageLayout> that are not SectionPageLayout.* slots.
 *
 * It walks JSX tags tracking nesting depth rather than matching indentation,
 * because indentation is exactly what a formatter is free to change.
 */
function strayChildren(source: string): string[] {
  const stray: string[] = []
  // Fragments must be matched BEFORE the generic forms: `<>` matches no tag
  // name, and `</>` contains `/>`, so a naive pattern scores every fragment
  // pair as a net -1 on depth. That drift made this scanner miss the real
  // regression on PR #62 until it was run against that branch.
  const tag = /<>|<\/>|<\/?([A-Za-z][\w.]*)|\/>/g
  let cursor = 0
  for (;;) {
    const start = source.indexOf('<SectionPageLayout', cursor)
    if (start === -1) break
    // Skip the slot components themselves (<SectionPageLayout.Title> etc).
    if (/^<SectionPageLayout\./.test(source.slice(start))) {
      cursor = start + 1
      continue
    }
    let depth = 0
    tag.lastIndex = start
    for (let m = tag.exec(source); m; m = tag.exec(source)) {
      const [text, name] = m
      if (text === '<>') {
        depth += 1
        if (depth === 2) stray.push('>')
      } else if (text === '</>' || text === '/>') {
        depth -= 1
      } else if (text.startsWith('</')) {
        depth -= 1
        if (name === 'SectionPageLayout' && depth === 0) break
      } else {
        depth += 1
        if (depth === 2 && !name.startsWith('SectionPageLayout.')) {
          stray.push(name)
        }
      }
      if (depth === 0) break
    }
    cursor = tag.lastIndex || start + 1
  }
  return stray
}

describe('every page keeps its children inside a slot', () => {
  test('the scanner catches the shape that broke the supplier portal', () => {
    const regressed = `
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Supplier portal')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>{body}</SectionPageLayout.Content>
        {data ? (
          <SubmitLotDialog open={submitOpen} onOpenChange={setSubmitOpen} />
        ) : null}
      </SectionPageLayout>`
    expect(strayChildren(regressed)).toEqual(['SubmitLotDialog'])

    const fixed = `
      <SectionPageLayout>
        <SectionPageLayout.Title>{t('Supplier portal')}</SectionPageLayout.Title>
        <SectionPageLayout.Content>
          {data ? (
            <SubmitLotDialog open={submitOpen} onOpenChange={setSubmitOpen} />
          ) : null}
        </SectionPageLayout.Content>
      </SectionPageLayout>`
    expect(strayChildren(fixed)).toEqual([])
  })

  test('a fragment inside a slot does not hide a later stray child', () => {
    // Regression on the scanner itself. `<>` matches no tag name and `</>`
    // contains `/>`, so an earlier version scored each fragment pair as -1 and
    // ran out of depth before reaching the stray child. PR #62 has a fragment
    // inside Content, and the scanner reported that branch clean while the bug
    // was still in it.
    const withFragment = `
      <SectionPageLayout>
        <SectionPageLayout.Content>
          <>
            <p>{t('anything')}</p>
          </>
        </SectionPageLayout.Content>
        {activeTerms ? (
          <SubmitLotDialog open={sellOpen} onOpenChange={setSellOpen} />
        ) : null}
      </SectionPageLayout>`
    expect(strayChildren(withFragment)).toEqual(['SubmitLotDialog'])
  })

  test('no page mounts a component outside the slots', () => {
    const offenders: string[] = []
    for (const file of tsxFiles(SRC)) {
      const source = readFileSync(file, 'utf8')
      if (!source.includes('<SectionPageLayout')) continue
      for (const name of strayChildren(source)) {
        offenders.push(`${file.slice(SRC.length + 1)}: <${name}>`)
      }
    }
    expect(offenders).toEqual([])
  })
})
