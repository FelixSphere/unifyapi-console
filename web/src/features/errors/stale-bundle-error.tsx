/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

/**
 * Shown by the root errorComponent when a route chunk failed to load: the tab
 * is running a bundle the server no longer serves (see lib/stale-bundle.ts).
 * Not an application error, so no 500, no "report an issue"; just reload.
 */
export function StaleBundleError() {
  const { t } = useTranslation()

  return (
    <div className='h-svh w-full'>
      <div className='m-auto flex h-full w-full flex-col items-center justify-center gap-2 px-4 text-center'>
        <span className='font-medium'>
          {t('A new version of the console is available.')}
        </span>
        <p className='text-muted-foreground'>
          {t('Reload the page to continue with the latest version.')}
        </p>
        <Button className='mt-6' onClick={() => window.location.reload()}>
          {t('Reload')}
        </Button>
      </div>
    </div>
  )
}
