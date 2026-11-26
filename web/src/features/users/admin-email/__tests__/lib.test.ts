/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { describe, test } from 'bun:test'
import assert from 'node:assert/strict'

import { contentFromTemplate, hasContent, replyToWarning } from '../lib'

describe('admin email compose logic', () => {
  test('a template that asks for a reply is flagged when Reply-To is empty', () => {
    const mythos = { subject: 'Access', body: 'Reply to this email with…' }
    assert.equal(replyToWarning('', mythos), 'blocking')
    assert.equal(replyToWarning('  ', mythos), 'blocking')
    assert.equal(replyToWarning('support@unifyapi.ai', mythos), 'none')
  })

  test('any message still notes an empty Reply-To', () => {
    assert.equal(replyToWarning('', { subject: 'Hi', body: 'News' }), 'notice')
  })

  test('picking no template starts blank; a template fills every field', () => {
    assert.deepEqual(contentFromTemplate(undefined).subject, '')
    const filled = contentFromTemplate({
      id: 4,
      builtin_key: '',
      name: 'n',
      subject: 's',
      headline: 'h',
      body: 'b',
      cta_label: 'l',
      cta_url: 'u',
      updated_at: 0,
    })
    assert.equal(filled.template_id, 4)
    assert.equal(filled.cta_url, 'u')
  })

  test('a message needs a subject and a body', () => {
    const blank = contentFromTemplate(undefined)
    assert.equal(hasContent(blank), false)
    assert.equal(hasContent({ ...blank, subject: 's', body: ' ' }), false)
    assert.equal(hasContent({ ...blank, subject: 's', body: 'b' }), true)
  })
})
