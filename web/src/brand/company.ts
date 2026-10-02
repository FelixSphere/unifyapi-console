/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * UNIFYAPI-BRAND: company facts from UI-STANDARD.md, identical in every
 * UnifyAI product. The product is UnifyAPI; the company is UnifyAI.
 */
export const UNIFYAI_COMPANY_NAME = 'UnifyAI'
export const UNIFYAI_COMPANY_ADDRESS =
  'Lebuh Bandar Utama PJU 6, 47800 Petaling Jaya, Selangor, Malaysia'

/** `© <current year> UnifyAI · <address>` -- the year is computed, never typed. */
export function companyLine(now: Date = new Date()): string {
  return `© ${now.getFullYear()} ${UNIFYAI_COMPANY_NAME} · ${UNIFYAI_COMPANY_ADDRESS}`
}
