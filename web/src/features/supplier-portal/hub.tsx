/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the role switch behind the "Credit Supply" sidebar entry.
// Customers get the selling portal; the super admin gets the management
// screen that also lives under System Settings -> Billing.

import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { CreditSupplySection } from '@/features/system-settings/billing/credit-supply-section'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SupplierPortal } from './index'

export function CreditSupplyHub() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const isSuperAdmin = Boolean(user?.role && user.role >= ROLE.SUPER_ADMIN)
  if (!isSuperAdmin) return <SupplierPortal />
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Credit Supply')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <CreditSupplySection />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
