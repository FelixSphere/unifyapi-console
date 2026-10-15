/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
// The sidebar shows a badge only when something is unread: an empty badge or
// a "0" would read as a notification that is not there.
import { describe, expect, test } from 'bun:test'

import { unreadBadge } from '../use-unread'

describe('unreadBadge', () => {
  test('nothing when nothing is unread, or the count is unknown', () => {
    expect(unreadBadge(0)).toBeUndefined()
    expect(unreadBadge(undefined)).toBeUndefined()
    expect(unreadBadge(-1)).toBeUndefined()
  })
  test('the count, capped at 99+', () => {
    expect(unreadBadge(1)).toBe('1')
    expect(unreadBadge(99)).toBe('99')
    expect(unreadBadge(100)).toBe('99+')
  })
})
