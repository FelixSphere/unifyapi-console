/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

import { createFileRoute } from '@tanstack/react-router'

import { CreditSupplyHub } from '@/features/supplier-portal/hub'

// One sidebar entry for everyone. The hub decides what it means: a customer
// sells credits here; the super admin runs the supply from here.
export const Route = createFileRoute('/_authenticated/credit-supply/')({
  component: CreditSupplyHub,
})
