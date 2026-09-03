/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { printAuthenticatedDocument } from '../print-authenticated-document'

describe('printAuthenticatedDocument', () => {
  test('opens synchronously and fetches the invoice with authentication-capable client options', async () => {
    const calls: unknown[] = []
    const target = {
      opener: {} as Window,
      document: {
        open: () => calls.push('open'),
        write: (value: string) => calls.push(['write', value]),
        close: () => calls.push('document-close'),
      },
      close: () => calls.push('window-close'),
      focus: () => calls.push('focus'),
      print: () => calls.push('print'),
    }
    await printAuthenticatedDocument(
      '/api/pricing/settlement/42/invoice',
      async (path, config) => {
        calls.push(['request', path, config])
        return { data: '<!doctype html><title>Invoice 42</title>' }
      },
      () => target as unknown as Window
    )

    assert.ok(
      calls.some(
        (call) =>
          Array.isArray(call) &&
          call[0] === 'request' &&
          call[1] === '/api/pricing/settlement/42/invoice' &&
          JSON.stringify(call[2]) ===
            JSON.stringify({ responseType: 'text', disableDuplicate: true })
      )
    )
    assert.equal(calls.at(-1), 'print')
    assert.equal(target.opener, null)
  })

  test('closes the popup when the authenticated request fails', async () => {
    let closed = false
    const target = {
      opener: null,
      document: { open() {}, write() {}, close() {} },
      close: () => {
        closed = true
      },
      focus() {},
      print() {},
    }
    await assert.rejects(
      () =>
        printAuthenticatedDocument(
          '/invoice',
          async () => {
            throw new Error('401')
          },
          () => target as unknown as Window
        ),
      /401/
    )
    assert.equal(closed, true)
  })
})
