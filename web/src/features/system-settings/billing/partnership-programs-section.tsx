/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Pencil, Plus, Trash2, Users } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { SettingsSection } from '../components/settings-section'
import {
  getPartnershipPrograms,
  removePartnershipCustomer,
  savePartnershipCustomer,
  savePartnershipProgram,
  type PartnershipCustomer,
  type PartnershipCustomerInput,
  type PartnershipProgram,
  type PartnershipProgramInput,
} from './partnership-api'
import { partnershipGroupLabel } from './partnership-group-label'

type FormState = {
  name: string
  code: string
  group: string
  grantUSD: string
  grantLimit: string
  enabled: boolean
  startsAt: string
  endsAt: string
}

type CustomerFormState = {
  name: string
  code: string
  group: string
  enabled: boolean
}

const emptyForm = (group: string): FormState => ({
  name: '',
  code: '',
  group,
  grantUSD: '0',
  grantLimit: '0',
  enabled: false,
  startsAt: '',
  endsAt: '',
})

const dateInput = (timestamp: number) =>
  timestamp ? new Date(timestamp * 1000).toISOString().slice(0, 16) : ''

const timestamp = (value: string) =>
  value ? Math.floor(new Date(value).getTime() / 1000) : 0

export function PartnershipProgramsSection({
  quotaPerUnit,
}: {
  quotaPerUnit: number
}) {
  const quotaUnit = quotaPerUnit > 0 ? quotaPerUnit : 500000
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const query = useQuery({
    queryKey: ['partnership-programs'],
    queryFn: getPartnershipPrograms,
  })
  const groups = useMemo(
    () => Object.keys(query.data?.groups ?? {}).sort(),
    [query.data?.groups]
  )
  const [editing, setEditing] = useState<PartnershipProgram | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [form, setForm] = useState<FormState>(emptyForm('default'))
  const [customerDialogOpen, setCustomerDialogOpen] = useState(false)
  const [customerProgram, setCustomerProgram] =
    useState<PartnershipProgram | null>(null)
  const [editingCustomer, setEditingCustomer] =
    useState<PartnershipCustomer | null>(null)
  const [removingCustomer, setRemovingCustomer] = useState<{
    program: PartnershipProgram
    customer: PartnershipCustomer
  } | null>(null)
  const [customerForm, setCustomerForm] = useState<CustomerFormState>({
    name: '',
    code: '',
    group: 'default',
    enabled: true,
  })

  useEffect(() => {
    if (!dialogOpen || editing || groups.length === 0) return
    setForm((current) => ({
      ...current,
      group: groups.includes(current.group) ? current.group : groups[0],
    }))
  }, [dialogOpen, editing, groups])

  const mutation = useMutation({
    mutationFn: savePartnershipProgram,
    onSuccess: () => {
      toast.success(t('Partnership program saved'))
      setDialogOpen(false)
      queryClient.invalidateQueries({ queryKey: ['partnership-programs'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const customerMutation = useMutation({
    mutationFn: savePartnershipCustomer,
    onSuccess: () => {
      toast.success(t('Partnership customer saved'))
      setCustomerDialogOpen(false)
      queryClient.invalidateQueries({ queryKey: ['partnership-programs'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const removeCustomerMutation = useMutation({
    mutationFn: removePartnershipCustomer,
    onSuccess: () => {
      toast.success(t('Customer group removed from program'))
      setRemovingCustomer(null)
      queryClient.invalidateQueries({ queryKey: ['partnership-programs'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const openCreate = () => {
    setEditing(null)
    setForm(emptyForm(groups[0] ?? 'default'))
    setDialogOpen(true)
  }

  const openEdit = (program: PartnershipProgram) => {
    setEditing(program)
    setForm({
      name: program.name,
      code: program.code,
      group: program.group,
      grantUSD: String(program.grant_quota / quotaUnit),
      grantLimit: String(program.grant_limit),
      enabled: program.enabled,
      startsAt: dateInput(program.starts_at),
      endsAt: dateInput(program.ends_at),
    })
    setDialogOpen(true)
  }

  const submit = () => {
    const grantUSD = Number(form.grantUSD)
    const grantLimit = Number(form.grantLimit)
    if (
      !form.name.trim() ||
      !/^[a-z0-9][a-z0-9_-]{2,63}$/.test(form.code.trim().toLowerCase()) ||
      !form.group ||
      !Number.isFinite(grantUSD) ||
      grantUSD < 0 ||
      !Number.isInteger(grantLimit) ||
      grantLimit < 0
    ) {
      toast.error(t('Check the name, code, group, credit, and limit.'))
      return
    }
    const program: PartnershipProgramInput = {
      name: form.name.trim(),
      code: form.code.trim().toLowerCase(),
      group: form.group,
      grant_quota: Math.round(grantUSD * quotaUnit),
      grant_limit: grantLimit,
      enabled: form.enabled,
      starts_at: timestamp(form.startsAt),
      ends_at: timestamp(form.endsAt),
    }
    mutation.mutate({ id: editing?.id, program })
  }

  const openCustomer = (
    program: PartnershipProgram,
    customer: PartnershipCustomer | null = null
  ) => {
    setCustomerProgram(program)
    setEditingCustomer(customer)
    setCustomerForm({
      name: customer?.name ?? '',
      code: customer?.code ?? '',
      group: customer?.group ?? groups[0] ?? 'default',
      enabled: customer?.enabled ?? true,
    })
    setCustomerDialogOpen(true)
  }

  const submitCustomer = () => {
    if (
      !customerProgram ||
      !customerForm.name.trim() ||
      !/^[a-z0-9][a-z0-9_-]{2,63}$/.test(
        customerForm.code.trim().toLowerCase()
      ) ||
      !customerForm.group
    ) {
      toast.error(t('Check the customer name, registration code, and group.'))
      return
    }
    const customer: PartnershipCustomerInput = {
      name: customerForm.name.trim(),
      code: customerForm.code.trim().toLowerCase(),
      group: customerForm.group,
      enabled: customerForm.enabled,
    }
    customerMutation.mutate({
      programId: customerProgram.id,
      id: editingCustomer?.id,
      customer,
    })
  }

  const copyLink = async (code: string) => {
    const link = `${window.location.origin}/sign-up?partnership=${encodeURIComponent(code)}`
    await navigator.clipboard.writeText(link)
    toast.success(t('Registration link copied'))
  }

  return (
    <SettingsSection title={t('Partnership Programs')}>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'A Program holds the shared registration credit, limit, and schedule. Each linked User Group is one customer and invoice owner; users are members of that customer. Each customer gets a dedicated registration link. All later top-ups and usage follow the normal payment and billing flow.'
          )}
        </AlertDescription>
      </Alert>

      <div className='flex justify-end'>
        <Button type='button' size='sm' onClick={openCreate}>
          <Plus className='size-4' />
          {t('New program')}
        </Button>
      </div>

      {query.isLoading && (
        <p className='text-muted-foreground text-sm'>{t('Loading...')}</p>
      )}
      {query.isError && (
        <Alert variant='destructive'>
          <AlertDescription>{(query.error as Error).message}</AlertDescription>
        </Alert>
      )}
      {!query.isLoading && !query.isError && (
        <div className='overflow-x-auto rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Program')}</TableHead>
                <TableHead>{t('Customer groups')}</TableHead>
                <TableHead>{t('Registration credit')}</TableHead>
                <TableHead>{t('Claims')}</TableHead>
                <TableHead>{t('Status')}</TableHead>
                <TableHead className='text-right'>{t('Actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {(query.data?.programs ?? []).map((program) => (
                <TableRow key={program.id}>
                  <TableCell>
                    <div className='font-medium'>{program.name}</div>
                    <code className='text-muted-foreground text-xs'>
                      {program.code}
                    </code>
                  </TableCell>
                  <TableCell className='min-w-72'>
                    <div className='grid gap-2'>
                      {(program.customers ?? []).map((customer) => (
                        <div
                          key={customer.id}
                          className='flex items-center justify-between gap-2 rounded border px-2 py-1'
                        >
                          <div>
                            <div className='text-sm font-medium'>
                              {customer.name}
                              {customer.is_default ? (
                                <span className='text-muted-foreground ml-1 text-xs'>
                                  ({t('default')})
                                </span>
                              ) : null}
                            </div>
                            <div className='text-muted-foreground text-xs'>
                              {customer.group} ·{' '}
                              {query.data?.group_ratios[customer.group] ?? 1}×
                            </div>
                          </div>
                          <div className='flex'>
                            <Button
                              type='button'
                              size='icon-sm'
                              variant='ghost'
                              disabled={!customer.enabled || !program.enabled}
                              aria-label={t('Copy customer registration link')}
                              onClick={() => copyLink(customer.code)}
                            >
                              <Copy className='size-4' />
                            </Button>
                            {!customer.is_default ? (
                              <>
                                <Button
                                  type='button'
                                  size='icon-sm'
                                  variant='ghost'
                                  aria-label={t('Edit customer')}
                                  onClick={() =>
                                    openCustomer(program, customer)
                                  }
                                >
                                  <Pencil className='size-4' />
                                </Button>
                                <Button
                                  type='button'
                                  size='icon-sm'
                                  variant='ghost'
                                  aria-label={t('Remove customer group')}
                                  onClick={() =>
                                    setRemovingCustomer({ program, customer })
                                  }
                                >
                                  <Trash2 className='size-4' />
                                </Button>
                              </>
                            ) : null}
                          </div>
                        </div>
                      ))}
                      <Button
                        type='button'
                        size='sm'
                        variant='outline'
                        onClick={() => openCustomer(program)}
                      >
                        <Users className='size-4' />
                        {t('Add customer group')}
                      </Button>
                    </div>
                  </TableCell>
                  <TableCell>
                    ${(program.grant_quota / quotaUnit).toFixed(2)}
                  </TableCell>
                  <TableCell>
                    {program.claimed_count} / {program.grant_limit}
                  </TableCell>
                  <TableCell>
                    <Badge variant={program.enabled ? 'default' : 'secondary'}>
                      {program.enabled ? t('Enabled') : t('Disabled')}
                    </Badge>
                  </TableCell>
                  <TableCell className='text-right'>
                    <Button
                      type='button'
                      size='icon-sm'
                      variant='ghost'
                      aria-label={t('Edit')}
                      onClick={() => openEdit(program)}
                    >
                      <Pencil className='size-4' />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {(query.data?.programs.length ?? 0) === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='text-muted-foreground text-center'
                  >
                    {t('No partnership programs yet.')}
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
      )}

      <Dialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title={
          editing ? t('Edit partnership program') : t('New partnership program')
        }
        description={t(
          'Create a dedicated registration link without changing normal billing.'
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
          <Field label={t('Program name')}>
            <Input
              value={form.name}
              onChange={(event) =>
                setForm({ ...form, name: event.target.value })
              }
            />
          </Field>
          <Field label={t('Registration code')}>
            <Input
              value={form.code}
              placeholder='partner-event'
              onChange={(event) =>
                setForm({ ...form, code: event.target.value })
              }
            />
          </Field>
          <Field label={t('Default customer group')}>
            <NativeSelect
              className='w-full'
              value={form.group}
              onChange={(event) =>
                setForm({ ...form, group: event.target.value })
              }
            >
              {groups.map((group) => (
                <NativeSelectOption key={group} value={group}>
                  {partnershipGroupLabel(group, query.data?.groups[group])}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t('Registration credit (USD)')}>
            <Input
              type='number'
              min='0'
              step='0.01'
              value={form.grantUSD}
              onChange={(event) =>
                setForm({ ...form, grantUSD: event.target.value })
              }
            />
          </Field>
          <Field label={t('Number of credited sign-ups')}>
            <Input
              type='number'
              min={editing?.claimed_count ?? 0}
              step='1'
              value={form.grantLimit}
              onChange={(event) =>
                setForm({ ...form, grantLimit: event.target.value })
              }
            />
          </Field>
          <div className='flex items-end gap-3 pb-2'>
            <Switch
              checked={form.enabled}
              onCheckedChange={(enabled) => setForm({ ...form, enabled })}
            />
            <Label>{t('Program enabled')}</Label>
          </div>
          <Field label={t('Starts at (optional)')}>
            <Input
              type='datetime-local'
              value={form.startsAt}
              onChange={(event) =>
                setForm({ ...form, startsAt: event.target.value })
              }
            />
          </Field>
          <Field label={t('Ends at (optional)')}>
            <Input
              type='datetime-local'
              value={form.endsAt}
              onChange={(event) =>
                setForm({ ...form, endsAt: event.target.value })
              }
            />
          </Field>
        </div>
      </Dialog>

      <Dialog
        open={customerDialogOpen}
        onOpenChange={setCustomerDialogOpen}
        title={
          editingCustomer
            ? t('Edit partnership customer')
            : t('Add partnership customer')
        }
        description={t(
          'Choose the existing User Group that owns this customer’s usage and invoice, then share its dedicated registration link.'
        )}
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setCustomerDialogOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              onClick={submitCustomer}
              disabled={customerMutation.isPending}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='grid gap-4 sm:grid-cols-2'>
          <Field label={t('Customer name')}>
            <Input
              value={customerForm.name}
              placeholder='Acme Vietnam'
              onChange={(event) =>
                setCustomerForm({ ...customerForm, name: event.target.value })
              }
            />
          </Field>
          <Field label={t('Registration code')}>
            <Input
              value={customerForm.code}
              placeholder='acme-vietnam'
              onChange={(event) =>
                setCustomerForm({ ...customerForm, code: event.target.value })
              }
            />
          </Field>
          <Field label={t('Customer User Group')}>
            <NativeSelect
              className='w-full'
              value={customerForm.group}
              onChange={(event) =>
                setCustomerForm({
                  ...customerForm,
                  group: event.target.value,
                })
              }
            >
              {groups.map((group) => (
                <NativeSelectOption key={group} value={group}>
                  {partnershipGroupLabel(group, query.data?.groups[group])}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <div className='flex items-end gap-3 pb-2'>
            <Switch
              checked={customerForm.enabled}
              onCheckedChange={(enabled) =>
                setCustomerForm({ ...customerForm, enabled })
              }
            />
            <Label>{t('Customer link enabled')}</Label>
          </div>
        </div>
      </Dialog>

      <AlertDialog
        open={removingCustomer !== null}
        onOpenChange={(open) => {
          if (!open) setRemovingCustomer(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Remove customer group?')}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                'This removes {{group}} from {{program}} and disables its registration link. Existing users, balances, usage, and invoice history are not deleted.',
                {
                  group: removingCustomer?.customer.group ?? '',
                  program: removingCustomer?.program.name ?? '',
                }
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={removeCustomerMutation.isPending}>
              {t('Cancel')}
            </AlertDialogCancel>
            <AlertDialogAction
              variant='destructive'
              disabled={
                removeCustomerMutation.isPending || removingCustomer === null
              }
              onClick={() => {
                if (!removingCustomer) return
                removeCustomerMutation.mutate({
                  programId: removingCustomer.program.id,
                  customerId: removingCustomer.customer.id,
                })
              }}
            >
              {t('Remove')}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </SettingsSection>
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
