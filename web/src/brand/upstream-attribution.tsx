/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { ProjectAttribution } from '@/components/layout/components/footer'

import { companyLine } from './company'

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
    <div className='border-border/40 bg-background text-muted-foreground/60 flex min-h-7 shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t px-3 py-1 text-[11px] leading-none'>
      {/* UI-STANDARD.md company line: same in every UnifyAI product. It sits
          beside the upstream attribution, which is unchanged. */}
      <span className='font-mono'>{companyLine()}</span>
      <ProjectAttribution currentYear={new Date().getFullYear()} inline />
    </div>
  )
}
