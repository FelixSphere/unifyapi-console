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
//
// The same form takes the other deal: contribute the key instead of selling
// the credits and keep a share of what it earns. Only the money changes --
// verification, the write-only key and the attestation are the same either
// way, so they are not duplicated into a second form.

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
  contributableVendors,
  sharePreview,
  submitSupplierLot,
  type SupplierDeal,
  type SupplierLotSubmission,
  type SupplierTerms,
  type SupplierVendorPreset,
  payoutPreview,
} from '../api'

type FormState = {
  deal: SupplierDeal
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
  const sellableVendors = vendors.filter(
    (vendor) => (terms.buy_rates[vendor.key] ?? 0) > 0
  )
  const contributable = contributableVendors(terms, vendors)
  const [form, setForm] = useState<FormState>({
    deal: 'purchase',
    vendor: sellableVendors[0]?.key ?? '',
    faceUSD: '',
    expiresAt: '',
    upstreamKey: '',
    payoutMethod: 'platform_credit',
    payoutAccount: '',
    note: '',
    confirmed: false,
  })
  const contributing = form.deal === 'revenue_share'
  const sellable = contributing ? contributable : sellableVendors
  const preset = sellable.find((vendor) => vendor.key === form.vendor)
  const faceUSD = Number(form.faceUSD)
  const preview = payoutPreview(
    terms,
    form.vendor,
    Number.isFinite(faceUSD) ? faceUSD : 0,
    form.payoutMethod
  )
  const share = sharePreview(terms, form.vendor)
  // Switching deal can leave a vendor selected that the other deal is not open
  // for; move to the first one that is rather than submitting an invalid pair.
  const chooseDeal = (deal: SupplierDeal) => {
    const list = deal === 'revenue_share' ? contributable : sellableVendors
    const vendor = list.some((entry) => entry.key === form.vendor)
      ? form.vendor
      : (list[0]?.key ?? '')
    setForm({ ...form, deal, vendor })
  }

  const mutation = useMutation({
    mutationFn: submitSupplierLot,
    onSuccess: (result) => {
      toast.success(
        result.deal_type === 'revenue_share'
          ? t(
              'Verified. Key #{{id}} starts earning you {{share}}% of what it serves as soon as we accept it.',
              {
                id: result.lot_id,
                share: Math.round((result.revenue_share_pct ?? 0) * 100),
              }
            )
          : t(
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
      deal_type: form.deal,
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
      title={contributing ? t('Contribute a key') : t('Sell credits')}
      description={
        contributing
          ? t(
              'We verify the key with one small request while you wait. Nothing is paid up front: you keep a share of everything the key earns, for as long as it earns, and we settle it as it builds up.'
            )
          : t(
              'We verify the key with one small request while you wait, then pay you the posted rate. Your credits are only used after you have been paid.'
            )
      }
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
              : contributing
                ? t('Contribute for {{share}}%', {
                    share: Math.round(share * 100),
                  })
                : t('Sell for {{amount}}', {
                    amount: formatUSD(preview.amount),
                  })}
          </Button>
        </>
      }
    >
      <div className='grid gap-4'>
        <Field label={t('Which deal?')}>
          <div className='flex flex-wrap gap-2'>
            <Button
              type='button'
              size='sm'
              variant={contributing ? 'outline' : 'default'}
              onClick={() => chooseDeal('purchase')}
            >
              {t('Sell the credits')}
            </Button>
            <Button
              type='button'
              size='sm'
              variant={contributing ? 'default' : 'outline'}
              disabled={contributable.length === 0}
              onClick={() => chooseDeal('revenue_share')}
            >
              {t('Contribute the key for a share')}
            </Button>
          </div>
          <p className='text-muted-foreground text-xs'>
            {contributing
              ? t(
                  'Paid as it earns, with no cap: if the key serves a lot of traffic you are paid a lot. If it serves none, you are paid nothing.'
                )
              : t(
                  'One payment, made before a single request goes through your key.'
                )}
          </p>
        </Field>
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
                {contributing
                  ? t('you keep {{share}}%', {
                      share: Math.round(sharePreview(terms, vendor.key) * 100),
                    })
                  : t('we pay {{rate}}', {
                      rate: formatRate(terms.buy_rates[vendor.key] ?? 0),
                    })}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field
          label={
            contributing
              ? t('Credit balance on the key (USD, at the vendor’s list price)')
              : t(
                  'Credit balance you are selling (USD, at the vendor’s list price)'
                )
          }
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
            {contributing ? (
              <>
                {t(
                  'You keep {{share}}% of {{basis}} on every request your key serves, until the balance runs out or the key expires.',
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
              </>
            ) : (
              <>
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
              </>
            )}
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
              {contributing
                ? t(
                    'Platform credit — added to your balance each time we settle'
                  )
                : t('Platform credit — instant when the sale is settled')}
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
              {contributing
                ? t(
                    'I own or control this vendor account and have the right to let UnifyAPI consume these credits. I understand that nothing is paid up front, that what I am paid depends entirely on how much traffic the key serves, and that the credits are used by UnifyAPI customers.'
                  )
                : t(
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
