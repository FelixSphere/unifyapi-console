/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: the dialog that renames a saved pricing group.

It does not touch the form. The server renames every reference in one
transaction and the saved settings are then read again, so what the table
shows afterwards is what is stored.
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Input } from '@/components/ui/input'

import { renamePricingGroup } from '../api'
import { groupRenameProblem } from './group-rename-logic'

type GroupRenameDialogProps = {
  /** The saved group being renamed, or null while the dialog is closed. */
  group: string | null
  existingNames: readonly string[]
  onClose: () => void
}

export function GroupRenameDialog(props: GroupRenameDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { group, existingNames, onClose } = props
  const [name, setName] = useState('')
  // What the dialog is about stays on screen while it animates closed.
  const [shown, setShown] = useState(group ?? '')

  useEffect(() => {
    if (group === null) return
    setShown(group)
    setName(group)
  }, [group])

  const rename = useMutation({
    mutationFn: (to: string) => renamePricingGroup(group ?? '', to),
    onSuccess: (data, to) => {
      if (!data.success) {
        toast.error(data.message || t('Failed to rename group'))
        return
      }
      toast.success(t('Group renamed to {{name}}', { name: to }))
      for (const warning of data.data?.warnings ?? []) {
        toast.warning(warning)
      }
      onClose()
      void queryClient.invalidateQueries({ queryKey: ['system-options'] })
      void queryClient.invalidateQueries({ queryKey: ['group-model-pricing'] })
    },
    onError: (error: Error) => {
      toast.error(error.message || t('Failed to rename group'))
    },
  })

  const problem = group ? groupRenameProblem(group, name, existingNames) : null
  const untouched = name.trim() === group
  const submit = () => {
    if (!group || problem) return
    rename.mutate(name.trim())
  }

  return (
    <ConfirmDialog
      open={group !== null}
      onOpenChange={(open) => {
        if (!open && !rename.isPending) onClose()
      }}
      title={t('Rename group')}
      desc={t(
        'Renames {{name}} everywhere it is used: logins, API keys, the wallet, per-model prices and channel rules. No balance moves and no key changes.',
        { name: shown }
      )}
      confirmText={rename.isPending ? t('Renaming...') : t('Rename')}
      handleConfirm={submit}
      disabled={problem !== null}
      isLoading={rename.isPending}
    >
      <div className='space-y-2'>
        <Input
          value={name}
          aria-label={t('New group name')}
          placeholder={t('New group name')}
          aria-invalid={problem !== null && !untouched}
          autoFocus
          onChange={(event) => setName(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter') {
              event.preventDefault()
              submit()
            }
          }}
        />
        {problem !== null && !untouched && (
          <p className='text-destructive text-sm'>{t(problem)}</p>
        )}
      </div>
    </ConfirmDialog>
  )
}
