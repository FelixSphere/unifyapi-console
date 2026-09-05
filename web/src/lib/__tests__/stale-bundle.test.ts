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

  test('a relay 404 under /api is still detected when this bundle has no build id (dev)', () => {
    assert.equal(
      getStaleBundleReason(axiosError({ status: 404 }), ''),
      'removed-endpoint'
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
