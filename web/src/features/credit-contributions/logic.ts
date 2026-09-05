/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import type { ContributionStatus } from './api'

export const STATUS_LABELS: Record<ContributionStatus, string> = {
  submitted: 'Submitted',
  needs_credentials: 'Credentials requested',
  verifying: 'Under verification',
  active: 'Active',
  exhausted: 'Exhausted',
  expired: 'Expired',
  revoked: 'Revoked',
  rejected: 'Rejected',
  cancelled: 'Cancelled',
}

export function contributionStatusTone(status: ContributionStatus) {
  if (status === 'active') return 'success'
  if (status === 'rejected' || status === 'revoked') return 'danger'
  if (status === 'submitted' || status === 'verifying') return 'info'
  return 'neutral'
}

export function parseExpiry(date: string) {
  if (!date) return 0
  const unix = Math.floor(new Date(`${date}T23:59:59`).getTime() / 1000)
  return Number.isFinite(unix) ? unix : 0
}

export function validateSupplierOffer(input: {
  faceValue: number
  purchaseRatePercent: number
  attested: boolean
}) {
  if (!Number.isFinite(input.faceValue) || input.faceValue <= 0) {
    return 'Face value must be greater than zero.'
  }
  if (
    !Number.isFinite(input.purchaseRatePercent) ||
    input.purchaseRatePercent < 0 ||
    input.purchaseRatePercent > 100
  ) {
    return 'Requested purchase rate must be between 0% and 100%.'
  }
  if (!input.attested) return 'Authorization attestation is required.'
  return null
}
