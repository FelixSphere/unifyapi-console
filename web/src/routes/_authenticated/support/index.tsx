/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { createFileRoute } from '@tanstack/react-router'
import z from 'zod'

import { SupportPage } from '@/features/support'

// One sidebar entry for everyone: customers ask, staff answer. ?ticket= opens
// a conversation, which is where the notification emails link.
const supportSearchSchema = z.object({
  ticket: z.coerce.number().int().positive().optional().catch(undefined),
})

export const Route = createFileRoute('/_authenticated/support/')({
  validateSearch: supportSearchSchema,
  component: SupportPage,
})
