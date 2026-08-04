/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'

import {
  getTenantAudits,
  getTenantPayments,
  getTenantUsage,
  type TenantOverview,
} from './api'
import { compact, money, when } from './format'

const TABS = ['Usage', 'Payments', 'Audits'] as const
type Tab = (typeof TABS)[number]

// Log type ints from model/log.go. Shown as words because an operator reading an
// audit trail should not have to remember that 3 means "manage".
const LOG_TYPE_LABEL: Record<number, string> = {
  1: 'Top-up',
  2: 'Consume',
  3: 'Admin action',
  4: 'System',
  5: 'Error',
  6: 'Refund',
  7: 'Login',
}

function Empty(props: { children: string }) {
  return <p className='text-muted-foreground text-sm'>{props.children}</p>
}

function UsageTab(props: { tenantId: number; startAt: number }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['ops-tenant-usage', props.tenantId, props.startAt],
    queryFn: () =>
      getTenantUsage(props.tenantId, { startAt: props.startAt || undefined }),
  })

  if (isLoading) return <Empty>Loading…</Empty>
  if (error) return <p className='text-destructive text-sm'>{error.message}</p>
  if (!data) return null

  return (
    <div className='grid gap-6 md:grid-cols-2'>
      <div>
        <h4 className='text-foreground mb-2 text-sm font-medium'>
          Spend by model
        </h4>
        {data.models.length === 0 ? (
          <Empty>No usage in this window.</Empty>
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
                  <td className='py-1.5 text-right'>{compact(m.requests)}</td>
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
  )
}

function PaymentsTab(props: { tenantId: number }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['ops-tenant-payments', props.tenantId],
    queryFn: () => getTenantPayments(props.tenantId),
  })

  if (isLoading) return <Empty>Loading…</Empty>
  if (error) return <p className='text-destructive text-sm'>{error.message}</p>
  if (!data || data.length === 0) {
    return <Empty>No payments recorded for this tenant.</Empty>
  }

  return (
    <table className='w-full text-sm'>
      <thead>
        <tr className='text-muted-foreground text-left text-xs'>
          <th className='py-1 font-normal'>When</th>
          <th className='py-1 font-normal'>By</th>
          <th className='py-1 font-normal'>Method</th>
          <th className='py-1 font-normal'>Trade no.</th>
          <th className='py-1 text-right font-normal'>Paid</th>
          <th className='py-1 text-right font-normal'>Status</th>
        </tr>
      </thead>
      <tbody>
        {data.map((p) => (
          <tr key={p.id} className='border-border/60 border-t'>
            <td className='text-muted-foreground py-1.5 text-xs'>
              {when(p.create_time)}
            </td>
            <td className='py-1.5'>{p.username}</td>
            <td className='text-muted-foreground py-1.5 text-xs'>
              {p.payment_provider || p.payment_method || '—'}
            </td>
            <td className='text-muted-foreground py-1.5 font-mono text-[11px]'>
              {p.trade_no || '—'}
            </td>
            {/* money is the amount actually charged; amount is quota granted. */}
            <td className='py-1.5 text-right'>
              {p.money ? `$${p.money.toFixed(2)}` : money(p.amount)}
            </td>
            <td className='text-muted-foreground py-1.5 text-right text-xs'>
              {p.status || '—'}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function AuditsTab(props: { tenantId: number }) {
  const { data, isLoading, error } = useQuery({
    queryKey: ['ops-tenant-audits', props.tenantId],
    queryFn: () => getTenantAudits(props.tenantId),
  })

  if (isLoading) return <Empty>Loading…</Empty>
  if (error) return <p className='text-destructive text-sm'>{error.message}</p>
  if (!data || data.length === 0) {
    return <Empty>Nothing recorded for this tenant yet.</Empty>
  }

  return (
    <table className='w-full text-sm'>
      <thead>
        <tr className='text-muted-foreground text-left text-xs'>
          <th className='py-1 font-normal'>When</th>
          <th className='py-1 font-normal'>Type</th>
          <th className='py-1 font-normal'>User</th>
          <th className='py-1 font-normal'>Detail</th>
          <th className='py-1 text-right font-normal'>IP</th>
        </tr>
      </thead>
      <tbody>
        {data.map((e) => (
          <tr key={e.id} className='border-border/60 border-t align-top'>
            <td className='text-muted-foreground py-1.5 text-xs whitespace-nowrap'>
              {when(e.created_at)}
            </td>
            <td className='py-1.5 text-xs whitespace-nowrap'>
              {LOG_TYPE_LABEL[e.type] ?? `Type ${e.type}`}
            </td>
            <td className='py-1.5'>{e.username || '—'}</td>
            <td className='text-muted-foreground py-1.5'>{e.content || '—'}</td>
            <td className='text-muted-foreground py-1.5 text-right font-mono text-[11px]'>
              {e.ip || '—'}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

export function TenantDetail(props: {
  tenant: TenantOverview
  startAt: number
}) {
  const [tab, setTab] = useState<Tab>('Usage')

  return (
    <div className='bg-muted/40 border-border border-t p-4'>
      <div className='border-border mb-4 flex gap-1 border-b'>
        {TABS.map((t) => (
          <button
            key={t}
            type='button'
            onClick={() => setTab(t)}
            className={
              t === tab
                ? 'border-primary text-foreground -mb-px border-b-2 px-3 py-1.5 text-xs'
                : 'text-muted-foreground hover:text-foreground -mb-px border-b-2 border-transparent px-3 py-1.5 text-xs'
            }
          >
            {t}
          </button>
        ))}
      </div>

      {tab === 'Usage' ? (
        <UsageTab tenantId={props.tenant.tenant_id} startAt={props.startAt} />
      ) : null}
      {tab === 'Payments' ? (
        <PaymentsTab tenantId={props.tenant.tenant_id} />
      ) : null}
      {tab === 'Audits' ? (
        <AuditsTab tenantId={props.tenant.tenant_id} />
      ) : null}
    </div>
  )
}
