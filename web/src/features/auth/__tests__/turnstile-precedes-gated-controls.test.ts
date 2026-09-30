/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
 * The Turnstile challenge must render ABOVE every control it disables.
 *
 * Reported from production on 2026-09-30: on the sign-up page a user filled in
 * their email, pressed "Send code", and nothing happened. The button is
 * disabled until `turnstileReady`, and the widget that sets it was rendered
 * BELOW the button, with no message saying so. The one control that unlocks
 * the form was further down the page than the control it unlocks, so the
 * natural reading order told the user to do the impossible thing first. The
 * same shape existed on the forgot-password form.
 *
 * Nothing else caught this. It type-checks, it renders without a warning, and
 * every string these files are grepped for is still present -- the defect is
 * purely one of ORDER.
 *
 * Why asserting on source position is enough here, when it was not enough for
 * SectionPageLayout: JSX siblings render in source order, these blocks are
 * siblings in a single column, and no `order`/`flex-direction: column-reverse`
 * class is applied to either. Source position therefore *is* render position.
 * If a future change introduces ordering CSS or moves these into a slot-style
 * component, this test stops being sufficient and must be replaced by a
 * rendered-DOM assertion -- say so rather than deleting it.
 */
import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const SRC = join(import.meta.dir, '..', '..', '..')

const FORMS = [
  {
    file: 'features/auth/sign-up/components/sign-up-form.tsx',
    what: 'sign-up: Send code, and Submit',
  },
  {
    file: 'features/auth/forgot-password/components/forgot-password-form.tsx',
    what: 'forgot-password: Send reset email',
  },
]

function read(file: string) {
  return readFileSync(join(SRC, file), 'utf8')
}

describe('Turnstile renders before the controls it gates', () => {
  for (const { file, what } of FORMS) {
    test(what, () => {
      const source = read(file)

      const widget = source.indexOf('<Turnstile')
      expect(widget).toBeGreaterThan(-1)

      // Every control whose `disabled` depends on turnstileReady. The
      // declaration itself (`const turnstileReady = ...`) sits in the
      // component body above the JSX and is not a control, so it is excluded.
      const gated = [...source.matchAll(/!turnstileReady/g)]
        .map((m) => m.index ?? -1)
        .filter((i) => i > source.indexOf('return ('))
      expect(gated.length).toBeGreaterThan(0)

      for (const control of gated) {
        expect(widget).toBeLessThan(control)
      }
    })
  }

  test('both forms are actually covered, so a rename cannot empty this suite', () => {
    // A file that moved or was renamed would make the loop above vacuous while
    // still reporting green. Fail loudly instead.
    for (const { file } of FORMS) {
      expect(read(file)).toContain('turnstileReady')
    }
  })
})
