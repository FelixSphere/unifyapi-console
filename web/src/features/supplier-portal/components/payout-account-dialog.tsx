/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: where a seller's share is paid. Filed before the first sale,
// because with a share deal nothing is paid up front and the account has to be
// on record before the first dollar is owed rather than chased afterwards.

import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import {
  PAYOUT_METHOD_LABELS,
  updateSupplierPayoutAccount,
  type SupplierPayoutAccount,
  type SupplierPayoutMethod,
} from '../api'

const DETAIL_HINTS: Record<SupplierPayoutMethod, string> = {
  platform_credit: '',
  bank: 'Bank name, IBAN or account number, SWIFT/BIC, and your address if your bank needs it.',
  paypal: 'The e-mail address of your PayPal account.',
  wise: 'The e-mail on your Wise account, or the account details Wise shows you.',
  crypto: 'Wallet address and network (e.g. USDT on TRON, USDC on Base).',
}

export function PayoutAccountDialog({
  open,
  onOpenChange,
  current,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  current: SupplierPayoutAccount | null
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<SupplierPayoutAccount>({
    method: 'platform_credit',
    holder: '',
    details: '',
    currency: 'USD',
  })
  useEffect(() => {
    if (open) {
      setForm({
        method: current?.method || 'platform_credit',
        holder: current?.holder ?? '',
        details: current?.details ?? '',
        currency: current?.currency || 'USD',
      })
    }
  }, [open, current])

  const mutation = useMutation({
    mutationFn: updateSupplierPayoutAccount,
    onSuccess: () => {
      toast.success(t('Payout account saved.'))
      onOpenChange(false)
      queryClient.invalidateQueries({ queryKey: ['supplier'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const external = form.method !== 'platform_credit'
  const submit = () => {
    if (!form.method) {
      toast.error(t('Choose how you want to be paid.'))
      return
    }
    if (external && (form.holder.trim() === '' || form.details.trim() === '')) {
      toast.error(t('Enter the account holder and the account details.'))
      return
    }
    mutation.mutate({
      method: form.method,
      holder: form.holder.trim(),
      details: form.details.trim(),
      currency: form.currency.trim().toUpperCase() || 'USD',
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={
        current?.method ? t('Payout account') : t('Add your payout account')
      }
      description={t(
        'Where we send your share of what your credits sell for. We pay as the balance builds up; nothing is paid before your credits are used, so this has to be on file first.'
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
          <Button type='button' onClick={submit} disabled={mutation.isPending}>
            {t('Save payout account')}
          </Button>
        </>
      }
    >
      <div className='grid gap-4'>
        <Field label={t('How do you want to be paid?')}>
          <NativeSelect
            value={form.method}
            onChange={(event) =>
              setForm({
                ...form,
                method: event.target.value as SupplierPayoutMethod,
              })
            }
            data-testid='payout-method'
          >
            {(Object.keys(PAYOUT_METHOD_LABELS) as SupplierPayoutMethod[]).map(
              (method) => (
                <NativeSelectOption key={method} value={method}>
                  {t(PAYOUT_METHOD_LABELS[method])}
                </NativeSelectOption>
              )
            )}
          </NativeSelect>
        </Field>
        {external ? (
          <>
            <Field label={t('Account holder')}>
              <Input
                value={form.holder}
                placeholder={t(
                  'Name on the account, exactly as the bank has it'
                )}
                onChange={(event) =>
                  setForm({ ...form, holder: event.target.value })
                }
              />
            </Field>
            <Field label={t('Account details')}>
              <Textarea
                rows={3}
                value={form.details}
                placeholder={t(
                  DETAIL_HINTS[form.method as SupplierPayoutMethod] ?? ''
                )}
                onChange={(event) =>
                  setForm({ ...form, details: event.target.value })
                }
              />
            </Field>
            <Field label={t('Currency')}>
              <Input
                value={form.currency}
                maxLength={5}
                className='w-28 uppercase'
                onChange={(event) =>
                  setForm({ ...form, currency: event.target.value })
                }
              />
            </Field>
          </>
        ) : (
          <Alert>
            <AlertDescription className='text-sm'>
              {t(
                'Your share is added to the balance of this UnifyAPI account each time we settle. No details needed.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground text-xs'>
          {t(
            'Never paste an API key here. Only the operator who pays you can read these details.'
          )}
        </p>
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
