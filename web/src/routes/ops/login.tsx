/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * Dedicated operator entry point: /ops/login
 *
 * A distinct, bookmarkable URL for staff that lands on the operations
 * dashboard rather than the customer dashboard. It delegates to the canonical
 * sign-in flow instead of rendering a second login form, because that flow
 * carries 2FA, Turnstile, OAuth providers, and session establishment -- a
 * hand-rolled duplicate would silently miss one of them.
 *
 * Note on what this is and is not: this is a separate *entry point*, not a
 * security boundary. Both pages post to the same /api/user/login. The boundary
 * that matters is AdminAuth on /api/tenant/* server-side, plus the fact that
 * operator accounts are separate rows (role >= 10, no tenant) from customers.
 * For genuine isolation, serve operations from its own hostname and restrict it
 * at the load balancer -- see infra/console.tf.
 */
export const Route = createFileRoute('/ops/login')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    if (auth.user) {
      // Already signed in: send operators to operations, everyone else away.
      if ((auth.user.role ?? 0) >= ROLE.ADMIN) {
        throw redirect({ to: '/ops' })
      }
      throw redirect({ to: '/403' })
    }

    throw redirect({ to: '/sign-in', search: { redirect: '/ops' } })
  },
})
