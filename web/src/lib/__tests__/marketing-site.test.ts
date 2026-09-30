/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import {
  MARKETING_SITE_URL,
  isExternalHref,
  rootDestination,
} from '../marketing-site'

describe('the console points home at the marketing site', () => {
  test('the marketing site is the canonical https www origin', () => {
    assert.equal(MARKETING_SITE_URL, 'https://www.unifyapi.ai/')
  })

  test('an absolute URL is external; a route path is not', () => {
    assert.equal(isExternalHref(MARKETING_SITE_URL), true)
    assert.equal(isExternalHref('HTTP://www.unifyapi.ai'), true)
    assert.equal(isExternalHref('/'), false)
    assert.equal(isExternalHref('/dashboard'), false)
    assert.equal(isExternalHref('javascript:alert(1)'), false)
  })

  test('a client-side landing on / goes where the edge sends a hard one', () => {
    assert.equal(rootDestination(true), '/dashboard')
    assert.equal(rootDestination(false), '/sign-in')
  })
})
