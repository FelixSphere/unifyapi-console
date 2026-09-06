/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the one form a seller fills. The price is posted, not asked
// for; the key is verified while they wait; the answer is either "verified,
// awaiting payment of $X" or the vendor's reason, right here.

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
  formatUSD,
  timestampFromInput,
} from '@/features/system-settings/billing/credit-supply-logic'

import {
  submitSupplierLot,
  type SupplierLotSubmission,
  type SupplierTerms,
  type SupplierVendorPreset,
  payoutPreview,
} from '../api'

type FormState = {
  vendor: string
  faceUSD: string
  expiresAt: string
  upstreamKey: string
  payoutMethod: 'platform_credit' | 'external'
  payoutAccount: string
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
  const sellable = vendors.filter(
    (vendor) => (terms.buy_rates[vendor.key] ?? 0) > 0
  )
  const [form, setForm] = useState<FormState>({
    vendor: sellable[0]?.key ?? '',
    faceUSD: '',
    expiresAt: '',
    upstreamKey: '',
    payoutMethod: 'platform_credit',
    payoutAccount: '',
    note: '',
    confirmed: false,
  })
  const preset = sellable.find((vendor) => vendor.key === form.vendor)
  const faceUSD = Number(form.faceUSD)
  const preview = payoutPreview(
    terms,
    form.vendor,
    Number.isFinite(faceUSD) ? faceUSD : 0,
    form.payoutMethod
  )

  const mutation = useMutation({
    mutationFn: submitSupplierLot,
    onSuccess: (result) => {
      toast.success(
        t(
          'Verified. Sale #{{id}} is awaiting payment of {{amount}}; your credits start being used once you have been paid.',
          { id: result.lot_id, amount: formatUSD(result.payout_usd ?? 0) }
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
          'Check the vendor, the face value (at least {{min}}) and the API key.',
          {
            min: formatUSD(terms.min_face_usd),
          }
        )
      )
      return
    }
    if (form.payoutMethod === 'external' && form.payoutAccount.trim() === '') {
      toast.error(t('Tell us where to send the payment.'))
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
      expires_at: timestampFromInput(form.expiresAt),
      payout_method: form.payoutMethod,
      payout_account: form.payoutAccount.trim(),
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
        'We verify the key with one small request while you wait, then pay you the posted rate. Your credits are only used after you have been paid.'
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
              : t('Sell for {{amount}}', { amount: formatUSD(preview.amount) })}
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
            {sellable.map((vendor) => (
              <NativeSelectOption key={vendor.key} value={vendor.key}>
                {vendor.label} —{' '}
                {t('we pay {{rate}}', {
                  rate: formatRate(terms.buy_rates[vendor.key] ?? 0),
                })}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label={t(
            'Credit balance you are selling (USD, at the vendor’s list price)'
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
        </Field>
        <Alert>
          <AlertDescription className='text-sm'>
            {t('You receive {{amount}} ({{rate}} of face value).', {
              amount: formatUSD(preview.amount),
              rate: formatRate(preview.rate),
            })}
            {form.payoutMethod === 'platform_credit' &&
            terms.platform_credit_bonus > 0
              ? ` ${t(
                  'Includes the {{bonus}}% bonus for taking platform credit.',
                  {
                    bonus: Math.round(terms.platform_credit_bonus * 100),
                  }
                )}`
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
                'Write-only. It goes straight into a disabled channel on our side and is verified with one request to {{host}}.',
                { host: preset.base_url }
              )}
            </p>
          </Field>
        ) : null}
        <Field label={t('How do you want to be paid?')}>
          <NativeSelect
            value={form.payoutMethod}
            onChange={(event) =>
              setForm({
                ...form,
                payoutMethod: event.target.value as FormState['payoutMethod'],
              })
            }
          >
            <NativeSelectOption value='platform_credit'>
              {t('Platform credit — instant when the sale is settled')}
            </NativeSelectOption>
            <NativeSelectOption value='external'>
              {t('Bank / PayPal / wallet transfer')}
            </NativeSelectOption>
          </NativeSelect>
        </Field>
        {form.payoutMethod === 'external' ? (
          <Field label={t('Where to send it')}>
            <Textarea
              rows={2}
              value={form.payoutAccount}
              placeholder={t(
                'e.g. PayPal ops@acme.example, or IBAN + account name'
              )}
              onChange={(event) =>
                setForm({ ...form, payoutAccount: event.target.value })
              }
            />
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
                'I own or control this vendor account and have the right to let UnifyAPI consume these credits. I understand payment is made once, at the posted rate, and that the credits are then used by UnifyAPI customers.'
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
