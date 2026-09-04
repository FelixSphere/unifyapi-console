/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  downloadFilename,
  fetchAuthenticatedFile,
} from '../authenticated-download'

describe('fetchAuthenticatedFile', () => {
  test('uses the authenticated request path and asks for a blob', async () => {
    const blob = new Blob(['invoice'])
    const seen: unknown[] = []
    const result = await fetchAuthenticatedFile(
      '/api/pricing/settlement.csv?kind=customer',
      async (path, config) => {
        seen.push(path, config)
        return {
          data: blob,
          headers: {
            'content-disposition': 'attachment; filename="invoice.csv"',
          },
        }
      }
    )
    assert.deepEqual(seen, [
      '/api/pricing/settlement.csv?kind=customer',
      { responseType: 'blob' },
    ])
    assert.equal(result.blob, blob)
    assert.equal(result.filename, 'invoice.csv')
  })
})

describe('downloadFilename', () => {
  test('supports UTF-8 names and safe fallback', () => {
    assert.equal(
      downloadFilename(
        "attachment; filename*=UTF-8''August%20invoice.csv",
        'fallback.csv'
      ),
      'August invoice.csv'
    )
    assert.equal(downloadFilename(undefined, 'fallback.csv'), 'fallback.csv')
    assert.equal(
      downloadFilename("attachment; filename*=UTF-8''%ZZ", 'fallback.csv'),
      'fallback.csv'
    )
  })
})
