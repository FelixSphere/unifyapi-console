/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleDollarSign, KeyRound, RefreshCw } from 'lucide-react'
import { useState } from 'react'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import {
  cancelCreditContribution,
  formatCreditUsd,
  getMyCreditContributions,
  submitCreditContribution,
  usdToQuota,
  type CreditContribution,
} from './api'
import {
  contributionStatusTone,
  STATUS_LABELS,
  validateSupplierOffer,
} from './logic'

const emptyForm = {
  provider: 'openai',
  accountLabel: '',
  models: '',
  faceValue: 0,
  purchaseRatePercent: 20,
  supplierNotes: '',
  attested: false,
}

function StatusBadge({ contribution }: { contribution: CreditContribution }) {
  const status = contribution.effective_status || contribution.status
  const tone = contributionStatusTone(status)
  const styles = {
    success: 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
    danger: 'bg-destructive/10 text-destructive',
    info: 'bg-primary/10 text-primary',
    neutral: 'bg-muted text-muted-foreground',
  }[tone]
  return (
    <span
      className={`${styles} rounded-full px-2 py-0.5 text-[11px] font-medium`}
    >
      {STATUS_LABELS[status]}
    </span>
  )
}

function Metric(props: { label: string; value: string }) {
  return (
    <div className='bg-muted/40 rounded-lg p-2.5'>
      <div className='text-muted-foreground text-[11px]'>{props.label}</div>
      <div className='text-foreground mt-0.5 font-mono text-sm'>
        {props.value}
      </div>
    </div>
  )
}

export function SupplierCreditsCard() {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [form, setForm] = useState(emptyForm)
  const [error, setError] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['credit-contributions', 'self'],
    queryFn: getMyCreditContributions,
  })
  const submit = useMutation({
    mutationFn: async () => {
      const validation = validateSupplierOffer(form)
      if (validation) throw new Error(validation)
      return submitCreditContribution({
        provider: form.provider,
        account_label: form.accountLabel,
        models: form.models,
        requested_quota: usdToQuota(form.faceValue),
        requested_acquisition_ratio: form.purchaseRatePercent / 100,
        supplier_notes: form.supplierNotes,
        attested: form.attested,
      })
    },
    onSuccess: () => {
      setError(null)
      setForm(emptyForm)
      setOpen(false)
      void qc.invalidateQueries({ queryKey: ['credit-contributions', 'self'] })
    },
    onError: (e: Error) => setError(e.message),
  })
  const cancel = useMutation({
    mutationFn: cancelCreditContribution,
    onSuccess: () =>
      void qc.invalidateQueries({ queryKey: ['credit-contributions', 'self'] }),
    onError: (e: Error) => setError(e.message),
  })

  const contributions = query.data ?? []
  const available = contributions.reduce(
    (sum, contribution) => sum + contribution.available_payout_quota,
    0
  )
  const paid = contributions.reduce(
    (sum, contribution) =>
      sum +
      contribution.payouts
        .filter((payout) => payout.status === 'paid')
        .reduce((subtotal, payout) => subtotal + payout.amount_quota, 0),
    0
  )

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle className='flex items-center gap-2'>
            <CircleDollarSign className='size-4' /> Sell provider credits
          </CardTitle>
          <CardDescription>
            Offer unused or renewable provider credits to UnifyAI. Your tenant
            shares the proceeds, just like its wallet balance.
          </CardDescription>
          <CardAction>
            <Button size='sm' onClick={() => setOpen(true)}>
              Submit an offer
            </Button>
          </CardAction>
        </CardHeader>
        <CardContent className='space-y-4'>
          <div className='grid gap-2 sm:grid-cols-3'>
            <Metric label='Offers' value={String(contributions.length)} />
            <Metric
              label='Available payable'
              value={formatCreditUsd(available)}
            />
            <Metric label='Recorded paid' value={formatCreditUsd(paid)} />
          </div>

          <div className='flex gap-2 rounded-lg border border-amber-500/30 bg-amber-500/5 p-3 text-xs'>
            <KeyRound className='mt-0.5 size-4 shrink-0 text-amber-600' />
            <div>
              <strong>Never paste an API key here.</strong> After review, our
              operations team will arrange credential verification through the
              approved secure channel.
            </div>
          </div>

          {query.isLoading ? (
            <div className='text-muted-foreground py-4 text-center text-sm'>
              Loading credit offers…
            </div>
          ) : contributions.length ? (
            <div className='space-y-3'>
              {contributions.map((contribution) => (
                <div
                  key={contribution.id}
                  className='border-border rounded-lg border p-3'
                >
                  <div className='flex flex-wrap items-start justify-between gap-2'>
                    <div>
                      <div className='text-sm font-medium capitalize'>
                        {contribution.provider} ·{' '}
                        {contribution.account_label ||
                          `Offer #${contribution.id}`}
                      </div>
                      <div className='text-muted-foreground mt-0.5 text-xs'>
                        Requested{' '}
                        {formatCreditUsd(contribution.requested_quota)} at{' '}
                        {(
                          contribution.requested_acquisition_ratio * 100
                        ).toFixed(0)}
                        %
                        {contribution.models ? ` · ${contribution.models}` : ''}
                      </div>
                    </div>
                    <StatusBadge contribution={contribution} />
                  </div>
                  {contribution.status === 'active' ? (
                    <div className='mt-3 grid gap-2 sm:grid-cols-4'>
                      <Metric
                        label='Inventory left'
                        value={formatCreditUsd(
                          contribution.inventory_remaining
                        )}
                      />
                      <Metric
                        label='Consumed'
                        value={formatCreditUsd(contribution.consumed_quota)}
                      />
                      <Metric
                        label='Lifetime payable'
                        value={formatCreditUsd(
                          contribution.lifetime_payable_quota
                        )}
                      />
                      <Metric
                        label='Available payout'
                        value={formatCreditUsd(
                          contribution.available_payout_quota
                        )}
                      />
                    </div>
                  ) : null}
                  {contribution.rejection_reason ? (
                    <p className='text-destructive mt-2 text-xs'>
                      {contribution.rejection_reason}
                    </p>
                  ) : null}
                  {contribution.events?.length ? (
                    <p className='text-muted-foreground mt-2 text-xs'>
                      Latest: {contribution.events.at(-1)?.message}
                    </p>
                  ) : null}
                  {['submitted', 'needs_credentials'].includes(
                    contribution.status
                  ) ? (
                    <Button
                      variant='ghost'
                      size='sm'
                      className='mt-2'
                      disabled={cancel.isPending}
                      onClick={() => cancel.mutate(contribution.id)}
                    >
                      Cancel offer
                    </Button>
                  ) : null}
                </div>
              ))}
            </div>
          ) : (
            <div className='text-muted-foreground py-4 text-center text-sm'>
              No credit offers yet.
            </div>
          )}
          {error || query.error ? (
            <div className='text-destructive text-xs'>
              {error ?? query.error?.message}
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className='sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>Submit provider credits</DialogTitle>
            <DialogDescription>
              Submit account metadata only. We will verify the balance, expiry,
              transfer authorization, and final purchase rate before activation.
            </DialogDescription>
          </DialogHeader>
          <div className='grid gap-3 sm:grid-cols-2'>
            <div className='grid gap-1.5'>
              <Label htmlFor='credit-provider'>Provider</Label>
              <select
                id='credit-provider'
                className='border-input bg-background h-8 rounded-lg border px-2 text-sm'
                value={form.provider}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    provider: event.target.value,
                  }))
                }
              >
                <option value='openai'>OpenAI</option>
                <option value='anthropic'>Anthropic</option>
                <option value='other'>Other</option>
              </select>
            </div>
            <div className='grid gap-1.5'>
              <Label htmlFor='credit-label'>Account label</Label>
              <Input
                id='credit-label'
                placeholder='e.g. Startup grant'
                value={form.accountLabel}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    accountLabel: event.target.value,
                  }))
                }
              />
            </div>
            <div className='grid gap-1.5'>
              <Label htmlFor='credit-value'>Available face value (USD)</Label>
              <Input
                id='credit-value'
                type='number'
                min='0.01'
                step='0.01'
                value={form.faceValue || ''}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    faceValue: Number(event.target.value),
                  }))
                }
              />
            </div>
            <div className='grid gap-1.5'>
              <Label htmlFor='credit-rate'>Requested purchase rate (%)</Label>
              <Input
                id='credit-rate'
                type='number'
                min='0'
                max='100'
                value={form.purchaseRatePercent}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    purchaseRatePercent: Number(event.target.value),
                  }))
                }
              />
            </div>
            <div className='grid gap-1.5 sm:col-span-2'>
              <Label htmlFor='credit-models'>Models or scope</Label>
              <Input
                id='credit-models'
                placeholder='e.g. gpt-* or claude-*'
                value={form.models}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    models: event.target.value,
                  }))
                }
              />
            </div>
            <div className='grid gap-1.5 sm:col-span-2'>
              <Label htmlFor='credit-notes'>Notes</Label>
              <Textarea
                id='credit-notes'
                placeholder='Program name, renewal schedule, and expiry. No credentials.'
                value={form.supplierNotes}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    supplierNotes: event.target.value,
                  }))
                }
              />
            </div>
            <label className='flex items-start gap-2 text-xs sm:col-span-2'>
              <Checkbox
                checked={form.attested}
                onCheckedChange={(checked) =>
                  setForm((current) => ({
                    ...current,
                    attested: checked === true,
                  }))
                }
              />
              <span>
                I confirm my tenant lawfully owns or controls this provider
                account and is authorized to let UnifyAI use and resell its
                available capacity.
              </span>
            </label>
          </div>
          {error ? (
            <div className='text-destructive text-xs'>{error}</div>
          ) : null}
          <DialogFooter>
            <Button variant='outline' onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button disabled={submit.isPending} onClick={() => submit.mutate()}>
              {submit.isPending ? <RefreshCw className='animate-spin' /> : null}
              Submit for review
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
