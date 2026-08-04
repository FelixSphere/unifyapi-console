/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Fragment, useMemo, useState } from 'react'

import {
  extendTenantTerm,
  getTenantOverviews,
  resumeTenant,
  suspendTenant,
  type TenantOverview,
} from './api'
import { compact, day, daysUntil, money, when } from './format'
import { TenantDetail } from './tenant-detail'

// Global operations monitoring. Upstream has no equivalent -- its admin surfaces
// are per-user and global, so "which customers exist, what are they running, what
// have they spent" could not be answered without aggregating by hand.

const WINDOWS = [
  { label: '24h', seconds: 86_400 },
  { label: '7d', seconds: 7 * 86_400 },
  { label: '30d', seconds: 30 * 86_400 },
  { label: 'All time', seconds: 0 },
] as const

const TENANT_STATUS_DISABLED = 2

// Terms inside this many days deserve attention before they lapse.
const EXPIRY_WARN_DAYS = 14

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

function Badge(props: { tone: 'danger' | 'warn'; children: string }) {
  const tone =
    props.tone === 'danger'
      ? 'bg-destructive/10 text-destructive'
      : 'bg-warning/10 text-warning'
  return (
    <span
      className={`${tone} ml-2 rounded px-1.5 py-0.5 text-[10px] whitespace-nowrap`}
    >
      {props.children}
    </span>
  )
}

function TenantActions(props: { tenant: TenantOverview }) {
  const qc = useQueryClient()
  const [error, setError] = useState<string | null>(null)
  const t = props.tenant
  const suspended = t.status === TENANT_STATUS_DISABLED

  const run = useMutation({
    mutationFn: async (action: 'suspend' | 'resume' | 'extend30') => {
      if (action === 'suspend') {
        // Ask for a reason: the next operator looking at this account should not
        // have to guess why access was cut.
        const reason =
          window.prompt(`Suspend "${t.name}" — reason (recorded):`, '') ?? ''
        if (!window.confirm(`Cut off API access for "${t.name}" now?`)) return
        await suspendTenant(t.tenant_id, reason)
        return
      }
      if (action === 'resume') {
        await resumeTenant(t.tenant_id)
        return
      }
      await extendTenantTerm(t.tenant_id, 30)
    },
    onSuccess: () => {
      setError(null)
      // Suspend/resume changes member status and extend changes the term; both
      // are shown in the row that triggered them, and both leave an audit entry.
      void qc.invalidateQueries({ queryKey: ['ops-tenants'] })
      void qc.invalidateQueries({
        queryKey: ['ops-tenant-audits', t.tenant_id],
      })
    },
    onError: (e: Error) => setError(e.message),
  })

  return (
    <div className='flex flex-col items-end gap-1'>
      <div className='flex items-center gap-2'>
        <button
          type='button'
          disabled={run.isPending}
          onClick={() => run.mutate('extend30')}
          className='text-muted-foreground hover:text-foreground text-xs disabled:opacity-50'
          title='Extend the paid term by 30 days'
        >
          +30d
        </button>
        {suspended ? (
          <button
            type='button'
            disabled={run.isPending}
            onClick={() => run.mutate('resume')}
            className='text-primary text-xs hover:underline disabled:opacity-50'
          >
            Resume
          </button>
        ) : (
          <button
            type='button'
            disabled={run.isPending}
            onClick={() => run.mutate('suspend')}
            className='text-destructive text-xs hover:underline disabled:opacity-50'
          >
            Suspend
          </button>
        )}
      </div>
      {error ? (
        <span className='text-destructive max-w-[12rem] text-right text-[10px]'>
          {error}
        </span>
      ) : null}
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
      suspended: items.filter((t) => t.status === TENANT_STATUS_DISABLED)
        .length,
    }
  }, [data])

  return (
    <div className='mx-auto w-full max-w-7xl overflow-y-auto p-6'>
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
            onClick={() => void refetch()}
            className='border-border text-muted-foreground hover:bg-muted rounded-md border px-3 py-1.5 text-xs'
          >
            {isFetching ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>
      </div>

      <div className='mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-5'>
        <Stat
          label='Tenants'
          value={String(totals.tenants)}
          hint={totals.suspended ? `${totals.suspended} suspended` : undefined}
        />
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
        <div className='border-destructive/40 bg-destructive/5 text-destructive mb-4 rounded-lg border p-4 text-sm'>
          {error.message}
        </div>
      ) : null}

      <div className='border-border bg-card overflow-x-auto rounded-lg border'>
        <table className='w-full text-sm'>
          <thead className='bg-muted/50 text-muted-foreground text-left text-xs'>
            <tr>
              <th className='px-4 py-2.5 font-normal'>Tenant</th>
              <th className='px-4 py-2.5 text-right font-normal'>Members</th>
              <th className='px-4 py-2.5 text-right font-normal'>Requests</th>
              <th className='px-4 py-2.5 text-right font-normal'>Spend</th>
              <th className='px-4 py-2.5 text-right font-normal'>Balance</th>
              <th className='px-4 py-2.5 text-right font-normal'>Term ends</th>
              <th className='px-4 py-2.5 text-right font-normal'>
                Last active
              </th>
              <th className='px-4 py-2.5 text-right font-normal'>Actions</th>
              <th className='px-4 py-2.5' />
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td
                  colSpan={9}
                  className='text-muted-foreground px-4 py-8 text-center'
                >
                  Loading tenants…
                </td>
              </tr>
            ) : (data?.items?.length ?? 0) === 0 ? (
              <tr>
                <td
                  colSpan={9}
                  className='text-muted-foreground px-4 py-8 text-center'
                >
                  No tenants yet. Every customer who registers becomes one.
                </td>
              </tr>
            ) : (
              data!.items.map((t) => {
                const suspended = t.status === TENANT_STATUS_DISABLED
                const left = daysUntil(t.expires_at)
                return (
                  <Fragment key={t.tenant_id}>
                    <tr
                      className={
                        suspended
                          ? 'border-border/60 bg-destructive/5 border-t'
                          : 'border-border/60 border-t'
                      }
                    >
                      <td className='px-4 py-3'>
                        <div className='text-foreground flex items-center'>
                          {t.name}
                          {suspended ? (
                            <Badge tone='danger'>suspended</Badge>
                          ) : null}
                          {!suspended && left < 0 ? (
                            <Badge tone='danger'>expired</Badge>
                          ) : null}
                          {!suspended &&
                          left >= 0 &&
                          left <= EXPIRY_WARN_DAYS ? (
                            <Badge tone='warn'>{`${left}d left`}</Badge>
                          ) : null}
                        </div>
                        <div className='text-muted-foreground font-mono text-[11px]'>
                          {t.slug}
                          {t.group && t.group !== 'default'
                            ? ` · ${t.group}`
                            : ''}
                        </div>
                        {suspended && t.suspend_reason ? (
                          <div className='text-destructive/80 mt-0.5 text-[11px]'>
                            {t.suspend_reason}
                          </div>
                        ) : null}
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
                        {day(t.expires_at)}
                      </td>
                      <td className='text-muted-foreground px-4 py-3 text-right text-xs'>
                        {when(t.last_activity_at)}
                      </td>
                      <td className='px-4 py-3 text-right'>
                        <TenantActions tenant={t} />
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
                        <td colSpan={9} className='p-0'>
                          <TenantDetail tenant={t} startAt={startAt} />
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
