/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { cn } from '@/lib/utils'

type BrandLockupProps = {
  className?: string
}

/**
 * UNIFYAPI-BRAND: the product's brand block, shared by every header surface.
 *
 * The UnifyAI lockup is fixed artwork (logo package README: "Do not retype
 * it"), so the header carries the SVG master from public/brand/, never the
 * `Logo` option's raster tile and never the system name set in a web font.
 * "API" is the product descriptor, typed in the display face per
 * UI-STANDARD.md, behind a 1x22px rule. Under 581px only the 32px mark shows.
 * The images set height only -- width follows the artwork's own ratio -- and
 * the link's accessible name is the sr-only product name, so the artwork is
 * decorative for assistive tech. The console is light-only (see
 * unifyapi.css), so the black artwork is the only variant needed.
 */
export function BrandLockup({ className }: BrandLockupProps) {
  return (
    <span className={cn('flex items-center gap-3', className)}>
      <img
        src='/brand/unifyai-lockup-horizontal-black.svg'
        alt=''
        loading='eager'
        decoding='async'
        className='hidden h-[51px] w-auto min-[581px]:block'
      />
      <img
        src='/brand/unifyai-mark-black.svg'
        alt=''
        loading='eager'
        decoding='async'
        className='h-8 w-auto min-[581px]:hidden'
      />
      <span
        aria-hidden='true'
        className='bg-border hidden h-[22px] w-px min-[581px]:block'
      />
      <span
        aria-hidden='true'
        className='font-display text-foreground hidden text-[17px] leading-none font-medium min-[581px]:block'
      >
        API
      </span>
      <span className='sr-only'>Unify API</span>
    </span>
  )
}
