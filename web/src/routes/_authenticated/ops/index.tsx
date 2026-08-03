/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { OpsDashboard } from '@/features/ops'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/ops/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()

    // Operators only. This is a convenience guard for routing -- the real
    // enforcement is AdminAuth on /api/tenant/* server-side, because a client
    // guard is trivially bypassed.
    if ((auth.user?.role ?? 0) < ROLE.ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: OpsDashboard,
})
