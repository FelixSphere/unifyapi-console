/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import React, { useCallback, useMemo, useState } from 'react'

import type { User } from '../types'

// UNIFYAPI-FORK: opens the "Send email" dialog from anywhere on the Users
// page. The default is null on purpose: a cell rendered outside the provider
// (a test, another page reusing the columns) shows plain text instead of a
// button that does nothing.

export type ComposeRecipient = Pick<
  User,
  'id' | 'username' | 'display_name' | 'email'
>

type AdminEmailContextValue = {
  openCompose: (recipients: ComposeRecipient[]) => void
  openTemplates: () => void
}

export const AdminEmailContext =
  React.createContext<AdminEmailContextValue | null>(null)

export type AdminEmailState = {
  recipients: ComposeRecipient[] | null
  templatesOpen: boolean
  closeCompose: () => void
  setTemplatesOpen: (open: boolean) => void
  value: AdminEmailContextValue
}

export function useAdminEmailState(): AdminEmailState {
  const [recipients, setRecipients] = useState<ComposeRecipient[] | null>(null)
  const [templatesOpen, setTemplatesOpen] = useState(false)
  const openCompose = useCallback(
    (list: ComposeRecipient[]) => setRecipients(list.length ? list : null),
    []
  )
  const openTemplates = useCallback(() => setTemplatesOpen(true), [])
  const value = useMemo(
    () => ({ openCompose, openTemplates }),
    [openCompose, openTemplates]
  )
  return {
    recipients,
    templatesOpen,
    closeCompose: () => setRecipients(null),
    setTemplatesOpen,
    value,
  }
}

// eslint-disable-next-line react-refresh/only-export-components
export function useAdminEmail() {
  return React.useContext(AdminEmailContext)
}
