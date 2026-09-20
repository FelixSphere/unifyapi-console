/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the one form a seller fills. The share is posted, not asked
// for; the key is verified while they wait; the answer is either "live, you
// keep X% of what it sells for" or the vendor's reason, right here. How they
// are paid is on their profile, so this form never asks.

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
  formatUSD,
  timestampFromInput,
} from '@/features/system-settings/billing/credit-supply-logic'

import {
  contributableVendors,
  sharePreview,
  submitSupplierLot,
  type SupplierLotSubmission,
  type SupplierTerms,
  type SupplierVendorPreset,
} from '../api'

type FormState = {
  vendor: string
  faceUSD: string
  expiresAt: string
  upstreamKey: string
  note: string
  confirmed: boolean
}

export function SubmitLotDialog({
  open,
  onOpenChange,
  vendors,
  terms,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  vendors: SupplierVendorPreset[]
  terms: SupplierTerms
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const offered = contributableVendors(terms, vendors)
  const [form, setForm] = useState<FormState>({
    vendor: offered[0]?.key ?? '',
    faceUSD: '',
    expiresAt: '',
    upstreamKey: '',
    note: '',
    confirmed: false,
  })
  const preset = offered.find((vendor) => vendor.key === form.vendor)
  const faceUSD = Number(form.faceUSD)
  const share = sharePreview(terms, form.vendor)

  const mutation = useMutation({
    mutationFn: submitSupplierLot,
    onSuccess: (result) => {
      toast.success(
        result.status === 'active'
          ? t(
              'Verified and live. Key #{{id}} is now serving customers; you keep {{share}}% of everything it sells.',
              {
                id: result.lot_id,
                share: Math.round((result.revenue_share_pct ?? 0) * 100),
              }
            )
          : t(
              'Verified. Key #{{id}} goes live as soon as the operator accepts it; you keep {{share}}% of everything it sells.',
              {
                id: result.lot_id,
                share: Math.round((result.revenue_share_pct ?? 0) * 100),
              }
            )
      )
      setForm((current) => ({
        ...current,
        faceUSD: '',
        upstreamKey: '',
        note: '',
        confirmed: false,
      }))
      onOpenChange(false)
      queryClient.invalidateQueries({ queryKey: ['supplier'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const submit = () => {
    if (
      !preset ||
      !Number.isFinite(faceUSD) ||
      faceUSD < terms.min_face_usd ||
      form.upstreamKey.trim() === ''
    ) {
      toast.error(
        t(
          'Check the vendor, the credit balance (at least {{min}}) and the API key.',
          { min: formatUSD(terms.min_face_usd) }
        )
      )
      return
    }
    if (!form.confirmed) {
      toast.error(
        t('Confirm that you have the right to let us use these credits.')
      )
      return
    }
    const submission: SupplierLotSubmission = {
      vendor: preset.key,
      face_value_usd: faceUSD,
      expires_at: timestampFromInput(form.expiresAt),
      note: form.note.trim(),
      upstream_key: form.upstreamKey.trim(),
      models: [],
      transfer_rights_confirmed: true,
    }
    mutation.mutate(submission)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Sell credits')}
      description={t(
        'We verify the key with one small request while you wait. Nothing is paid up front: your key starts serving paying customers, and you keep a share of everything it sells, settled as the balance builds up.'
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
            disabled={mutation.isPending || !preset}
          >
            {mutation.isPending
              ? t('Verifying key…')
              : t('Submit — you keep {{share}}%', {
                  share: Math.round(share * 100),
                })}
          </Button>
        </>
      }
    >
      <div className='grid gap-4'>
        <Field label={t('Vendor')}>
          <NativeSelect
            value={form.vendor}
            onChange={(event) =>
              setForm({ ...form, vendor: event.target.value })
            }
          >
            {offered.map((vendor) => (
              <NativeSelectOption key={vendor.key} value={vendor.key}>
                {vendor.label} —{' '}
                {t('you keep {{share}}%', {
                  share: Math.round(sharePreview(terms, vendor.key) * 100),
                })}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label={t(
            'Credit balance on the key (USD, at the vendor’s list price)'
          )}
        >
          <Input
            type='number'
            min={terms.min_face_usd}
            step='1'
            value={form.faceUSD}
            placeholder={String(terms.min_face_usd)}
            onChange={(event) =>
              setForm({ ...form, faceUSD: event.target.value })
            }
          />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Used to track how much is left; you are paid on what actually sells, so an estimate is fine.'
            )}
          </p>
        </Field>
        <Alert>
          <AlertDescription className='text-sm'>
            {t(
              'You keep {{share}}% of {{basis}} on every request your key serves, until the balance runs out or the key expires. Paid as it earns, with no cap: a busy key pays a lot, an idle one pays nothing.',
              {
                share: Math.round(share * 100),
                basis:
                  terms.revenue_share_basis === 'revenue'
                    ? t('what the customer pays')
                    : t('what we make after costs'),
              }
            )}
            {terms.min_share_payout_usd > 0
              ? ` ${t('We settle once you are owed {{min}}.', {
                  min: formatUSD(terms.min_share_payout_usd),
                })}`
              : null}
          </AlertDescription>
        </Alert>
        <Field label={t('Credits expire at (optional)')}>
          <Input
            type='datetime-local'
            value={form.expiresAt}
            onChange={(event) =>
              setForm({ ...form, expiresAt: event.target.value })
            }
          />
        </Field>
        {preset ? (
          <Field label={t('{{vendor}} API key', { vendor: preset.label })}>
            <PasswordInput
              value={form.upstreamKey}
              autoComplete='off'
              onChange={(event) =>
                setForm({ ...form, upstreamKey: event.target.value })
              }
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Write-only. It goes straight into a channel on our side and is verified with one request to {{host}}.',
                { host: preset.base_url }
              )}
            </p>
          </Field>
        ) : null}
        <Field label={t('Note (optional)')}>
          <Textarea
            rows={2}
            value={form.note}
            onChange={(event) => setForm({ ...form, note: event.target.value })}
          />
        </Field>
        <Alert>
          <AlertDescription className='flex items-start gap-3 text-xs'>
            <Switch
              checked={form.confirmed}
              onCheckedChange={(confirmed) => setForm({ ...form, confirmed })}
            />
            <span>
              {t(
                'I own or control this vendor account and have the right to let UnifyAPI consume these credits. I understand that nothing is paid up front, that what I am paid is a share of what the credits actually sell for, and that the credits are used by UnifyAPI customers.'
              )}
            </span>
          </AlertDescription>
        </Alert>
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
