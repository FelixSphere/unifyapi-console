/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { createFileRoute, redirect } from '@tanstack/react-router'

import { PlatformLog } from '@/features/platform-log'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/platform-log/')({
  beforeLoad: () => {
    // Super admin only, and not merely admin: these rows carry OTHER
    // customers' source addresses and the prefixes of keys that are not
    // theirs. This is a routing convenience -- the real enforcement is
    // RootAuth on /api/platform-log server-side, because a client guard is
    // trivially bypassed.
    const { auth } = useAuthStore.getState()
    if ((auth.user?.role ?? 0) < ROLE.SUPER_ADMIN) {
      throw redirect({ to: '/403' })
    }
  },
  component: PlatformLog,
})
