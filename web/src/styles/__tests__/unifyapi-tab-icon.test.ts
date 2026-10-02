import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../../..')
const sha256 = (p: string) =>
  createHash('sha256')
    .update(readFileSync(join(webRoot, p)))
    .digest('hex')

/*
 * The browser tab icon is NOT decided by the <link> tags in index.html.
 *
 * `initSystemBranding` in src/main.tsx calls `applyFaviconToDom(status.logo)`,
 * and that helper REMOVES every `link[rel~="icon"]` on the page -- including the
 * brand SVG -- and appends a single one pointing at the `Logo` option. `Logo`
 * defaults to `/logo.png` (DEFAULT_LOGO in src/lib/constants.ts) and production
 * is set to exactly that, so **web/public/logo.png is the tab icon**, whatever
 * index.html says.
 *
 * That is how the retired black rounded-square "U" survived the brand work in
 * #230 and #234: both fixed the markup, neither touched the file the runtime
 * actually installs. Measured in a browser after this fix, the one remaining
 * icon link is `/logo.png` and its ink covers 35.7% of the frame -- the hex
 * mark. The old artwork was a filled square and covered nearly all of it.
 *
 * These hashes are a brand guard, not a checksum ritual: they are the artwork
 * generated from `icons/icon.svg` in the UnifyAI brand kit, the same files
 * UnifyAI Data serves. To change them deliberately, regenerate from that SVG,
 * confirm in a browser that the mark is what renders, and update the hash here
 * in the same commit.
 */
describe('the browser tab icon is the UnifyAI mark', () => {
  test('logo.png -- the file the runtime installs as the favicon', () => {
    assert.equal(
      sha256('public/logo.png'),
      '4f5b00195734dbfe6a0ac14d9d4faa9c72e2c05d724293444c8f2f4be0e4a74d'
    )
  })

  test('favicon.ico -- what the browser requests on its own', () => {
    assert.equal(
      sha256('public/favicon.ico'),
      '7e1054e85108d211e13596063327141b9441c7a3c28a81426089b58fc862d496'
    )
  })

  test('index.html still offers the brand SVG for browsers that honour it', () => {
    const html = readFileSync(join(webRoot, 'index.html'), 'utf8')
    assert.ok(html.includes('href="/icon.svg"'))
  })
})
