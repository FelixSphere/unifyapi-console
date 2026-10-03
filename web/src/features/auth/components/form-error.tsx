/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { AlertCircle } from 'lucide-react'

type FormErrorProps = {
  message?: string | null
}

/**
 * UNIFYAPI-BRAND: UI-STANDARD.md form-level error -- a box at the top of the
 * card with a 1px danger border on a white background, an icon and one
 * sentence. Never a red fill, and never a toast alone.
 */
export function FormError({ message }: FormErrorProps) {
  if (!message) return null
  return (
    <div
      role='alert'
      className='border-destructive bg-card text-destructive flex items-start gap-2 rounded-md border px-3 py-2 text-sm'
    >
      <AlertCircle className='mt-0.5 size-4 shrink-0' aria-hidden='true' />
      <span>{message}</span>
    </div>
  )
}
