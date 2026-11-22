/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: the dialog that marks a saved pricing group as an access tier,
or removes the mark.

It does not touch the form and saves no option. The server decides, and when
it refuses it says which wallet and which logins are in the way; that answer
stays in the dialog, because it is what the operator has to act on.
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'

import { setPricingGroupAccessTier } from '../api'
import { ACCESS_TIER_GROUPS_QUERY_KEY } from './group-access-tier-logic'

export type AccessTierChange = { group: string; accessTier: boolean }

type GroupAccessTierDialogProps = {
  /** What was asked for, or null while the dialog is closed. */
  change: AccessTierChange | null
  onClose: () => void
}

export function GroupAccessTierDialog(props: GroupAccessTierDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { change, onClose } = props
  // What the dialog is about stays on screen while it animates closed.
  const [shown, setShown] = useState<AccessTierChange>(
    change ?? { group: '', accessTier: true }
  )
  const [refusal, setRefusal] = useState<string | null>(null)

  useEffect(() => {
    if (change === null) return
    setShown(change)
    setRefusal(null)
  }, [change])

  const save = useMutation({
    mutationFn: (asked: AccessTierChange) =>
      setPricingGroupAccessTier(asked.group, asked.accessTier),
    onSuccess: (data, asked) => {
      if (!data.success) {
        setRefusal(data.message || t('Failed to change the access tier mark'))
        return
      }
      toast.success(
        asked.accessTier
          ? t('{{name}} is now an access tier', { name: asked.group })
          : t('{{name}} is no longer an access tier', { name: asked.group })
      )
      onClose()
      void queryClient.invalidateQueries({
        queryKey: ACCESS_TIER_GROUPS_QUERY_KEY,
      })
    },
    onError: (error: Error) => {
      setRefusal(error.message || t('Failed to change the access tier mark'))
    },
  })

  const confirmLabel = shown.accessTier
    ? t('Mark as access tier')
    : t('Remove mark')

  return (
    <ConfirmDialog
      open={change !== null}
      onOpenChange={(open) => {
        if (!open && !save.isPending) onClose()
      }}
      title={
        shown.accessTier
          ? t('Mark as an access tier')
          : t('Remove the access tier mark')
      }
      desc={
        shown.accessTier
          ? t(
              'Logins in {{name}} will each keep their own wallet, as logins in default do. Moving a login in or out of the group moves no balance. Prices and channel access do not change. Nothing is moved now: if logins in the group already share a wallet, this is refused and says which.',
              { name: shown.group }
            )
          : t(
              '{{name}} becomes a customer group again: logins placed in it share one wallet. This is refused while the group still has logins.',
              { name: shown.group }
            )
      }
      confirmText={save.isPending ? t('Saving...') : confirmLabel}
      handleConfirm={() => {
        if (change) save.mutate(change)
      }}
      isLoading={save.isPending}
    >
      {refusal !== null && (
        <p role='alert' className='text-destructive text-sm break-words'>
          {refusal}
        </p>
      )}
    </ConfirmDialog>
  )
}
