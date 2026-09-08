/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Fragment, useState } from 'react'

import {
  addCreditPoolLot,
  addTenantCreditGrant,
  createCreditPool,
  getCreditPool,
  getCreditPools,
  type CreditPoolSummary,
} from './api'
import { day, money, QUOTA_PER_UNIT } from './format'

function ask(label: string, initial = '') {
  const value = window.prompt(label, initial)
  return value === null ? null : value.trim()
}

function askPositiveNumber(label: string, initial = '') {
  const raw = ask(label, initial)
  if (raw === null) return null
  const value = Number(raw)
  if (!Number.isFinite(value) || value <= 0) {
    throw new Error(`${label} must be a positive number`)
  }
  return value
}

function askUnixExpiry() {
  const raw = ask('Expiry date (YYYY-MM-DD), or blank for no expiry', '')
  if (raw === null) return null
  if (!raw) return 0
  const value = Math.floor(new Date(`${raw}T23:59:59`).getTime() / 1000)
  if (!Number.isFinite(value)) throw new Error('Expiry date is invalid')
  return value
}

function PoolDetails({ poolId }: { poolId: number }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['ops-credit-pool', poolId],
    queryFn: () => getCreditPool(poolId),
  })
  if (isLoading) {
    return <div className='text-muted-foreground p-4 text-xs'>Loading…</div>
  }
  if (error) {
    return <div className='text-destructive p-4 text-xs'>{error.message}</div>
  }
  return (
    <div className='grid gap-4 p-4 lg:grid-cols-2'>
      <div>
        <h4 className='text-foreground mb-2 text-xs font-medium'>
          Inventory lots
        </h4>
        <div className='space-y-2'>
          {data?.lots.length ? (
            data.lots.map((lot) => (
              <div key={lot.id} className='bg-muted/40 rounded-md p-2 text-xs'>
                <div className='flex justify-between gap-3'>
                  <span className='text-foreground'>
                    {lot.label || `Lot #${lot.id}`}
                  </span>
                  <span className='font-mono'>
                    {money(lot.remaining_quota)}
                  </span>
                </div>
                <div className='text-muted-foreground mt-1'>
                  {lot.source_type} · channel {lot.channel_id || 'any'} · buy
                  rate {(lot.acquisition_ratio * 100).toFixed(0)}% · expires{' '}
                  {day(lot.expires_at)}
                </div>
                {lot.source_type === 'contributed' ? (
                  <div className='text-muted-foreground mt-1'>
                    contributor tenant #{lot.contributor_tenant_id} · payable{' '}
                    {money(lot.accrued_payable_quota)}
                  </div>
                ) : null}
              </div>
            ))
          ) : (
            <div className='text-muted-foreground text-xs'>
              No inventory yet.
            </div>
          )}
        </div>
      </div>
      <div>
        <h4 className='text-foreground mb-2 text-xs font-medium'>
          Tenant grants
        </h4>
        <div className='space-y-2'>
          {data?.grants.length ? (
            data.grants.map((grant) => (
              <div
                key={grant.id}
                className='bg-muted/40 rounded-md p-2 text-xs'
              >
                <div className='flex justify-between gap-3'>
                  <span className='text-foreground'>
                    {grant.name || `Grant #${grant.id}`}
                  </span>
                  <span className='font-mono'>
                    {money(grant.remaining_quota)}
                  </span>
                </div>
                <div className='text-muted-foreground mt-1'>
                  tenant #{grant.tenant_id} · priority {grant.priority} ·
                  expires {day(grant.expires_at)}
                </div>
              </div>
            ))
          ) : (
            <div className='text-muted-foreground text-xs'>No grants yet.</div>
          )}
        </div>
      </div>
    </div>
  )
}

function PoolActions({ pool }: { pool: CreditPoolSummary }) {
  const qc = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  const mutation = useMutation({
    mutationFn: async (kind: 'lot' | 'grant') => {
      if (kind === 'lot') {
        const source = ask('Source: free, purchased, or contributed', 'free')
        if (source === null) return
        if (!['free', 'purchased', 'contributed'].includes(source)) {
          throw new Error('Source must be free, purchased, or contributed')
        }
        const amount = askPositiveNumber('Face value in USD')
        if (amount === null) return
        const channelId = Number(ask('Channel ID (0 = any pool channel)', '0'))
        const contributorTenantId =
          source === 'contributed'
            ? askPositiveNumber('Contributor tenant ID')
            : 0
        if (contributorTenantId === null) return
        const ratio =
          source === 'free'
            ? 0
            : askPositiveNumber('Acquisition ratio (for example 0.2)', '0.2')
        if (ratio === null) return
        const expiresAt = askUnixExpiry()
        if (expiresAt === null) return
        await addCreditPoolLot(pool.id, {
          channel_id: channelId,
          source_type: source as 'free' | 'purchased' | 'contributed',
          contributor_tenant_id: Number(contributorTenantId),
          label: ask('Lot label', '') ?? '',
          original_quota: Math.round(amount * QUOTA_PER_UNIT),
          acquisition_ratio: ratio,
          expires_at: expiresAt,
        })
        return
      }
      const tenantId = askPositiveNumber('Tenant ID')
      if (tenantId === null) return
      const amount = askPositiveNumber('Promotional grant in USD')
      if (amount === null) return
      const expiresAt = askUnixExpiry()
      if (expiresAt === null) return
      const priority = Number(ask('Priority (higher is consumed first)', '0'))
      await addTenantCreditGrant(pool.id, {
        tenant_id: tenantId,
        name: ask('Customer-facing grant name', 'Promotional credit') ?? '',
        original_quota: Math.round(amount * QUOTA_PER_UNIT),
        starts_at: 0,
        expires_at: expiresAt,
        priority: Number.isFinite(priority) ? priority : 0,
      })
    },
    onSuccess: () => {
      setError(null)
      void qc.invalidateQueries({ queryKey: ['ops-credit-pools'] })
      void qc.invalidateQueries({ queryKey: ['ops-credit-pool', pool.id] })
    },
    onError: (e: Error) => setError(e.message),
  })
  return (
    <div className='flex flex-col items-end gap-1'>
      <div className='flex gap-2'>
        <button
          type='button'
          className='text-primary text-xs hover:underline'
          onClick={() => mutation.mutate('lot')}
        >
          Add inventory
        </button>
        <button
          type='button'
          className='text-primary text-xs hover:underline'
          onClick={() => mutation.mutate('grant')}
        >
          Grant credits
        </button>
      </div>
      {error ? (
        <span className='text-destructive text-[10px]'>{error}</span>
      ) : null}
    </div>
  )
}

export function CreditPools() {
  const qc = useQueryClient()
  const [expanded, setExpanded] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['ops-credit-pools'],
    queryFn: getCreditPools,
  })
  const create = useMutation({
    mutationFn: async () => {
      const name = ask('Pool name', 'OpenAI promotional pool')
      if (!name) return
      const routingGroup = ask('Existing channel routing group', 'promo-openai')
      if (!routingGroup) return
      const models = ask('Eligible models (comma-separated globs)', 'gpt-*')
      if (models === null) return
      await createCreditPool({ name, routing_group: routingGroup, models })
    },
    onSuccess: () => {
      setError(null)
      void qc.invalidateQueries({ queryKey: ['ops-credit-pools'] })
    },
    onError: (e: Error) => setError(e.message),
  })

  let tableBody
  if (query.isLoading) {
    tableBody = (
      <tr>
        <td colSpan={6} className='text-muted-foreground p-6 text-center'>
          Loading pools…
        </td>
      </tr>
    )
  } else if (query.data?.length) {
    tableBody = query.data.map((pool) => {
      const coverage = pool.remaining_quota - pool.grant_remaining_quota
      return (
        <Fragment key={pool.id}>
          <tr className='border-border/60 border-t'>
            <td className='px-4 py-3'>
              <button
                type='button'
                className='text-foreground text-left hover:underline'
                onClick={() =>
                  setExpanded(expanded === pool.id ? null : pool.id)
                }
              >
                {pool.name}
              </button>
              <div className='text-muted-foreground font-mono text-[11px]'>
                {pool.routing_group} · {pool.models || '*'}
              </div>
            </td>
            <td className='px-4 py-3 text-right'>
              {money(pool.remaining_quota)}
            </td>
            <td className='px-4 py-3 text-right'>
              {money(pool.grant_remaining_quota)}
            </td>
            <td
              className={`px-4 py-3 text-right ${coverage < 0 ? 'text-destructive' : ''}`}
            >
              {money(coverage)}
            </td>
            <td className='px-4 py-3 text-right'>
              {money(pool.accrued_payable_quota)}
            </td>
            <td className='px-4 py-3'>
              <PoolActions pool={pool} />
            </td>
          </tr>
          {expanded === pool.id ? (
            <tr className='border-border/60 border-t'>
              <td colSpan={6}>
                <PoolDetails poolId={pool.id} />
              </td>
            </tr>
          ) : null}
        </Fragment>
      )
    })
  } else {
    tableBody = (
      <tr>
        <td colSpan={6} className='text-muted-foreground p-6 text-center'>
          No pools. Create one after its channel routing group exists.
        </td>
      </tr>
    )
  }

  return (
    <section className='border-border bg-card mb-6 overflow-hidden rounded-lg border'>
      <div className='flex flex-wrap items-center justify-between gap-3 p-4'>
        <div>
          <h2 className='text-foreground text-base'>
            Promotional credit pools
          </h2>
          <p className='text-muted-foreground mt-0.5 text-xs'>
            Inventory backs free tenant grants. Cash balances remain separate.
          </p>
        </div>
        <button
          type='button'
          disabled={create.isPending}
          onClick={() => create.mutate()}
          className='bg-primary text-primary-foreground rounded-md px-3 py-1.5 text-xs disabled:opacity-50'
        >
          Create pool
        </button>
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
              <th className='px-4 py-2.5 font-normal'>Pool / routing</th>
              <th className='px-4 py-2.5 text-right font-normal'>
                Inventory left
              </th>
              <th className='px-4 py-2.5 text-right font-normal'>
                Grants left
              </th>
              <th className='px-4 py-2.5 text-right font-normal'>Coverage</th>
              <th className='px-4 py-2.5 text-right font-normal'>
                Payable accrued
              </th>
              <th className='px-4 py-2.5 text-right font-normal'>Actions</th>
            </tr>
          </thead>
          <tbody>{tableBody}</tbody>
        </table>
      </div>
    </section>
  )
}
