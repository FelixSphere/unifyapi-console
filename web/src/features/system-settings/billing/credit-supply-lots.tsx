/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, History, Pencil, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Progress } from '@/components/ui/progress'
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
import type { Channel } from '@/features/channels/types'

import {
  getCreditLotEvents,
  saveCreditLot,
  transitionCreditLot,
  type CreditLot,
  type CreditLotInput,
  type CreditSupplier,
} from './credit-supply-api'
import {
  LOT_STATUS_LABELS,
  availableTransitions,
  consumedPct,
  dateTimeInputValue,
  daysUntil,
  formatRate,
  formatUSD,
  lotHealth,
  payableUSD,
  remainingUSD,
  timestampFromInput,
  type LotHealth,
  type LotTransition,
} from './credit-supply-logic'

type LotFormState = {
  supplierId: string
  vendor: string
  channelId: string
  faceUSD: string
  rate: string
  lowWaterUSD: string
  expiresAt: string
  activate: boolean
  note: string
}

const emptyLotForm = (supplierId: string): LotFormState => ({
  supplierId,
  vendor: 'anthropic',
  channelId: '0',
  faceUSD: '',
  rate: '0.5',
  lowWaterUSD: '',
  expiresAt: '',
  activate: false,
  note: '',
})

const HEALTH_BADGE: Record<
  LotHealth,
  {
    labelKey: string
    variant: 'default' | 'secondary' | 'destructive' | 'outline'
  }
> = {
  pending: { labelKey: 'Pending approval', variant: 'outline' },
  healthy: { labelKey: 'Healthy', variant: 'default' },
  low: { labelKey: 'Running low', variant: 'destructive' },
  expiring: { labelKey: 'Expiring soon', variant: 'destructive' },
  suspended: { labelKey: 'Suspended', variant: 'secondary' },
  retired: { labelKey: 'Retired', variant: 'secondary' },
  rejected: { labelKey: 'Rejected', variant: 'secondary' },
}

export function CreditLotsPanel({
  lots,
  suppliers,
  channels,
  now,
}: {
  lots: CreditLot[]
  suppliers: CreditSupplier[]
  channels: Channel[]
  now: number
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['credit-supply'] })

  const [dialogOpen, setDialogOpen] = useState(false)
  const [editing, setEditing] = useState<CreditLot | null>(null)
  const [form, setForm] = useState<LotFormState>(
    emptyLotForm(String(suppliers[0]?.id ?? ''))
  )
  const [approving, setApproving] = useState<CreditLot | null>(null)
  const [provenanceConfirmed, setProvenanceConfirmed] = useState(false)
  // Rejection and suspension ask for the reason the supplier will read.
  const [reasoning, setReasoning] = useState<{
    lot: CreditLot
    transition: LotTransition
  } | null>(null)
  const [reason, setReason] = useState('')
  const [historyLot, setHistoryLot] = useState<CreditLot | null>(null)
  const history = useQuery({
    queryKey: ['credit-supply', 'lot-events', historyLot?.id ?? 0],
    queryFn: () => getCreditLotEvents(historyLot?.id ?? 0),
    enabled: historyLot !== null,
  })

  const supplierName = (id: number) =>
    suppliers.find((supplier) => supplier.id === id)?.name ?? `#${id}`
  const channelName = (id: number) => {
    if (id === 0) return t('Not bound')
    const channel = channels.find((item) => item.id === id)
    return channel ? `#${channel.id} ${channel.name}` : `#${id}`
  }

  const saveMutation = useMutation({
    mutationFn: saveCreditLot,
    onSuccess: () => {
      toast.success(t('Credit lot saved'))
      setDialogOpen(false)
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const transitionMutation = useMutation({
    mutationFn: transitionCreditLot,
    onSuccess: (lot) => {
      toast.success(
        t('Lot #{{id}} is now {{status}}', {
          id: lot.id,
          status: t(LOT_STATUS_LABELS[lot.status]),
        })
      )
      setApproving(null)
      setProvenanceConfirmed(false)
      setReasoning(null)
      setReason('')
      invalidate()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const openCreate = () => {
    setEditing(null)
    setForm(emptyLotForm(String(suppliers[0]?.id ?? '')))
    setDialogOpen(true)
  }

  const openEdit = (lot: CreditLot) => {
    setEditing(lot)
    setForm({
      supplierId: String(lot.supplier_id),
      vendor: lot.vendor,
      channelId: String(lot.channel_id),
      faceUSD: String(lot.face_value_usd),
      rate: String(lot.acquisition_rate),
      lowWaterUSD: lot.low_water_usd ? String(lot.low_water_usd) : '',
      expiresAt: dateTimeInputValue(lot.expires_at),
      activate: lot.status === 'active',
      note: lot.note,
    })
    setDialogOpen(true)
  }

  const submit = () => {
    const supplierId = Number(form.supplierId)
    const faceUSD = Number(form.faceUSD)
    const rate = Number(form.rate)
    const lowWaterUSD = form.lowWaterUSD === '' ? 0 : Number(form.lowWaterUSD)
    const channelId = Number(form.channelId)
    if (
      !Number.isInteger(supplierId) ||
      supplierId <= 0 ||
      !/^[a-z0-9][a-z0-9._-]{1,63}$/.test(form.vendor.trim().toLowerCase()) ||
      !Number.isFinite(faceUSD) ||
      faceUSD <= 0 ||
      !Number.isFinite(rate) ||
      rate <= 0 ||
      rate > 1 ||
      !Number.isFinite(lowWaterUSD) ||
      lowWaterUSD < 0 ||
      lowWaterUSD > faceUSD
    ) {
      toast.error(
        t(
          'Check the supplier, vendor, face value, rate (0–1) and low-water mark.'
        )
      )
      return
    }
    if (!editing && form.activate && channelId === 0) {
      toast.error(t('Bind the lot to a channel before activating it'))
      return
    }
    const lot: CreditLotInput = {
      supplier_id: supplierId,
      vendor: form.vendor.trim().toLowerCase(),
      channel_id: channelId,
      face_value_usd: faceUSD,
      acquisition_rate: rate,
      low_water_usd: lowWaterUSD,
      expires_at: timestampFromInput(form.expiresAt),
      status: !editing && form.activate ? 'active' : 'pending',
      note: form.note.trim(),
    }
    saveMutation.mutate({ id: editing?.id, lot })
  }

  const runTransition = (lot: CreditLot, transition: LotTransition) => {
    if (transition.to === 'active' && lot.status === 'pending') {
      setProvenanceConfirmed(false)
      setApproving(lot)
      return
    }
    if (transition.to === 'rejected' || transition.to === 'suspended') {
      setReason('')
      setReasoning({ lot, transition })
      return
    }
    transitionMutation.mutate({ id: lot.id, to: transition.to })
  }

  return (
    <div className='flex flex-col gap-4'>
      <div className='flex justify-end'>
        <Button
          type='button'
          size='sm'
          onClick={openCreate}
          disabled={suppliers.length === 0}
        >
          <Plus className='size-4' />
          {t('New lot')}
        </Button>
      </div>

      <div className='overflow-x-auto rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Lot')}</TableHead>
              <TableHead>{t('Supplier')}</TableHead>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead className='min-w-56'>{t('Drawn down')}</TableHead>
              <TableHead>{t('Rate')}</TableHead>
              <TableHead>{t('Payable')}</TableHead>
              <TableHead>{t('Expires')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='text-right'>{t('Actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {lots.map((lot) => {
              const health = lotHealth(lot, now)
              const badge = HEALTH_BADGE[health]
              return (
                <TableRow key={lot.id}>
                  <TableCell>
                    <div className='font-medium'>
                      #{lot.id} · {lot.vendor}
                    </div>
                    <div className='text-muted-foreground text-xs'>
                      {lot.source === 'supplier'
                        ? t('Submitted by supplier')
                        : t('Entered by operator')}
                    </div>
                  </TableCell>
                  <TableCell>{supplierName(lot.supplier_id)}</TableCell>
                  <TableCell>
                    <code className='text-xs'>
                      {channelName(lot.channel_id)}
                    </code>
                  </TableCell>
                  <TableCell>
                    <div className='flex items-baseline justify-between gap-2 text-sm'>
                      <span>
                        {formatUSD(lot.consumed_usd)}{' '}
                        <span className='text-muted-foreground'>
                          / {formatUSD(lot.face_value_usd)}
                        </span>
                      </span>
                      <span className='text-muted-foreground text-xs tabular-nums'>
                        {formatUSD(remainingUSD(lot))} {t('left')}
                      </span>
                    </div>
                    <Progress value={consumedPct(lot)} className='mt-1' />
                    {lot.unpriced_requests > 0 ? (
                      <div className='text-destructive mt-1 flex items-center gap-1 text-xs'>
                        <AlertTriangle className='size-3' />
                        {t(
                          '{{count}} requests on uncatalogued models drew nothing',
                          {
                            count: lot.unpriced_requests,
                          }
                        )}
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {formatRate(lot.acquisition_rate)}
                  </TableCell>
                  <TableCell className='tabular-nums'>
                    {formatUSD(payableUSD(lot))}
                  </TableCell>
                  <TableCell className='text-sm'>
                    {lot.expires_at
                      ? `${new Date(lot.expires_at * 1000).toLocaleDateString()} (${daysUntil(lot.expires_at, now)}d)`
                      : t('No expiry')}
                  </TableCell>
                  <TableCell>
                    <Badge variant={badge.variant}>{t(badge.labelKey)}</Badge>
                    {lot.status_reason ? (
                      <div className='text-muted-foreground mt-1 max-w-48 text-xs'>
                        {lot.status_reason}
                      </div>
                    ) : null}
                  </TableCell>
                  <TableCell className='text-right'>
                    <div className='flex justify-end gap-1'>
                      {availableTransitions(lot, now).map((transition) => (
                        <Button
                          key={transition.to}
                          type='button'
                          size='sm'
                          variant={
                            transition.destructive ? 'outline' : 'default'
                          }
                          disabled={
                            Boolean(transition.blockedKey) ||
                            transitionMutation.isPending
                          }
                          title={
                            transition.blockedKey
                              ? t(transition.blockedKey)
                              : undefined
                          }
                          onClick={() => runTransition(lot, transition)}
                        >
                          {t(transition.labelKey)}
                        </Button>
                      ))}
                      <Button
                        type='button'
                        size='icon-sm'
                        variant='ghost'
                        aria-label={t('Edit lot')}
                        onClick={() => openEdit(lot)}
                      >
                        <Pencil className='size-4' />
                      </Button>
                      <Button
                        type='button'
                        size='icon-sm'
                        variant='ghost'
                        aria-label={t('Lot history')}
                        onClick={() => setHistoryLot(lot)}
                      >
                        <History className='size-4' />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              )
            })}
            {lots.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={9}
                  className='text-muted-foreground text-center'
                >
                  {suppliers.length === 0
                    ? t('Add a supplier first, then record their credit lot.')
                    : t('No credit lots yet.')}
                </TableCell>
              </TableRow>
            ) : null}
          </TableBody>
        </Table>
      </div>

      <Dialog
        open={dialogOpen}
        onOpenChange={setDialogOpen}
        title={editing ? t('Edit credit lot') : t('New credit lot')}
        description={t(
          'Face value is what the credits are worth at the vendor’s list price. The acquisition rate is what we pay per $1 of it, and becomes the channel’s purchasing cost ratio when the lot activates.'
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
              disabled={saveMutation.isPending}
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        <div className='grid gap-4 sm:grid-cols-2'>
          <Field label={t('Supplier')}>
            <NativeSelect
              className='w-full'
              value={form.supplierId}
              disabled={Boolean(editing)}
              onChange={(event) =>
                setForm({ ...form, supplierId: event.target.value })
              }
            >
              {suppliers.map((supplier) => (
                <NativeSelectOption
                  key={supplier.id}
                  value={String(supplier.id)}
                >
                  {supplier.name} ({supplier.code})
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t('Vendor')}>
            <Input
              value={form.vendor}
              placeholder='anthropic'
              list='credit-supply-vendors'
              onChange={(event) =>
                setForm({ ...form, vendor: event.target.value })
              }
            />
            <datalist id='credit-supply-vendors'>
              <option value='openai' />
              <option value='anthropic' />
              <option value='google' />
              <option value='openrouter' />
            </datalist>
          </Field>
          <Field label={t('Channel carrying the supplier’s key')}>
            <NativeSelect
              className='w-full'
              value={form.channelId}
              onChange={(event) =>
                setForm({ ...form, channelId: event.target.value })
              }
            >
              <NativeSelectOption value='0'>
                {t('Not bound yet')}
              </NativeSelectOption>
              {channels.map((channel) => (
                <NativeSelectOption key={channel.id} value={String(channel.id)}>
                  #{channel.id} {channel.name}
                </NativeSelectOption>
              ))}
            </NativeSelect>
          </Field>
          <Field label={t('Face value (USD at list price)')}>
            <Input
              type='number'
              min='0'
              step='0.01'
              value={form.faceUSD}
              onChange={(event) =>
                setForm({ ...form, faceUSD: event.target.value })
              }
            />
          </Field>
          <Field label={t('Acquisition rate (we pay per $1)')}>
            <Input
              type='number'
              min='0'
              max='1'
              step='0.01'
              value={form.rate}
              onChange={(event) =>
                setForm({ ...form, rate: event.target.value })
              }
            />
            <p className='text-muted-foreground text-xs'>
              {Number.isFinite(Number(form.rate)) && Number(form.rate) > 0
                ? formatRate(Number(form.rate))
                : ''}
            </p>
          </Field>
          <Field label={t('Low-water alert (USD remaining, optional)')}>
            <Input
              type='number'
              min='0'
              step='0.01'
              value={form.lowWaterUSD}
              onChange={(event) =>
                setForm({ ...form, lowWaterUSD: event.target.value })
              }
            />
          </Field>
          <Field label={t('Expires at (optional)')}>
            <Input
              type='datetime-local'
              value={form.expiresAt}
              onChange={(event) =>
                setForm({ ...form, expiresAt: event.target.value })
              }
            />
          </Field>
          {!editing ? (
            <div className='flex items-end gap-3 pb-2'>
              <Switch
                checked={form.activate}
                onCheckedChange={(activate) => setForm({ ...form, activate })}
              />
              <Label>{t('Activate immediately')}</Label>
            </div>
          ) : null}
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

      <Dialog
        open={approving !== null}
        onOpenChange={(open) => {
          if (!open) setApproving(null)
        }}
        title={t('Approve credit lot #{{id}}?', { id: approving?.id ?? '' })}
        description={t(
          'Approving enables channel {{channel}}, writes the {{rate}} acquisition rate into its purchasing cost ratio, and starts drawing the lot down at list price.',
          {
            channel: approving ? channelName(approving.channel_id) : '',
            rate: approving ? formatRate(approving.acquisition_rate) : '',
          }
        )}
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setApproving(null)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              disabled={!provenanceConfirmed || transitionMutation.isPending}
              onClick={() => {
                if (!approving) return
                transitionMutation.mutate({
                  id: approving.id,
                  to: 'active',
                  transfer_rights_confirmed: true,
                })
              }}
            >
              {t('Approve')}
            </Button>
          </>
        }
      >
        <div className='flex items-start gap-3'>
          <Switch
            checked={provenanceConfirmed}
            onCheckedChange={setProvenanceConfirmed}
          />
          <Label className='leading-snug'>
            {t(
              'I have confirmed that this supplier has the right to transfer these credits to us under the vendor’s terms.'
            )}
          </Label>
        </div>
      </Dialog>

      <ReasonDialog
        reasoning={reasoning}
        reason={reason}
        setReason={setReason}
        pending={transitionMutation.isPending}
        onCancel={() => setReasoning(null)}
        onConfirm={() => {
          if (!reasoning) return
          transitionMutation.mutate({
            id: reasoning.lot.id,
            to: reasoning.transition.to,
            reason: reason.trim(),
          })
        }}
      />

      <Dialog
        open={historyLot !== null}
        onOpenChange={(open) => {
          if (!open) setHistoryLot(null)
        }}
        title={t('History of lot #{{id}}', { id: historyLot?.id ?? '' })}
        description={
          historyLot
            ? t('Attested by {{by}} under {{version}}', {
                by: historyLot.attested_by || '—',
                version: historyLot.attestation_version || '—',
              })
            : ''
        }
      >
        <div className='grid gap-2 text-sm'>
          {(history.data ?? []).map((event) => (
            <div key={event.id} className='rounded border px-3 py-2'>
              <div className='flex flex-wrap items-center justify-between gap-2'>
                <span className='font-medium'>
                  {event.event_type}
                  {event.to_status
                    ? ` → ${t(LOT_STATUS_LABELS[event.to_status as keyof typeof LOT_STATUS_LABELS] ?? event.to_status)}`
                    : ''}
                </span>
                <span className='text-muted-foreground text-xs'>
                  {event.actor} ·{' '}
                  {new Date(event.created_at * 1000).toLocaleString()}
                </span>
              </div>
              {event.message ? (
                <div className='text-muted-foreground mt-1 text-xs'>
                  {event.message}
                </div>
              ) : null}
            </div>
          ))}
          {history.isSuccess && (history.data ?? []).length === 0 ? (
            <p className='text-muted-foreground'>{t('No events recorded.')}</p>
          ) : null}
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

function ReasonDialog({
  reasoning,
  reason,
  setReason,
  pending,
  onCancel,
  onConfirm,
}: {
  reasoning: { lot: CreditLot; transition: LotTransition } | null
  reason: string
  setReason: (value: string) => void
  pending: boolean
  onCancel: () => void
  onConfirm: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      open={reasoning !== null}
      onOpenChange={(open) => {
        if (!open) onCancel()
      }}
      title={
        reasoning
          ? t('{{action}} lot #{{id}}', {
              action: t(reasoning.transition.labelKey),
              id: reasoning.lot.id,
            })
          : ''
      }
      description={t(
        'The supplier reads this reason in their portal, and it is kept in the lot’s history. Never paste a key here.'
      )}
      footer={
        <>
          <Button type='button' variant='outline' onClick={onCancel}>
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant={
              reasoning?.transition.destructive ? 'destructive' : 'default'
            }
            disabled={pending || reason.trim() === ''}
            onClick={onConfirm}
          >
            {reasoning ? t(reasoning.transition.labelKey) : ''}
          </Button>
        </>
      }
    >
      <Field label={t('Reason')}>
        <Textarea
          rows={3}
          value={reason}
          onChange={(event) => setReason(event.target.value)}
        />
      </Field>
    </Dialog>
  )
}
