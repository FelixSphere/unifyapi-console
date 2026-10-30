/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useQuery } from '@tanstack/react-query'

import { getGroups } from '../api'

const EMPTY: string[] = []

// Every Group Pricing key. Shares the ['groups'] query the channels table
// already runs for its filter, so the cards and columns add no request.
export function usePricingGroups(): string[] {
  const { data } = useQuery({ queryKey: ['groups'], queryFn: getGroups })
  return data?.data ?? EMPTY
}
