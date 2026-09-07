/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { afterEach, describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  getStaleBundleReason,
  isChunkLoadError,
  isServerFailure,
  markStaleBundle,
  noteFailedRequest,
  readServerBuildId,
  resetStaleBundleForTests,
  subscribeStaleBundle,
} from '../stale-bundle'

const CLIENT = 'build-old'

const relayNotFound = {
  error: {
    message: 'Invalid URL (POST /api/credit-contribution/self/)',
    type: 'invalid_request_error',
    param: '',
    code: '',
  },
}

function axiosError(input: {
  status: number
  url?: string
  data?: unknown
  headers?: Record<string, string>
}) {
  return {
    config: { url: input.url ?? '/api/credit-contribution/self/', baseURL: '' },
    response: {
      status: input.status,
      data: input.data ?? relayNotFound,
      headers: input.headers ?? {},
    },
  }
}

describe('readServerBuildId', () => {
  test('reads the header through AxiosHeaders.get and from plain objects', () => {
    const axiosHeaders = {
      get: (name: string) => (name === 'x-unifyapi-build' ? ' abc ' : null),
    }

    assert.equal(readServerBuildId(axiosHeaders), 'abc')
    assert.equal(readServerBuildId({ 'x-unifyapi-build': 'def' }), 'def')
    assert.equal(readServerBuildId({ 'content-type': 'application/json' }), '')
    assert.equal(readServerBuildId(undefined), '')
  })
})

describe('getStaleBundleReason', () => {
  test('a response built from another bundle is a build mismatch whatever its status', () => {
    const error = axiosError({
      status: 200,
      data: { success: true },
      headers: { 'x-unifyapi-build': 'build-new' },
    })

    assert.equal(getStaleBundleReason(error, CLIENT), 'build-mismatch')
  })

  test('a relay 404 under /api without a build header is a removed endpoint', () => {
    assert.equal(
      getStaleBundleReason(axiosError({ status: 404 }), CLIENT),
      'removed-endpoint'
    )
  })

  test('a bundle without a build id (dev) is never classified as stale', () => {
    assert.equal(getStaleBundleReason(axiosError({ status: 404 }), ''), null)
    assert.equal(
      getStaleBundleReason(
        axiosError({
          status: 200,
          data: {},
          headers: { 'x-unifyapi-build': 'build-new' },
        }),
        ''
      ),
      null
    )
  })

  test('a relay 404 from a server built from the same bundle is a bug, not staleness', () => {
    const error = axiosError({
      status: 404,
      headers: { 'x-unifyapi-build': CLIENT },
    })

    assert.equal(getStaleBundleReason(error, CLIENT), null)
  })

  test('a 404 with an ordinary API body is not staleness', () => {
    const error = axiosError({
      status: 404,
      data: { success: false, message: 'record not found' },
    })

    assert.equal(getStaleBundleReason(error, CLIENT), null)
  })

  test('a relay 404 outside /api is not staleness', () => {
    const error = axiosError({ status: 404, url: '/v1/chat/completions' })

    assert.equal(getStaleBundleReason(error, CLIENT), null)
  })

  test('server failures and network errors are not staleness', () => {
    assert.equal(
      getStaleBundleReason(axiosError({ status: 500 }), CLIENT),
      null
    )
    assert.equal(
      getStaleBundleReason(axiosError({ status: 503 }), CLIENT),
      null
    )
    assert.equal(
      getStaleBundleReason({ message: 'Network Error' }, CLIENT),
      null
    )
    assert.equal(getStaleBundleReason(null, CLIENT), null)
  })
})

describe('isChunkLoadError', () => {
  test("recognises rspack's ChunkLoadError by name and by message", () => {
    const named = Object.assign(
      new Error(
        'Loading chunk 1127 failed.\n(missing: /static/js/async/1127.bee1faa10b.js)'
      ),
      {
        name: 'ChunkLoadError',
      }
    )

    assert.equal(isChunkLoadError(named), true)
    assert.equal(
      isChunkLoadError({
        message:
          'Loading CSS chunk 2261 failed.\n(/static/css/2261.10e003a7e2.css)',
      }),
      true
    )
    assert.equal(
      isChunkLoadError(
        new TypeError(
          'Failed to fetch dynamically imported module: /static/js/async/x.js'
        )
      ),
      true
    )
  })

  test('ordinary errors and API failures are not chunk-load errors', () => {
    assert.equal(
      isChunkLoadError(
        new TypeError("Cannot read properties of undefined (reading 'map')")
      ),
      false
    )
    assert.equal(isChunkLoadError(axiosError({ status: 500 })), false)
    assert.equal(isChunkLoadError('Loading chunk failed'), false)
    assert.equal(isChunkLoadError(null), false)
  })
})

describe('isServerFailure', () => {
  test('only responses with status >= 500 qualify for the /500 page', () => {
    assert.equal(isServerFailure(axiosError({ status: 500 })), true)
    assert.equal(isServerFailure(axiosError({ status: 503 })), true)
    assert.equal(isServerFailure(axiosError({ status: 499 })), false)
    assert.equal(isServerFailure(axiosError({ status: 404 })), false)
    assert.equal(isServerFailure({ message: 'Network Error' }), false)
  })
})

describe('stale bundle notification', () => {
  afterEach(() => {
    resetStaleBundleForTests()
  })

  test('notifies subscribers once and ignores later detections', () => {
    const seen: string[] = []
    subscribeStaleBundle((reason) => seen.push(reason))

    markStaleBundle('removed-endpoint')
    markStaleBundle('build-mismatch')

    assert.deepEqual(seen, ['removed-endpoint'])
  })

  test('replays the detection to a subscriber that arrives late', () => {
    markStaleBundle('build-mismatch')
    const seen: string[] = []

    const unsubscribe = subscribeStaleBundle((reason) => seen.push(reason))
    unsubscribe()
    resetStaleBundleForTests()
    markStaleBundle('removed-endpoint')

    assert.deepEqual(seen, ['build-mismatch'])
  })

  test('noteFailedRequest records a stale failure and leaves a genuine 500 alone', () => {
    const seen: string[] = []
    subscribeStaleBundle((reason) => seen.push(reason))

    assert.equal(noteFailedRequest(axiosError({ status: 500 }), CLIENT), null)
    assert.deepEqual(seen, [])

    assert.equal(
      noteFailedRequest(axiosError({ status: 404 }), CLIENT),
      'removed-endpoint'
    )
    assert.deepEqual(seen, ['removed-endpoint'])
  })
})
