/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Textarea } from '@/components/ui/textarea'

import {
  saveCreditSupplier,
  type CreditLot,
  type CreditSupplier,
  type CreditSupplierInput,
} from './credit-supply-api'
import { formatUSD, payableUSD } from './credit-supply-logic'

type SupplierFormState = {
  name: string
  code: string
  contactEmail: string
  userId: string
  active: boolean
  payoutTerms: string
  note: string
}

const emptySupplierForm = (): SupplierFormState => ({
  name: '',
  code: '',
  contactEmail: '',
  userId: '',
  active: true,
  payoutTerms: '',
  note: '',
})

export function CreditSuppliersPanel({
  suppliers,
  lots,
}: {
  suppliers: CreditSupplier[]
  lots: CreditLot[]
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<CreditSupplier | null>(null)
  const [form, setForm] = useState<SupplierFormState>(emptySupplierForm())

  const mutation = useMutation({
    mutationFn: saveCreditSupplier,
    onSuccess: () => {
      toast.success(t('Supplier saved'))
      setDialogOpen(false)
      queryClient.invalidateQueries({ queryKey: ['credit-supply'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const openCreate = () => {
    setEditing(null)
    setForm(emptySupplierForm())
    setDialogOpen(true)
  }

  const openEdit = (supplier: CreditSupplier) => {
    setEditing(supplier)
    setForm({
      name: supplier.name,
      code: supplier.code,
      contactEmail: supplier.contact_email,
      userId: supplier.user_id ? String(supplier.user_id) : '',
      active: supplier.status === 'active',
      payoutTerms: supplier.payout_terms,
      note: supplier.note,
    })
    setDialogOpen(true)
  }

  const submit = () => {
    const userId = form.userId === '' ? 0 : Number(form.userId)
    if (
      !form.name.trim() ||
      !/^[a-z0-9][a-z0-9_-]{2,63}$/.test(form.code.trim().toLowerCase()) ||
      (form.contactEmail.trim() !== '' && !form.contactEmail.includes('@')) ||
      !Number.isInteger(userId) ||
      userId < 0
    ) {
      toast.error(t('Check the name, code, contact email and login id.'))
      return
    }
    const supplier: CreditSupplierInput = {
      name: form.name.trim(),
      code: form.code.trim().toLowerCase(),
      contact_email: form.contactEmail.trim(),
      user_id: userId,
      status: form.active ? 'active' : 'suspended',
      payout_terms: form.payoutTerms.trim(),
      note: form.note.trim(),
    }
    mutation.mutate({ id: editing?.id, supplier })
  }

  const totalsFor = (supplierId: number) => {
    const own = lots.filter(
      (lot) => lot.supplier_id === supplierId && lot.status !== 'rejected'
    )
    return {
      lots: own.length,
      active: own.filter((lot) => lot.status === 'active').length,
      face: own.reduce((sum, lot) => sum + lot.face_value_usd, 0),
      consumed: own.reduce((sum, lot) => sum + lot.consumed_usd, 0),
      payable: own.reduce((sum, lot) => sum + payableUSD(lot), 0),
    }
  }

  return (
    <div className='flex flex-col gap-4'>
      <div className='flex justify-end'>
        <Button type='button' size='sm' onClick={openCreate}>
          <Plus className='size-4' />
          {t('New supplier')}
        </Button>
      </div>

      <div className='overflow-x-auto rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Supplier')}</TableHead>
              <TableHead>{t('Portal login')}</TableHead>
              <TableHead>{t('Lots')}</TableHead>
              <TableHead>{t('Face value')}</TableHead>
              <TableHead>{t('Consumed')}</TableHead>
              <TableHead>{t('Payable to date')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {suppliers.map((supplier) => {
              const totals = totalsFor(supplier.id)
              return (
                <TableRow key={supplier.id}>
                  <TableCell>
                    <div className='font-medium'>{supplier.name}</div>
                    <code className='text-muted-foreground text-xs'>
                      supplier:{supplier.code}
                    </code>
                    {supplier.contact_email ? (
                      <div className='text-muted-foreground text-xs'>
                        {supplier.contact_email}
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className='text-sm'>
                    {supplier.user_id ? (
                      <code className='text-xs'>user #{supplier.user_id}</code>
                    ) : (
                      <span className='text-muted-foreground'>
                        {t('Operator-managed')}
                      </span>
                    )}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {totals.active} / {totals.lots}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {formatUSD(totals.face)}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {formatUSD(totals.consumed)}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {formatUSD(totals.payable)}
                  </TableCell>
                  <TableCell>
                    <Badge
                      variant={
                        supplier.status === 'active' ? 'default' : 'secondary'
                      }
                    >
                      {supplier.status === 'active'
                        ? t('Active')
                        : t('Suspended')}
                    </Badge>
                  </TableCell>
                  <TableCell className='text-right'>
                    <Button
                      type='button'
                      size='icon-sm'
                      variant='ghost'
                      aria-label={t('Edit supplier')}
                      onClick={() => openEdit(supplier)}
                    >
                      <Pencil className='size-4' />
                    </Button>
                  </TableCell>
                </TableRow>
              )
            })}
            {suppliers.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={8}
                  className='text-muted-foreground text-center'
                >
                  {t('No suppliers yet.')}
                </TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>

      <Dialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title={editing ? t('Edit supplier') : t('New supplier')}
        description={t(
          'A supplier is settled under supplier:<code> on the vendor side of Settlement. Linking a console login lets that person use the supplier portal.'
        )}
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setDialogOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              onClick={submit}
              disabled={mutation.isPending}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='grid gap-4 sm:grid-cols-2'>
          <Field label={t('Supplier name')}>
            <Input
              value={form.name}
              placeholder='Acme Labs'
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
            />
          </Field>
          <Field label={t('Code')}>
            <Input
              value={form.code}
              placeholder='acme-labs'
              onChange={(event) =>
                setForm({ ...form, code: event.target.value })
              }
            />
          </Field>
          <Field label={t('Contact email (optional)')}>
            <Input
              type='email'
              value={form.contactEmail}
              onChange={(event) =>
                setForm({ ...form, contactEmail: event.target.value })
              }
            />
          </Field>
          <Field label={t('Portal login user id (optional)')}>
            <Input
              type='number'
              min='0'
              step='1'
              value={form.userId}
              onChange={(event) =>
                setForm({ ...form, userId: event.target.value })
              }
            />
          </Field>
          <div className='flex items-end gap-3 pb-2'>
            <Switch
              checked={form.active}
              onCheckedChange={(active) => setForm({ ...form, active })}
            />
            <Label>{t('Supplier active')}</Label>
          </div>
          <div className='sm:col-span-2'>
            <Field label={t('Payout terms (in words — never account numbers)')}>
              <Textarea
                value={form.payoutTerms}
                rows={2}
                placeholder={t(
                  'Monthly wire, net 15, against our vendor statement'
                )}
                onChange={(event) =>
                  setForm({ ...form, payoutTerms: event.target.value })
                }
              />
            </Field>
          </div>
          <div className='sm:col-span-2'>
            <Field label={t('Note')}>
              <Textarea
                value={form.note}
                rows={2}
                onChange={(event) =>
                  setForm({ ...form, note: event.target.value })
                }
              />
            </Field>
          </div>
        </div>
      </Dialog>
    </div>
  )
}

function Field({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <div className='grid gap-2'>
      <Label>{label}</Label>
      {children}
    </div>
  )
}
