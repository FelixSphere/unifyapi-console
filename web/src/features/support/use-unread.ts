/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useQuery } from '@tanstack/react-query'

import { getUnread } from './api'

export const SUPPORT_UNREAD_KEY = ['support', 'unread'] as const

// The sidebar badge and the unread dots share this one query. It refetches
// every minute and when the tab regains focus, and opening a conversation
// invalidates it, so the badge clears as soon as the server records the read.
export function useSupportUnread() {
  return useQuery({
    queryKey: SUPPORT_UNREAD_KEY,
    queryFn: getUnread,
    refetchInterval: 60_000,
    refetchOnWindowFocus: true,
    staleTime: 15_000,
  })
}

// "3", "99+", or nothing at all when there is nothing unread.
export function unreadBadge(count: number | undefined): string | undefined {
  if (!count || count <= 0) return undefined
  return count > 99 ? '99+' : String(count)
}
