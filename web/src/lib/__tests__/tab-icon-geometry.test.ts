/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, expect, it } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { inflateSync } from 'node:zlib'

// The browser draws the tab icon into a fixed box, so any margin baked into
// the asset is simply a smaller logo. Against Unify Gateway, whose raster is
// edge to edge, our mark read visibly smaller in the tab strip: icon.svg
// carried a 170-unit margin per side (76.9% of its canvas) and favicon.ico
// 87.5%. Three products, three sizes.
//
// This is invisible to every other check -- the icon is correct, on brand and
// the right colour, just smaller -- and it comes back the moment someone
// re-exports from the kit, whose icon.svg is padded. Hence a geometry test.
const publicDir = join(import.meta.dir, '..', '..', '..', 'public')

describe('the tab icon fills its canvas', () => {
  const svg = readFileSync(join(publicDir, 'icon.svg'), 'utf8')

  it('icon.svg has a viewBox that tightly bounds the mark', () => {
    const viewBox = svg.match(/viewBox="([^"]+)"/)?.[1] ?? ''
    expect(viewBox).not.toBe('')
    const [vx, vy, vw, vh] = viewBox.split(/\s+/).map(Number)

    const path = svg.match(/<path d="(.*?)"/s)?.[1] ?? ''
    expect(path).not.toBe('')
    const nums = (path.match(/-?\d+\.?\d*/g) ?? []).map(Number)
    const xs = nums.filter((_, i) => i % 2 === 0)
    const ys = nums.filter((_, i) => i % 2 === 1)
    const markW = Math.max(...xs) - Math.min(...xs)
    const markH = Math.max(...ys) - Math.min(...ys)

    // The canvas is square and the mark is centred in it, so the LONGER side
    // must reach both edges. One tolerance unit on a ~1134-unit canvas is
    // rounding in the viewBox string, not margin.
    expect(Math.max(markW, markH)).toBeGreaterThanOrEqual(Math.max(vw, vh) - 1)
    expect(Math.abs(vw - vh)).toBeLessThanOrEqual(1)

    // and the mark must not be cropped by the tightening
    expect(Math.min(...xs)).toBeGreaterThanOrEqual(vx - 1)
    expect(Math.min(...ys)).toBeGreaterThanOrEqual(vy - 1)
    expect(Math.max(...xs)).toBeLessThanOrEqual(vx + vw + 1)
    expect(Math.max(...ys)).toBeLessThanOrEqual(vy + vh + 1)
  })

  it('icon.svg still swaps to the light mark in a dark browser chrome', () => {
    // Tightening the viewBox must not cost the dark-mode rule; a raster cannot
    // carry it, so the SVG is the only asset that adapts.
    expect(svg).toContain('prefers-color-scheme: dark')
  })

  it('favicon.ico ships the 16, 32 and 48 px frames the tab strip picks from', () => {
    const widths = icoFrames().map((f) => f.width)
    expect(widths.sort((a, b) => a - b)).toEqual([16, 32, 48])
  })

  it('every favicon.ico frame has the mark reaching all four edges', () => {
    // favicon.ico is the frame Chrome actually draws in the tab strip, so this
    // is the assertion that matches what a person sees. The SVG test above
    // cannot stand in for it -- the two assets are exported separately, and
    // the old .ico was padded (87.5%) by a different amount than the old SVG.
    for (const frame of icoFrames()) {
      const { width, height } = frame
      const alpha = decodePngAlpha(frame.png)
      const opaque = (x: number, y: number) => alpha[y * width + x] > 24

      const rows = [...Array(height).keys()].filter((y) =>
        [...Array(width).keys()].some((x) => opaque(x, y))
      )
      const cols = [...Array(width).keys()].filter((x) =>
        [...Array(height).keys()].some((y) => opaque(x, y))
      )
      expect(rows.length).toBeGreaterThan(0)

      // Anti-aliasing can soften a corner, so allow the ink to start one pixel
      // in -- but no more. Two pixels of margin at 32px is the regression.
      expect(Math.min(...rows)).toBeLessThanOrEqual(1)
      expect(Math.min(...cols)).toBeLessThanOrEqual(1)
      expect(height - 1 - Math.max(...rows)).toBeLessThanOrEqual(1)
      expect(width - 1 - Math.max(...cols)).toBeLessThanOrEqual(1)
    }
  })
})

function icoFrames() {
  const ico = readFileSync(join(publicDir, 'favicon.ico'))
  expect(ico.readUInt16LE(0)).toBe(0) // reserved
  expect(ico.readUInt16LE(2)).toBe(1) // type: icon
  return Array.from({ length: ico.readUInt16LE(4) }, (_, i) => {
    const e = 6 + i * 16
    const size = ico.readUInt32LE(e + 8)
    const offset = ico.readUInt32LE(e + 12)
    return {
      width: ico.readUInt8(e) || 256,
      height: ico.readUInt8(e + 1) || 256,
      png: ico.subarray(offset, offset + size),
    }
  })
}

/** Alpha channel of an 8-bit RGBA PNG. Enough for icon frames, not general. */
function decodePngAlpha(png: Buffer): Uint8Array {
  expect(png.subarray(0, 8).toString('hex')).toBe('89504e470d0a1a0a')
  let width = 0
  let height = 0
  const idat: Buffer[] = []
  for (let p = 8; p + 8 <= png.length; ) {
    const len = png.readUInt32BE(p)
    const type = png.subarray(p + 4, p + 8).toString('ascii')
    const data = png.subarray(p + 8, p + 8 + len)
    if (type === 'IHDR') {
      width = data.readUInt32BE(0)
      height = data.readUInt32BE(4)
      expect(data.readUInt8(8)).toBe(8) // bit depth
      expect(data.readUInt8(9)).toBe(6) // colour type: RGBA
      expect(data.readUInt8(12)).toBe(0) // not interlaced
    } else if (type === 'IDAT') idat.push(Buffer.from(data))
    p += 12 + len
  }

  const raw = Buffer.from(inflateSync(Buffer.concat(idat)))
  const bpp = 4
  const stride = width * bpp
  const out = Buffer.alloc(height * stride)
  for (let y = 0; y < height; y++) {
    const filter = raw[y * (stride + 1)]
    const line = raw.subarray(
      y * (stride + 1) + 1,
      y * (stride + 1) + 1 + stride
    )
    for (let i = 0; i < stride; i++) {
      const a = i >= bpp ? out[y * stride + i - bpp] : 0
      const b = y > 0 ? out[(y - 1) * stride + i] : 0
      const c = i >= bpp && y > 0 ? out[(y - 1) * stride + i - bpp] : 0
      let add = 0
      if (filter === 1) add = a
      else if (filter === 2) add = b
      else if (filter === 3) add = (a + b) >> 1
      else if (filter === 4) {
        const pa = Math.abs(b - c)
        const pb = Math.abs(a - c)
        const pc = Math.abs(a + b - 2 * c)
        if (pa <= pb && pa <= pc) add = a
        else if (pb <= pc) add = b
        else add = c
      }
      out[y * stride + i] = (line[i] + add) & 0xff
    }
  }

  const alpha = new Uint8Array(width * height)
  for (let i = 0; i < width * height; i++) alpha[i] = out[i * bpp + 3]
  return alpha
}
