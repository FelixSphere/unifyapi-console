/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { Download, Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { downloadAuthenticatedFile } from '@/lib/authenticated-download'

export function VideoDownloadButton({ taskId }: { taskId: string }) {
  const { t } = useTranslation()
  const [downloading, setDownloading] = useState(false)
  const [failed, setFailed] = useState(false)

  async function download() {
    setDownloading(true)
    setFailed(false)
    try {
      await downloadAuthenticatedFile(
        `/v1/videos/${encodeURIComponent(taskId)}/content`,
        `${taskId}.mp4`
      )
    } catch {
      // The authenticated client displays the request error. Keep a local
      // failure indicator and allow retrying without submitting a new job.
      setFailed(true)
    } finally {
      setDownloading(false)
    }
  }

  return (
    <div className='flex items-center gap-2'>
      <Button
        variant='outline'
        size='sm'
        disabled={downloading}
        onClick={download}
      >
        {downloading ? (
          <Loader2 className='size-3 animate-spin' />
        ) : (
          <Download className='size-3' />
        )}
        {t('Download')}
      </Button>
      {failed && (
        <span role='alert' className='text-destructive text-xs'>
          {t('Failed')}
        </span>
      )}
    </div>
  )
}
