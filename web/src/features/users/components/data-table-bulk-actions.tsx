/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { type Table } from '@tanstack/react-table'
import { Mail } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { DataTableBulkActions as BulkActionsToolbar } from '@/components/data-table'
import { Button } from '@/components/ui/button'

// UNIFYAPI-FORK: "Send email" for the selected users (admin-email).
import { useAdminEmail } from '../admin-email/compose-context'
import { type User } from '../types'

interface DataTableBulkActionsProps {
  table: Table<User>
}

export function DataTableBulkActions({ table }: DataTableBulkActionsProps) {
  const { t } = useTranslation()
  const adminEmail = useAdminEmail()
  return (
    <BulkActionsToolbar table={table} entityName='user'>
      {/* UNIFYAPI-FORK: bulk email; the dialog lists who is skipped. */}
      {adminEmail && (
        <Button
          variant='outline'
          size='sm'
          data-admin-email-bulk
          onClick={() =>
            adminEmail.openCompose(
              table.getFilteredSelectedRowModel().rows.map((r) => r.original)
            )
          }
        >
          <Mail className='size-4' aria-hidden='true' />
          {t('Send email')}
        </Button>
      )}
    </BulkActionsToolbar>
  )
}
