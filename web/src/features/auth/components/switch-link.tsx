/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { Link } from '@tanstack/react-router'

type SwitchLinkProps = {
  prompt: string
  to: '/sign-in' | '/sign-up'
  label: string
}

/** UNIFYAPI-BRAND: the card's bottom line -- "No account? Sign up" and its mirror. */
export function SwitchLink({ prompt, to, label }: SwitchLinkProps) {
  return (
    <p className='text-muted-foreground mt-6 text-center text-sm'>
      {prompt}{' '}
      <Link
        to={to}
        className='text-foreground font-medium underline underline-offset-4'
      >
        {label}
      </Link>
    </p>
  )
}
