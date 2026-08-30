/*
UNIFYAPI-FORK: the raw-ratio tabs must stay out of the billing section.

They are easy to re-add — one string in an array — and the damage is invisible
at the moment it happens. Saving any of them writes a ModelRatio / ModelPrice
options row, and `types.LoadFromJsonString` is replace-not-merge, so from that
point the code catalog no longer drives billing and `scripts/pricing-drift` is
checking a table nobody bills from. Production once held such a row with 2,877
keys.

So the decision is pinned here rather than left to the comment beside it.
*/
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, test } from 'node:test'

const REGISTRY = join(
  new URL('.', import.meta.url).pathname,
  '../section-registry.tsx'
)

/** Tabs that write the pricing options rows and therefore shadow the catalog. */
const SHADOWING_TABS = ['models', 'unset-models', 'upstream-sync']

/** visibleTabsArrays returns the contents of every `visibleTabs={[...]}` prop,
 *  with comments stripped — the tab names are discussed at length in a comment
 *  right above one of them, and matching that would defeat the test. */
function visibleTabsArrays(source: string): string[][] {
  const withoutComments = source
    .replaceAll(/\/\*[\s\S]*?\*\//g, '')
    .replaceAll(/\/\/[^\n]*/g, '')
  return [...withoutComments.matchAll(/visibleTabs=\{\[([\s\S]*?)\]\}/g)].map(
    (match) => [...match[1].matchAll(/'([^']+)'/g)].map((m) => m[1])
  )
}

describe('billing section tabs', () => {
  const source = readFileSync(REGISTRY, 'utf8')
  const arrays = visibleTabsArrays(source)

  test('the registry still declares visibleTabs the way this test reads it', () => {
    // Without this, a refactor that renamed the prop would make every
    // assertion below pass vacuously.
    assert.ok(arrays.length >= 2, `found ${arrays.length} visibleTabs arrays`)
    assert.ok(
      arrays.flat().includes('baseline'),
      'expected the baseline tab to be visible somewhere'
    )
  })

  for (const tab of SHADOWING_TABS) {
    test(`'${tab}' is not reachable from the billing section`, () => {
      const offenders = arrays.filter((tabs) => tabs.includes(tab))
      assert.equal(
        offenders.length,
        0,
        `'${tab}' writes a pricing options row, which replaces the code catalog ` +
          'wholesale and leaves pricing-drift checking a table nobody bills from. ' +
          'Price models by adding a row to unifyapi_catalog.go instead.'
      )
    })
  }

  test('the supported pricing paths are still reachable', () => {
    // The point is to remove the footgun, not the ability to price anything.
    const all = arrays.flat()
    for (const tab of ['baseline', 'channel-cost', 'groups']) {
      assert.ok(all.includes(tab), `${tab} must remain reachable`)
    }
  })
})
