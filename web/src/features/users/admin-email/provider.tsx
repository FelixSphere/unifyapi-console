/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import type React from 'react'

import { AdminEmailContext, useAdminEmailState } from './compose-context'
import { ComposeDialog } from './compose-dialog'
import { TemplatesDialog } from './templates-dialog'

// UNIFYAPI-FORK: wraps the Users page so a username, the bulk bar and the
// header button can all open the email dialogs.
export function AdminEmailProvider(props: { children: React.ReactNode }) {
  const state = useAdminEmailState()
  return (
    <AdminEmailContext.Provider value={state.value}>
      {props.children}
      {state.recipients && (
        <ComposeDialog
          key={state.recipients.map((r) => r.id).join(',')}
          recipients={state.recipients}
          onClose={state.closeCompose}
          onManageTemplates={() => state.setTemplatesOpen(true)}
        />
      )}
      <TemplatesDialog
        open={state.templatesOpen}
        onOpenChange={state.setTemplatesOpen}
      />
    </AdminEmailContext.Provider>
  )
}
