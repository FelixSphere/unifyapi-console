/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, ShieldCheck } from 'lucide-react'
import { Fragment, useState } from 'react'

import { Button } from '@/components/ui/button'
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
  activateCreditContribution,
  createContributionPayout,
  formatCreditUsd,
  getCreditContributions,
  resetCreditContribution,
  reviewCreditContribution,
  revokeCreditContribution,
  updateContributionPayout,
  usdToQuota,
  type ContributionPayout,
  type CreditContribution,
} from '@/features/credit-contributions/api'
import { STATUS_LABELS } from '@/features/credit-contributions/logic'
import { parseExpiry } from '@/features/credit-contributions/logic'

import { getCreditPools } from './api'
import { day, when } from './format'

type Action = 'reject' | 'activate' | 'reset' | 'revoke' | 'payout' | 'paid'

type DialogTarget = {
  action: Action
  contribution: CreditContribution
  payout?: ContributionPayout
}

const ACTION_TITLES: Record<Action, string> = {
  reject: 'Reject credit offer',
  activate: 'Activate verified credits',
  reset: 'Reset provider-credit cycle',
  revoke: 'Revoke contributed credits',
  payout: 'Create supplier payout',
  paid: 'Record payout as paid',
}

function emptyActionForm(contribution?: CreditContribution) {
  return {
    poolId: String(contribution?.pool_id || ''),
    channelId: String(contribution?.channel_id || ''),
    quotaUsd: String(
      (contribution?.approved_quota || contribution?.requested_quota || 0) /
        500_000
    ),
    ratePercent: String(
      (contribution?.acquisition_ratio ||
        contribution?.requested_acquisition_ratio ||
        0.2) * 100
    ),
    expiry: '',
    message: '',
    adminNotes: contribution?.admin_notes || '',
    payoutUsd: String((contribution?.available_payout_quota || 0) / 500_000),
    externalReference: '',
  }
}

function statusClass(status: string) {
  if (status === 'active') return 'bg-emerald-500/10 text-emerald-700'
  if (status === 'rejected' || status === 'revoked') {
    return 'bg-destructive/10 text-destructive'
  }
  return 'bg-muted text-muted-foreground'
}

export function CreditContributions() {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState<number | null>(null)
  const [target, setTarget] = useState<DialogTarget | null>(null)
  const [form, setForm] = useState(emptyActionForm())
  const [error, setError] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['ops-credit-contributions'],
    queryFn: getCreditContributions,
  })
  const pools = useQuery({
    queryKey: ['ops-credit-pools'],
    queryFn: getCreditPools,
  })

  const refresh = () => {
    setError(null)
    setTarget(null)
    void qc.invalidateQueries({ queryKey: ['ops-credit-contributions'] })
    void qc.invalidateQueries({ queryKey: ['ops-credit-pools'] })
  }
  const mutation = useMutation({
    mutationFn: async () => {
      if (!target) return
      const contribution = target.contribution
      if (target.action === 'reject') {
        await reviewCreditContribution(contribution.id, {
          status: 'rejected',
          message: form.message,
          admin_notes: form.adminNotes,
        })
      } else if (target.action === 'activate') {
        await activateCreditContribution(contribution.id, {
          pool_id: Number(form.poolId),
          channel_id: Number(form.channelId),
          approved_quota: usdToQuota(Number(form.quotaUsd)),
          acquisition_ratio: Number(form.ratePercent) / 100,
          expires_at: parseExpiry(form.expiry),
          admin_notes: form.adminNotes,
        })
      } else if (target.action === 'reset') {
        await resetCreditContribution(contribution.id, {
          verified_quota: usdToQuota(Number(form.quotaUsd)),
          expires_at: parseExpiry(form.expiry),
          reason: form.message,
        })
      } else if (target.action === 'revoke') {
        await revokeCreditContribution(contribution.id, form.message)
      } else if (target.action === 'payout') {
        await createContributionPayout(contribution.id, {
          amount_quota: usdToQuota(Number(form.payoutUsd)),
          note: form.message,
        })
      } else if (target.action === 'paid' && target.payout) {
        await updateContributionPayout(target.payout.id, {
          status: 'paid',
          external_reference: form.externalReference,
        })
      }
    },
    onSuccess: refresh,
    onError: (e: Error) => setError(e.message),
  })
  const quick = useMutation({
    mutationFn: async (input: {
      contribution: CreditContribution
      action: 'needs_credentials' | 'verifying' | 'approve' | 'void'
      payout?: ContributionPayout
    }) => {
      if (input.action === 'approve' && input.payout) {
        return updateContributionPayout(input.payout.id, { status: 'approved' })
      }
      if (input.action === 'void' && input.payout) {
        return updateContributionPayout(input.payout.id, { status: 'void' })
      }
      const message =
        input.action === 'needs_credentials'
          ? 'Operations will contact you to verify credentials securely. Do not send API keys in this form.'
          : 'Provider balance and authorization are being verified.'
      return reviewCreditContribution(input.contribution.id, {
        status: input.action,
        message,
        admin_notes: input.contribution.admin_notes || '',
      })
    },
    onSuccess: refresh,
    onError: (e: Error) => setError(e.message),
  })

  const openAction = (
    action: Action,
    contribution: CreditContribution,
    payout?: ContributionPayout
  ) => {
    setError(null)
    setForm(emptyActionForm(contribution))
    setTarget({ action, contribution, payout })
  }

  const contributions = query.data ?? []
  const pending = contributions.filter((item) =>
    ['submitted', 'needs_credentials', 'verifying'].includes(item.status)
  ).length
  const availablePayable = contributions.reduce(
    (sum, item) => sum + item.available_payout_quota,
    0
  )

  return (
    <section className='border-border bg-card mb-6 overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center justify-between gap-3 p-4'>
        <div>
          <h2 className='text-foreground flex items-center gap-2 text-base'>
            <ShieldCheck className='size-4' /> Supplier credit offers
          </h2>
          <p className='text-muted-foreground mt-0.5 text-xs'>
            {pending} awaiting review · {formatCreditUsd(availablePayable)}{' '}
            available payable
          </p>
        </div>
        <Button
          variant='outline'
          size='sm'
          onClick={() => void query.refetch()}
        >
          Refresh
        </Button>
      </div>
      {error || query.error ? (
        <div className='text-destructive border-border border-t px-4 py-2 text-xs'>
          {error ?? query.error?.message}
        </div>
      ) : null}
      <div className='overflow-x-auto border-t'>
        <table className='w-full text-sm'>
          <thead className='bg-muted/50 text-muted-foreground text-left text-xs'>
            <tr>
              <th className='w-8 px-3 py-2.5' />
              <th className='px-3 py-2.5 font-normal'>Supplier / provider</th>
              <th className='px-3 py-2.5 text-right font-normal'>Offered</th>
              <th className='px-3 py-2.5 text-right font-normal'>Inventory</th>
              <th className='px-3 py-2.5 text-right font-normal'>Payable</th>
              <th className='px-3 py-2.5 font-normal'>Status</th>
              <th className='px-3 py-2.5 text-right font-normal'>Actions</th>
            </tr>
          </thead>
          <tbody>
            {query.isLoading ? (
              <tr>
                <td
                  colSpan={7}
                  className='text-muted-foreground p-6 text-center'
                >
                  Loading offers…
                </td>
              </tr>
            ) : contributions.length === 0 ? (
              <tr>
                <td
                  colSpan={7}
                  className='text-muted-foreground p-6 text-center'
                >
                  No supplier offers yet.
                </td>
              </tr>
            ) : (
              contributions.map((contribution) => (
                <Fragment key={contribution.id}>
                  <tr className='border-border/60 border-t first:border-t-0'>
                    <td className='px-3 py-3'>
                      <button
                        type='button'
                        onClick={() =>
                          setExpanded(
                            expanded === contribution.id
                              ? null
                              : contribution.id
                          )
                        }
                      >
                        {expanded === contribution.id ? (
                          <ChevronDown className='size-4' />
                        ) : (
                          <ChevronRight className='size-4' />
                        )}
                      </button>
                    </td>
                    <td className='px-3 py-3'>
                      <div className='font-medium'>
                        Tenant #{contribution.tenant_id} ·{' '}
                        {contribution.provider}
                      </div>
                      <div className='text-muted-foreground text-xs'>
                        {contribution.account_label ||
                          `Offer #${contribution.id}`}{' '}
                        · {contribution.models || 'all models'}
                      </div>
                    </td>
                    <td className='px-3 py-3 text-right font-mono'>
                      {formatCreditUsd(contribution.requested_quota)}
                    </td>
                    <td className='px-3 py-3 text-right font-mono'>
                      {formatCreditUsd(contribution.inventory_remaining)}
                    </td>
                    <td className='px-3 py-3 text-right font-mono'>
                      {formatCreditUsd(contribution.available_payout_quota)}
                    </td>
                    <td className='px-3 py-3'>
                      <span
                        className={`${statusClass(contribution.effective_status)} rounded-full px-2 py-0.5 text-[11px]`}
                      >
                        {STATUS_LABELS[contribution.effective_status]}
                      </span>
                    </td>
                    <td className='px-3 py-3'>
                      <div className='flex flex-wrap justify-end gap-1.5'>
                        {['submitted', 'needs_credentials'].includes(
                          contribution.status
                        ) ? (
                          <Button
                            variant='outline'
                            size='sm'
                            disabled={quick.isPending}
                            onClick={() =>
                              quick.mutate({
                                contribution,
                                action: 'needs_credentials',
                              })
                            }
                          >
                            Request credentials
                          </Button>
                        ) : null}
                        {[
                          'submitted',
                          'needs_credentials',
                          'verifying',
                        ].includes(contribution.status) ? (
                          <>
                            <Button
                              variant='outline'
                              size='sm'
                              disabled={quick.isPending}
                              onClick={() =>
                                quick.mutate({
                                  contribution,
                                  action: 'verifying',
                                })
                              }
                            >
                              Verify
                            </Button>
                            <Button
                              size='sm'
                              onClick={() =>
                                openAction('activate', contribution)
                              }
                            >
                              Activate
                            </Button>
                            <Button
                              variant='destructive'
                              size='sm'
                              onClick={() => openAction('reject', contribution)}
                            >
                              Reject
                            </Button>
                          </>
                        ) : null}
                        {contribution.status === 'active' ? (
                          <>
                            <Button
                              variant='outline'
                              size='sm'
                              onClick={() => openAction('reset', contribution)}
                            >
                              Reset cycle
                            </Button>
                            <Button
                              variant='destructive'
                              size='sm'
                              onClick={() => openAction('revoke', contribution)}
                            >
                              Revoke
                            </Button>
                          </>
                        ) : null}
                        {contribution.available_payout_quota > 0 ? (
                          <Button
                            variant='outline'
                            size='sm'
                            onClick={() => openAction('payout', contribution)}
                          >
                            Create payout
                          </Button>
                        ) : null}
                      </div>
                    </td>
                  </tr>
                  {expanded === contribution.id ? (
                    <tr className='border-border/60 border-t'>
                      <td colSpan={7} className='bg-muted/20 px-4 py-3'>
                        <div className='grid gap-4 lg:grid-cols-3'>
                          <div className='text-xs'>
                            <div className='font-medium'>Accounting</div>
                            <div className='text-muted-foreground mt-1 space-y-1'>
                              <div>
                                Buy rate:{' '}
                                {(contribution.acquisition_ratio * 100).toFixed(
                                  1
                                )}
                                %
                              </div>
                              <div>
                                Consumed:{' '}
                                {formatCreditUsd(contribution.consumed_quota)}
                              </div>
                              <div>
                                Lifetime payable:{' '}
                                {formatCreditUsd(
                                  contribution.lifetime_payable_quota
                                )}
                              </div>
                              <div>
                                Cycle: {contribution.cycle || '—'} · expires{' '}
                                {day(contribution.expires_at)}
                              </div>
                              <div>
                                Pool #{contribution.pool_id || '—'} · channel #
                                {contribution.channel_id || '—'}
                              </div>
                            </div>
                          </div>
                          <div className='text-xs'>
                            <div className='font-medium'>Timeline</div>
                            <div className='text-muted-foreground mt-1 space-y-1'>
                              {contribution.events
                                ?.slice(-4)
                                .reverse()
                                .map((event) => (
                                  <div key={event.id}>
                                    {when(event.created_at)} · {event.message}
                                  </div>
                                ))}
                            </div>
                          </div>
                          <div className='text-xs'>
                            <div className='font-medium'>Payouts</div>
                            <div className='mt-1 space-y-2'>
                              {contribution.payouts?.length ? (
                                contribution.payouts.map((payout) => (
                                  <div
                                    key={payout.id}
                                    className='flex items-center justify-between gap-2'
                                  >
                                    <span className='text-muted-foreground'>
                                      {formatCreditUsd(payout.amount_quota)} ·{' '}
                                      {payout.status}
                                    </span>
                                    <span className='flex gap-1'>
                                      {payout.status === 'draft' ? (
                                        <>
                                          <Button
                                            size='sm'
                                            variant='outline'
                                            onClick={() =>
                                              quick.mutate({
                                                contribution,
                                                action: 'approve',
                                                payout,
                                              })
                                            }
                                          >
                                            Approve
                                          </Button>
                                          <Button
                                            size='sm'
                                            variant='ghost'
                                            onClick={() =>
                                              quick.mutate({
                                                contribution,
                                                action: 'void',
                                                payout,
                                              })
                                            }
                                          >
                                            Void
                                          </Button>
                                        </>
                                      ) : null}
                                      {payout.status === 'approved' ? (
                                        <>
                                          <Button
                                            size='sm'
                                            onClick={() =>
                                              openAction(
                                                'paid',
                                                contribution,
                                                payout
                                              )
                                            }
                                          >
                                            Mark paid
                                          </Button>
                                          <Button
                                            size='sm'
                                            variant='ghost'
                                            onClick={() =>
                                              quick.mutate({
                                                contribution,
                                                action: 'void',
                                                payout,
                                              })
                                            }
                                          >
                                            Void
                                          </Button>
                                        </>
                                      ) : null}
                                    </span>
                                  </div>
                                ))
                              ) : (
                                <span className='text-muted-foreground'>
                                  No payouts.
                                </span>
                              )}
                            </div>
                          </div>
                        </div>
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              ))
            )}
          </tbody>
        </table>
      </div>

      <Dialog
        open={target !== null}
        onOpenChange={(open) => !open && setTarget(null)}
      >
        <DialogContent className='sm:max-w-lg'>
          <DialogHeader>
            <DialogTitle>
              {target ? ACTION_TITLES[target.action] : ''}
            </DialogTitle>
            <DialogDescription>
              {target?.action === 'reset'
                ? 'This closes the previous inventory lot and creates a new immutable provider-credit cycle.'
                : target?.action === 'activate'
                  ? 'The channel must already be enabled and belong to the selected pool routing group.'
                  : 'This action is recorded in the supplier timeline and operations audit log.'}
            </DialogDescription>
          </DialogHeader>
          {target?.action === 'activate' ? (
            <div className='grid gap-3 sm:grid-cols-2'>
              <div className='grid gap-1.5'>
                <Label htmlFor='ops-pool'>Pool</Label>
                <select
                  id='ops-pool'
                  className='border-input bg-background h-8 rounded-lg border px-2 text-sm'
                  value={form.poolId}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      poolId: event.target.value,
                    }))
                  }
                >
                  <option value=''>Select a pool</option>
                  {pools.data?.map((pool) => (
                    <option key={pool.id} value={pool.id}>
                      {pool.name} · {pool.routing_group}
                    </option>
                  ))}
                </select>
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='ops-channel'>Channel ID</Label>
                <Input
                  id='ops-channel'
                  type='number'
                  min='1'
                  value={form.channelId}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      channelId: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='ops-quota'>Verified value (USD)</Label>
                <Input
                  id='ops-quota'
                  type='number'
                  min='0.01'
                  step='0.01'
                  value={form.quotaUsd}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      quotaUsd: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='ops-rate'>Acquisition rate (%)</Label>
                <Input
                  id='ops-rate'
                  type='number'
                  min='0.01'
                  max='100'
                  value={form.ratePercent}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      ratePercent: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='ops-expiry'>Expiry</Label>
                <Input
                  id='ops-expiry'
                  type='date'
                  value={form.expiry}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      expiry: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5 sm:col-span-2'>
                <Label htmlFor='ops-notes'>Internal notes</Label>
                <Textarea
                  id='ops-notes'
                  value={form.adminNotes}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      adminNotes: event.target.value,
                    }))
                  }
                />
              </div>
            </div>
          ) : null}
          {target?.action === 'reset' ? (
            <div className='grid gap-3'>
              <div className='grid gap-1.5'>
                <Label htmlFor='reset-quota'>New verified value (USD)</Label>
                <Input
                  id='reset-quota'
                  type='number'
                  min='0.01'
                  step='0.01'
                  value={form.quotaUsd}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      quotaUsd: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='reset-expiry'>New expiry</Label>
                <Input
                  id='reset-expiry'
                  type='date'
                  value={form.expiry}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      expiry: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='reset-reason'>Reason</Label>
                <Textarea
                  id='reset-reason'
                  value={form.message}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      message: event.target.value,
                    }))
                  }
                />
              </div>
            </div>
          ) : null}
          {target && ['reject', 'revoke'].includes(target.action) ? (
            <div className='grid gap-1.5'>
              <Label htmlFor='ops-reason'>
                {target.action === 'reject'
                  ? 'Supplier-visible reason'
                  : 'Revocation reason'}
              </Label>
              <Textarea
                id='ops-reason'
                value={form.message}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    message: event.target.value,
                  }))
                }
              />
            </div>
          ) : null}
          {target?.action === 'payout' ? (
            <div className='grid gap-3'>
              <div className='grid gap-1.5'>
                <Label htmlFor='payout-value'>Payout value (USD)</Label>
                <Input
                  id='payout-value'
                  type='number'
                  min='0.01'
                  step='0.01'
                  max={(
                    target.contribution.available_payout_quota / 500_000
                  ).toString()}
                  value={form.payoutUsd}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      payoutUsd: event.target.value,
                    }))
                  }
                />
              </div>
              <div className='grid gap-1.5'>
                <Label htmlFor='payout-note'>Reconciliation note</Label>
                <Textarea
                  id='payout-note'
                  value={form.message}
                  onChange={(event) =>
                    setForm((current) => ({
                      ...current,
                      message: event.target.value,
                    }))
                  }
                />
              </div>
            </div>
          ) : null}
          {target?.action === 'paid' ? (
            <div className='grid gap-1.5'>
              <Label htmlFor='payout-reference'>
                External payment reference
              </Label>
              <Input
                id='payout-reference'
                value={form.externalReference}
                onChange={(event) =>
                  setForm((current) => ({
                    ...current,
                    externalReference: event.target.value,
                  }))
                }
              />
            </div>
          ) : null}
          {error ? (
            <div className='text-destructive text-xs'>{error}</div>
          ) : null}
          <DialogFooter>
            <Button variant='outline' onClick={() => setTarget(null)}>
              Cancel
            </Button>
            <Button
              variant={
                target?.action === 'reject' || target?.action === 'revoke'
                  ? 'destructive'
                  : 'default'
              }
              disabled={mutation.isPending}
              onClick={() => mutation.mutate()}
            >
              Confirm {target?.action}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
