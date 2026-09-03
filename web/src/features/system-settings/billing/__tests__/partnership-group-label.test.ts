/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, test } from 'node:test'

import { partnershipGroupLabel } from '../partnership-group-label'

describe('partnership group labels', () => {
  test('keeps equal-ratio groups distinct by showing their identifiers', () => {
    const groups = { GenAI: 1, UnifyAI: 1 }
    const labels = Object.entries(groups).map(([group, ratio]) =>
      partnershipGroupLabel(group, ratio)
    )

    assert.deepEqual(labels, ['GenAI (1×)', 'UnifyAI (1×)'])
    assert.notEqual(labels[0], labels[1])
  })

  test('the Program group dropdown uses the identifier-preserving label', () => {
    const source = readFileSync(
      join(
        new URL('.', import.meta.url).pathname,
        '../partnership-programs-section.tsx'
      ),
      'utf8'
    )

    assert.match(
      source,
      /partnershipGroupLabel\(group, query\.data\?\.groups\[group\]\)/
    )
    assert.doesNotMatch(source, /query\.data\?\.groups\[group\] \|\| group/)
  })
})
