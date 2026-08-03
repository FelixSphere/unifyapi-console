/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { ProjectAttribution } from '@/components/layout/components/footer'

/**
 * AGPLv3 s.7(b) attribution required by the upstream NOTICE file.
 *
 * DO NOT REMOVE, HIDE, CONDITIONALLY RENDER, OR RESTYLE TO REDUCE VISIBILITY.
 *
 * Why this component exists: upstream's <Footer /> mounts in exactly one
 * place, features/home/index.tsx, and Home() returns earlier whenever the
 * HomePageContent option is set -- which this deployment does set. Likewise
 * features/about/index.tsx only renders its attribution when the About option
 * is empty. Without an unconditional carrier, a logged-in product renders no
 * attribution at all.
 *
 * The notice text and the required link to https://github.com/QuantumNous/new-api
 * both live in footer.tsx and are reused from there verbatim -- nothing is
 * duplicated or reworded here, so upstream revisions propagate automatically.
 */
export function UpstreamAttribution() {
  return (
    <div className='border-border/40 bg-background text-muted-foreground/60 flex h-7 shrink-0 items-center justify-end border-t px-3 text-[11px] leading-none'>
      <ProjectAttribution currentYear={new Date().getFullYear()} inline />
    </div>
  )
}
