/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { PasswordInput } from '@/components/password-input'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import {
  formatRate,
  timestampFromInput,
} from '@/features/system-settings/billing/credit-supply-logic'

import {
  submitSupplierLot,
  type SupplierLotSubmission,
  type SupplierVendorPreset,
} from '../api'

type FormState = {
  vendor: string
  faceUSD: string
  rate: string
  expiresAt: string
  upstreamKey: string
  models: string
  note: string
  confirmed: boolean
}

export function SubmitLotDialog({
  open,
  onOpenChange,
  vendors,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  vendors: SupplierVendorPreset[]
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<FormState>({
    vendor: vendors[0]?.key ?? '',
    faceUSD: '',
    rate: '0.5',
    expiresAt: '',
    upstreamKey: '',
    models: '',
    note: '',
    confirmed: false,
  })
  const preset = vendors.find((vendor) => vendor.key === form.vendor)

  const mutation = useMutation({
    mutationFn: submitSupplierLot,
    onSuccess: (result) => {
      toast.success(
        t(
          'Lot #{{id}} submitted. It stays pending until the operator approves it.',
          {
            id: result.lot_id,
          }
        )
      )
      setForm((current) => ({
        ...current,
        faceUSD: '',
        upstreamKey: '',
        models: '',
        note: '',
        confirmed: false,
      }))
      onOpenChange(false)
      queryClient.invalidateQueries({ queryKey: ['supplier'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const submit = () => {
    const faceUSD = Number(form.faceUSD)
    const rate = Number(form.rate)
    if (
      !preset ||
      !Number.isFinite(faceUSD) ||
      faceUSD <= 0 ||
      !Number.isFinite(rate) ||
      rate <= 0 ||
      rate > 1 ||
      form.upstreamKey.trim() === ''
    ) {
      toast.error(t('Check the vendor, face value, rate (0–1) and API key.'))
      return
    }
    if (!form.confirmed) {
      toast.error(
        t('Confirm that you have the right to transfer these credits.')
      )
      return
    }
    const submission: SupplierLotSubmission = {
      vendor: preset.key,
      face_value_usd: faceUSD,
      acquisition_rate: rate,
      expires_at: timestampFromInput(form.expiresAt),
      note: form.note.trim(),
      upstream_key: form.upstreamKey.trim(),
      models: form.models
        .split(/[\s,]+/)
        .map((name) => name.trim())
        .filter(Boolean),
      transfer_rights_confirmed: true,
    }
    mutation.mutate(submission)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Submit credits')}
      description={t(
        'Tell us what you hold and your asking rate. We create a channel for your key, disabled until an operator approves the lot. Your key is stored write-only and never shown again.'
      )}
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={submit}
            disabled={mutation.isPending || !form.confirmed}
          >
            {t('Submit for approval')}
          </Button>
        </>
      }
    >
      <div className='grid gap-4 sm:grid-cols-2'>
        <Field label={t('Vendor')}>
          <NativeSelect
            className='w-full'
            value={form.vendor}
            onChange={(event) =>
              setForm({ ...form, vendor: event.target.value, models: '' })
            }
          >
            {vendors.map((vendor) => (
              <NativeSelectOption key={vendor.key} value={vendor.key}>
                {vendor.label}
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
        <Field label={t('Asking rate (per $1 of face value)')}>
          <Input
            type='number'
            min='0'
            max='1'
            step='0.01'
            value={form.rate}
            onChange={(event) => setForm({ ...form, rate: event.target.value })}
          />
          <p className='text-muted-foreground text-xs'>
            {Number(form.rate) > 0 ? formatRate(Number(form.rate)) : ''}{' '}
            {t('— the operator may counter before approving.')}
          </p>
        </Field>
        <Field label={t('Credits expire at (optional)')}>
          <Input
            type='datetime-local'
            value={form.expiresAt}
            onChange={(event) =>
              setForm({ ...form, expiresAt: event.target.value })
            }
          />
        </Field>
        <div className='sm:col-span-2'>
          <Field label={t('Upstream API key for this vendor')}>
            <PasswordInput
              value={form.upstreamKey}
              autoComplete='off'
              onChange={(event) =>
                setForm({ ...form, upstreamKey: event.target.value })
              }
            />
          </Field>
        </div>
        <div className='sm:col-span-2'>
          <Field label={t('Models to offer (optional, comma-separated)')}>
            <Textarea
              rows={2}
              value={form.models}
              placeholder={
                preset && preset.models.length > 0
                  ? t(
                      'Leave empty for all {{count}} catalogued {{vendor}} models',
                      {
                        count: preset.models.length,
                        vendor: preset.label,
                      }
                    )
                  : ''
              }
              onChange={(event) =>
                setForm({ ...form, models: event.target.value })
              }
            />
          </Field>
        </div>
        <div className='sm:col-span-2'>
          <Field label={t('Note to the operator (optional)')}>
            <Textarea
              rows={2}
              value={form.note}
              onChange={(event) =>
                setForm({ ...form, note: event.target.value })
              }
            />
          </Field>
        </div>
        <div className='sm:col-span-2'>
          <Alert>
            <AlertDescription className='flex items-start gap-3 text-xs'>
              <Switch
                checked={form.confirmed}
                onCheckedChange={(confirmed) => setForm({ ...form, confirmed })}
              />
              <span>
                {t(
                  'I confirm that I hold these credits and have the right to transfer their use to UnifyAPI under the vendor’s terms. This attestation is recorded with the lot.'
                )}
              </span>
            </AlertDescription>
          </Alert>
        </div>
      </div>
    </Dialog>
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
