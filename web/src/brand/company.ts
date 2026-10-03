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
 * FelixSphere LLC, United States. "FelixSphere LLC" appears only in this
 * legal-entity line -- never as a tagline, logo or link.
 */
export const UNIFYAI_COMPANY_NAME = 'UnifyAI'
export const UNIFYAI_LEGAL_ENTITY_LINE =
  'Operated by FelixSphere LLC · 6 Karen Ct, CA 94010, United States'
/** @deprecated kept for callers that still import the old name; same value as UNIFYAI_LEGAL_ENTITY_LINE. */
export const UNIFYAI_COMPANY_ADDRESS = UNIFYAI_LEGAL_ENTITY_LINE

/** `© <current year> UnifyAI · Operated by FelixSphere LLC · <address>` -- the year is computed, never typed. */
export function companyLine(now: Date = new Date()): string {
  return `© ${now.getFullYear()} ${UNIFYAI_COMPANY_NAME} · ${UNIFYAI_COMPANY_ADDRESS}`
}
