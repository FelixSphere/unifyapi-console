/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { Fragment, useMemo, useState } from 'react'

import { useQuery } from '@tanstack/react-query'

import { getTenantOverviews, getTenantUsage } from './api'

// Global operations monitoring: every tenant, what they are running, and what
// they have spent. Upstream has no equivalent -- its admin surfaces are
// per-user and global, so this question could not be answered without
// aggregating by hand.

const WINDOWS = [
  { label: '24h', seconds: 86_400 },
  { label: '7d', seconds: 7 * 86_400 },
  { label: '30d', seconds: 30 * 86_400 },
  { label: 'All time', seconds: 0 },
] as const

// Quota units are integers; upstream renders them as dollars at 500k units per
// unit of currency (common.QuotaPerUnit). Shown as a raw count alongside so an
// operator can reconcile against the ledger.
const QUOTA_PER_UNIT = 500_000

function money(quota: number) {
  return `$${(quota / QUOTA_PER_UNIT).toFixed(4)}`
}

function compact(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`
  return String(n)
}

function when(unix: number) {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleString()
}

function Stat(props: { label: string; value: string; hint?: string }) {
  return (
    <div className='border-border bg-card rounded-lg border p-4'>
      <div className='text-muted-foreground text-xs tracking-wide uppercase'>
        {props.label}
      </div>
      <div className='text-foreground mt-1 text-2xl'>{props.value}</div>
      {props.hint ? (
        <div className='text-muted-foreground mt-1 text-xs'>{props.hint}</div>
      ) : null}
    </div>
  )
}

function TenantDetail(props: { tenantId: number; startAt: number }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['ops-tenant-usage', props.tenantId, props.startAt],
    queryFn: () =>
      getTenantUsage(props.tenantId, { startAt: props.startAt || undefined }),
  })

  if (isLoading) {
    return <div className='text-muted-foreground p-4 text-sm'>Loading…</div>
  }
  if (error) {
    return (
      <div className='text-destructive p-4 text-sm'>{String(error.message)}</div>
    )
  }
  if (!data) return null

  return (
    <div className='bg-muted/40 border-border border-t p-4'>
      <div className='grid gap-6 md:grid-cols-2'>
        <div>
          <h4 className='text-foreground mb-2 text-sm font-medium'>
            Spend by model
          </h4>
          {data.models.length === 0 ? (
            <p className='text-muted-foreground text-sm'>
              No usage in this window.
            </p>
          ) : (
            <table className='w-full text-sm'>
              <thead>
                <tr className='text-muted-foreground text-left text-xs'>
                  <th className='py-1 font-normal'>Model</th>
                  <th className='py-1 text-right font-normal'>Requests</th>
                  <th className='py-1 text-right font-normal'>Spend</th>
                </tr>
              </thead>
              <tbody>
                {data.models.map((m) => (
                  <tr key={m.model_name} className='border-border/60 border-t'>
                    <td className='py-1.5 font-mono text-[12px]'>
                      {m.model_name || '—'}
                    </td>
                    <td className='py-1.5 text-right'>
                      {compact(m.requests)}
                    </td>
                    <td className='py-1.5 text-right'>{money(m.quota)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

        <div>
          <h4 className='text-foreground mb-2 text-sm font-medium'>
            Members ({data.members.length})
          </h4>
          <table className='w-full text-sm'>
            <thead>
              <tr className='text-muted-foreground text-left text-xs'>
                <th className='py-1 font-normal'>User</th>
                <th className='py-1 font-normal'>Email</th>
                <th className='py-1 text-right font-normal'>Last login</th>
              </tr>
            </thead>
            <tbody>
              {data.members.map((m) => (
                <tr key={m.id} className='border-border/60 border-t'>
                  <td className='py-1.5'>{m.display_name || m.username}</td>
                  <td className='text-muted-foreground py-1.5'>
                    {m.email || '—'}
                  </td>
                  <td className='text-muted-foreground py-1.5 text-right text-xs'>
                    {when(m.last_login_at)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

export function OpsDashboard() {
  const [windowIndex, setWindowIndex] = useState(1)
  const [expanded, setExpanded] = useState<number | null>(null)

  const startAt = useMemo(() => {
    const seconds = WINDOWS[windowIndex].seconds
    if (!seconds) return 0
    return Math.floor(Date.now() / 1000) - seconds
  }, [windowIndex])

  const { data, isLoading, error, refetch, isFetching } = useQuery({
    queryKey: ['ops-tenants', startAt],
    queryFn: () =>
      getTenantOverviews({ startAt: startAt || undefined, pageSize: 200 }),
  })

  const totals = useMemo(() => {
    const items = data?.items ?? []
    return {
      tenants: data?.total ?? 0,
      balance: items.reduce((a, t) => a + t.quota, 0),
      spend: items.reduce((a, t) => a + t.period_quota, 0),
      requests: items.reduce((a, t) => a + t.period_requests, 0),
      active: items.filter((t) => t.period_requests > 0).length,
    }
  }, [data])

  return (
    <div className='mx-auto w-full max-w-7xl p-6'>
      <div className='mb-6 flex flex-wrap items-center justify-between gap-4'>
        <div>
          <h1 className='text-foreground text-2xl'>Operations</h1>
          <p className='text-muted-foreground mt-1 text-sm'>
            Every tenant, what they are running, and what they have spent.
          </p>
        </div>

        <div className='flex items-center gap-2'>
          <div className='border-border flex overflow-hidden rounded-md border'>
            {WINDOWS.map((w, i) => (
              <button
                key={w.label}
                type='button'
                onClick={() => setWindowIndex(i)}
                className={
                  i === windowIndex
                    ? 'bg-primary text-primary-foreground px-3 py-1.5 text-xs'
                    : 'text-muted-foreground hover:bg-muted px-3 py-1.5 text-xs'
                }
              >
                {w.label}
              </button>
            ))}
          </div>
          <button
            type='button'
            onClick={() => refetch()}
            className='border-border text-muted-foreground hover:bg-muted rounded-md border px-3 py-1.5 text-xs'
          >
            {isFetching ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
      </div>

      <div className='mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-5'>
        <Stat label='Tenants' value={String(totals.tenants)} />
        <Stat
          label='Active'
          value={String(totals.active)}
          hint='with usage in window'
        />
        <Stat
          label='Spend'
          value={money(totals.spend)}
          hint={`${compact(totals.spend)} quota`}
        />
        <Stat label='Requests' value={compact(totals.requests)} />
        <Stat
          label='Balance held'
          value={money(totals.balance)}
          hint='unspent, across tenants'
        />
      </div>

      {error ? (
        <div className='border-destructive/40 bg-destructive/5 text-destructive rounded-lg border p-4 text-sm'>
          {String(error.message)}
        </div>
      ) : null}

      <div className='border-border bg-card overflow-hidden rounded-lg border'>
        <table className='w-full text-sm'>
          <thead className='bg-muted/50 text-muted-foreground text-left text-xs'>
            <tr>
              <th className='px-4 py-2.5 font-normal'>Tenant</th>
              <th className='px-4 py-2.5 text-right font-normal'>Members</th>
              <th className='px-4 py-2.5 text-right font-normal'>Requests</th>
              <th className='px-4 py-2.5 text-right font-normal'>Spend</th>
              <th className='px-4 py-2.5 text-right font-normal'>Balance</th>
              <th className='px-4 py-2.5 text-right font-normal'>
                Last activity
              </th>
              <th className='px-4 py-2.5' />
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td
                  colSpan={7}
                  className='text-muted-foreground px-4 py-8 text-center'
                >
                  Loading tenants…
                </td>
              </tr>
            ) : (data?.items?.length ?? 0) === 0 ? (
              <tr>
                <td
                  colSpan={7}
                  className='text-muted-foreground px-4 py-8 text-center'
                >
                  No tenants yet. Every customer who registers becomes one.
                </td>
              </tr>
            ) : (
              data!.items.map((t) => (
                <Fragment key={t.tenant_id}>
                  <tr className='border-border/60 border-t'>
                    <td className='px-4 py-3'>
                      <div className='text-foreground'>{t.name}</div>
                      <div className='text-muted-foreground font-mono text-[11px]'>
                        {t.slug}
                        {t.group && t.group !== 'default'
                          ? ` · ${t.group}`
                          : ''}
                      </div>
                    </td>
                    <td className='px-4 py-3 text-right'>{t.member_count}</td>
                    <td className='px-4 py-3 text-right'>
                      {compact(t.period_requests)}
                    </td>
                    <td className='px-4 py-3 text-right'>
                      {money(t.period_quota)}
                    </td>
                    <td className='px-4 py-3 text-right'>{money(t.quota)}</td>
                    <td className='text-muted-foreground px-4 py-3 text-right text-xs'>
                      {when(t.last_activity_at)}
                    </td>
                    <td className='px-4 py-3 text-right'>
                      <button
                        type='button'
                        onClick={() =>
                          setExpanded(
                            expanded === t.tenant_id ? null : t.tenant_id
                          )
                        }
                        className='text-primary text-xs hover:underline'
                      >
                        {expanded === t.tenant_id ? 'Hide' : 'Details'}
                      </button>
                    </td>
                  </tr>
                  {expanded === t.tenant_id ? (
                    <tr>
                      <td colSpan={7} className='p-0'>
                        <TenantDetail
                          tenantId={t.tenant_id}
                          startAt={startAt}
                        />
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
