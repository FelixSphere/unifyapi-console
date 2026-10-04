/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
type AuthHeadingProps = {
  title: string
  children?: React.ReactNode
}

/** UNIFYAPI-BRAND: the card's H1 (Space Grotesk 700, 24px) and its sub-line. */
export function AuthHeading({ title, children }: AuthHeadingProps) {
  return (
    <div className='space-y-2'>
      <h1 className='font-display text-foreground text-2xl leading-tight font-bold tracking-tight'>
        {title}
      </h1>
      {children}
    </div>
  )
}
