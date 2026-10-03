/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * UNIFYAPI-BRAND: company facts from UI-STANDARD.md "Company and legal
 * facts", identical in every Unify product. The brand is UnifyAI; the legal
 * entity (operator decision 2026-10-03, replacing the Malaysia entity) is
 * FelixSphere LLC, United States. "FelixSphere LLC" appears only in the
 * legal-entity line -- on legal pages and in email footers -- never as a
 * tagline, logo or link, and never in a page footer.
 */
export const UNIFYAI_COMPANY_NAME = 'UnifyAI'
export const UNIFYAI_LEGAL_ENTITY_LINE =
  'Operated by FelixSphere LLC · 6 Karen Ct, CA 94010, United States'
/** @deprecated same value as UNIFYAI_LEGAL_ENTITY_LINE; kept for older imports. */
export const UNIFYAI_COMPANY_ADDRESS = UNIFYAI_LEGAL_ENTITY_LINE

/**
 * `© <current year> UnifyAI` -- the year is computed, never typed. Footers
 * carry the brand line only (UI-STANDARD.md, 2026-10-03); the legal-entity
 * line is rendered by legal pages and by service/unifyapi_email.go.
 */
export function companyLine(now: Date = new Date()): string {
  return `© ${now.getFullYear()} ${UNIFYAI_COMPANY_NAME}`
}
