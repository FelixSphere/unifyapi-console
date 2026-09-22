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
//
// The rails come from the server, which derives them from Payment Settings --
// the same list, and the same names, the person sees when they top up. There
// is deliberately no copy of that list here: a client-side copy is a copy that
// drifts, and it would keep offering a rail after the operator switched it off.

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
  updateSupplierPayoutAccount,
  type PayoutRail,
  type SupplierPayoutAccount,
} from '../api'
import { splitOnChain } from '../lib/payout'

export function PayoutAccountDialog({
  open,
  onOpenChange,
  current,
  rails,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  current: SupplierPayoutAccount | null
  rails: PayoutRail[]
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<SupplierPayoutAccount>({
    method: '',
    holder: '',
    details: '',
    currency: 'USD',
  })
  const [network, setNetwork] = useState('')

  // The seller's own rail is always among the options, even once it is no
  // longer offered: the server sends it back for exactly that reason. Falling
  // back to the first available rail would silently move somebody off the
  // account they are being paid on.
  const firstAvailable = rails.find((rail) => rail.available)?.id ?? ''
  useEffect(() => {
    if (!open) return
    const method = current?.method || firstAvailable
    const details = current?.details ?? ''
    const parsed = splitOnChain(details)
    setForm({
      method,
      holder: current?.holder ?? '',
      details,
      currency: current?.currency || 'USD',
    })
    setNetwork(parsed.network)
  }, [open, current, firstAvailable])

  const mutation = useMutation({
    mutationFn: updateSupplierPayoutAccount,
    onSuccess: () => {
      toast.success(t('Payout account saved.'))
      onOpenChange(false)
      queryClient.invalidateQueries({ queryKey: ['supplier'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const rail = rails.find((entry) => entry.id === form.method)
  const onChain = Boolean(rail?.networks?.length)
  const address = onChain ? splitOnChain(form.details).address : form.details

  const setAddress = (value: string) =>
    setForm((previous) => ({
      ...previous,
      details: network ? `${network}:${value.trim()}` : value.trim(),
    }))
  const setNetworkAndDetails = (value: string) => {
    setNetwork(value)
    setForm((previous) => ({
      ...previous,
      details: `${value}:${splitOnChain(previous.details).address}`,
    }))
  }

  const submit = () => {
    if (!rail) {
      toast.error(t('Choose how you want to be paid.'))
      return
    }
    if (!rail.available && rail.id !== current?.method) {
      toast.error(rail.unavailable_reason || t('That method is unavailable.'))
      return
    }
    if (
      rail.needs_account &&
      (form.holder.trim() === '' || address.trim() === '')
    ) {
      toast.error(t('Enter the account holder and the account details.'))
      return
    }
    if (onChain && !network) {
      toast.error(t('Choose the network to send on.'))
      return
    }
    mutation.mutate({
      method: rail.id,
      holder: form.holder.trim(),
      details: form.details.trim(),
      currency: rail.currency || form.currency.trim().toUpperCase() || 'USD',
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
              setForm({ ...form, method: event.target.value })
            }
            data-testid='payout-method'
          >
            {rails.map((entry) => (
              <NativeSelectOption
                key={entry.id}
                value={entry.id}
                // An unavailable rail is shown rather than hidden so its
                // absence is explained, not guessed at -- except the one the
                // seller is already on, which stays selectable.
                disabled={
                  entry.available ? undefined : entry.id !== current?.method
                }
              >
                {t(entry.label)}
                {entry.available ? '' : ` — ${t('unavailable')}`}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        {rail && !rail.available && rail.unavailable_reason ? (
          <Alert>
            <AlertDescription className='text-sm'>
              {t(rail.unavailable_reason)}
            </AlertDescription>
          </Alert>
        ) : null}
        {rail?.needs_account ? (
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
            {onChain ? (
              <>
                <Field label={t('Network')}>
                  <NativeSelect
                    value={network}
                    onChange={(event) =>
                      setNetworkAndDetails(event.target.value)
                    }
                    data-testid='payout-network'
                  >
                    <NativeSelectOption value=''>
                      {t('Choose a network')}
                    </NativeSelectOption>
                    {rail.networks?.map((code) => (
                      <NativeSelectOption key={code} value={code}>
                        {code}
                      </NativeSelectOption>
                    ))}
                  </NativeSelect>
                </Field>
                <Field label={t('Receiving address')}>
                  <Input
                    value={address}
                    data-testid='payout-address'
                    placeholder={t(
                      'Paste the address exactly as your wallet shows it'
                    )}
                    onChange={(event) => setAddress(event.target.value)}
                  />
                </Field>
              </>
            ) : (
              <Field label={t('Account details')}>
                <Textarea
                  rows={3}
                  value={form.details}
                  placeholder={t(rail.hint ?? '')}
                  onChange={(event) =>
                    setForm({ ...form, details: event.target.value })
                  }
                />
              </Field>
            )}
            <Field label={t('Currency')}>
              {rail.currency ? (
                <p className='text-muted-foreground text-sm'>
                  {t('{{currency}} — set by this payout method, not by you.', {
                    currency: rail.currency,
                  })}
                </p>
              ) : (
                <Input
                  value={form.currency}
                  maxLength={5}
                  className='w-28 uppercase'
                  onChange={(event) =>
                    setForm({ ...form, currency: event.target.value })
                  }
                />
              )}
            </Field>
          </>
        ) : (
          <Alert>
            <AlertDescription className='text-sm'>
              {t(
                rail?.hint ??
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
