/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { createFileRoute } from '@tanstack/react-router'

import { SupplierPortal } from '@/features/supplier-portal'

// Any authenticated user may open the route; the page itself asks the server
// whether this login is a supplier and shows an explanation when it is not.
export const Route = createFileRoute('/_authenticated/supplier/')({
  component: SupplierPortal,
})
