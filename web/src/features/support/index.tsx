/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  addMyMessage,
  closeMyTicket,
  createTicket,
  getInboxTicket,
  getMyTicket,
  listInbox,
  listMyTickets,
  replyInbox,
  setInboxStatus,
  type SupportConversation,
  type SupportStatus,
  type SupportTicket,
} from './api'
import './i18n'

// Customer questions and messages. One sidebar entry, role-routed like Credit
// Supply: a customer sees their own conversations; staff see the inbox. The
// selected conversation lives in ?ticket= so the link in the notification
// email opens it directly.

const SUBJECT_MAX = 200
const BODY_MAX = 5000

function when(ts: number) {
  return new Date(ts * 1000).toLocaleString()
}

const STATUS_LABEL: Record<SupportStatus, string> = {
  open: 'Waiting for us',
  answered: 'Replied',
  closed: 'Closed',
}

const STAFF_STATUS_LABEL: Record<SupportStatus, string> = {
  open: 'Needs reply',
  answered: 'Waiting on customer',
  closed: 'Closed',
}

function StatusPill(props: { status: SupportStatus; staff?: boolean }) {
  const tone =
    props.status === 'open'
      ? 'bg-warning/10 text-warning'
      : props.status === 'answered'
        ? 'bg-primary/10 text-primary'
        : 'bg-muted text-muted-foreground'
  const { t } = useTranslation()
  const label = t(
    (props.staff ? STAFF_STATUS_LABEL : STATUS_LABEL)[props.status]
  )
  return (
    <span
      className={`${tone} rounded px-1.5 py-0.5 text-[11px] whitespace-nowrap`}
    >
      {label}
    </span>
  )
}

function useSelectedTicket(): [number | undefined, (id?: number) => void] {
  const search = useSearch({ strict: false }) as { ticket?: number }
  const navigate = useNavigate()
  const select = (id?: number) =>
    void navigate({ to: '/support', search: id ? { ticket: id } : {} })
  return [search.ticket, select]
}

function Thread(props: {
  conversation: SupportConversation
  staffView: boolean
}) {
  const { t } = useTranslation()
  return (
    <ol className='flex flex-col gap-3'>
      {props.conversation.messages.map((m) => {
        const mine = props.staffView ? m.from_staff : !m.from_staff
        const who = m.from_staff
          ? t('Unify API support')
          : props.staffView
            ? (props.conversation.customer?.username ?? t('Customer'))
            : t('You')
        return (
          <li
            key={m.id}
            className={`max-w-[85%] rounded-lg border p-3 ${
              mine
                ? 'border-primary/30 bg-primary/5 self-end'
                : 'border-border bg-card self-start'
            }`}
          >
            <div className='text-muted-foreground mb-1 text-xs'>
              {who} · {when(m.created_at)}
            </div>
            <div className='text-foreground text-sm break-words whitespace-pre-wrap'>
              {m.body}
            </div>
          </li>
        )
      })}
    </ol>
  )
}

function Composer(props: {
  placeholder: string
  submitLabel: string
  pending: boolean
  onSubmit: (body: string) => Promise<unknown>
}) {
  const { t } = useTranslation()
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const submit = async () => {
    setError(null)
    try {
      await props.onSubmit(body)
      setBody('')
    } catch (e) {
      setError((e as Error).message)
    }
  }
  return (
    <div className='flex flex-col gap-2'>
      <Textarea
        value={body}
        maxLength={BODY_MAX}
        rows={4}
        placeholder={props.placeholder}
        onChange={(e) => setBody(e.target.value)}
      />
      <div className='flex items-center justify-between gap-2'>
        <span className='text-destructive text-xs'>{error}</span>
        <Button
          disabled={props.pending || body.trim() === ''}
          onClick={() => void submit()}
        >
          {props.pending ? t('Sending…') : props.submitLabel}
        </Button>
      </div>
    </div>
  )
}

function NewQuestion(props: { onCreated: (t: SupportTicket) => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const [subject, setSubject] = useState('')
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const create = useMutation({
    mutationFn: () => createTicket(subject, body),
    onSuccess: (ticket) => {
      setSubject('')
      setBody('')
      void qc.invalidateQueries({ queryKey: ['support', 'mine'] })
      props.onCreated(ticket)
    },
    onError: (e) => setError((e as Error).message),
  })
  return (
    <div className='border-border bg-card flex flex-col gap-3 rounded-lg border p-4'>
      <h2 className='text-foreground text-base'>{t('Ask a question')}</h2>
      <Input
        value={subject}
        maxLength={SUBJECT_MAX}
        placeholder={t('Subject, e.g. "How do I raise my rate limit?"')}
        onChange={(e) => setSubject(e.target.value)}
      />
      <Textarea
        value={body}
        maxLength={BODY_MAX}
        rows={5}
        placeholder={t(
          'Tell us what you need. Include the model, the request id or the error text if there is one.'
        )}
        onChange={(e) => setBody(e.target.value)}
      />
      <div className='flex items-center justify-between gap-2'>
        <span className='text-destructive text-xs'>{error}</span>
        <Button
          disabled={
            create.isPending || subject.trim() === '' || body.trim() === ''
          }
          onClick={() => {
            setError(null)
            create.mutate()
          }}
        >
          {create.isPending ? t('Sending…') : t('Send')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t('We reply here and email you when we do.')}
      </p>
    </div>
  )
}

function CustomerConversation(props: { id: number; onBack: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const key = ['support', 'mine', props.id]
  const q = useQuery({ queryKey: key, queryFn: () => getMyTicket(props.id) })
  const settle = (data: SupportConversation) => {
    qc.setQueryData(key, data)
    void qc.invalidateQueries({ queryKey: ['support', 'mine'], exact: true })
  }
  const send = useMutation({
    mutationFn: (body: string) => addMyMessage(props.id, body),
    onSuccess: settle,
  })
  const close = useMutation({
    mutationFn: () => closeMyTicket(props.id),
    onSuccess: settle,
  })

  if (q.isLoading)
    return <p className='text-muted-foreground'>{t('Loading…')}</p>
  if (q.error || !q.data)
    return (
      <p className='text-destructive'>
        {(q.error as Error)?.message ?? t('Not found')}
      </p>
    )
  const ticket = q.data.ticket
  return (
    <div className='flex flex-col gap-4'>
      <button
        className='text-muted-foreground self-start text-sm hover:underline'
        onClick={props.onBack}
      >
        {t('← All conversations')}
      </button>
      <div className='flex flex-wrap items-center gap-2'>
        <h2 className='text-foreground text-lg'>{ticket.subject}</h2>
        <StatusPill status={ticket.status} />
      </div>
      <Thread conversation={q.data} staffView={false} />
      <Composer
        placeholder={
          ticket.status === 'closed'
            ? t('Write to reopen this conversation')
            : t('Add a message')
        }
        submitLabel={t('Send')}
        pending={send.isPending}
        onSubmit={(body) => send.mutateAsync(body)}
      />
      {ticket.status !== 'closed' ? (
        <Button
          variant='outline'
          className='self-start'
          disabled={close.isPending}
          onClick={() => close.mutate()}
        >
          {t('Mark as resolved')}
        </Button>
      ) : null}
    </div>
  )
}

function CustomerSupport() {
  const { t } = useTranslation()
  const [selected, select] = useSelectedTicket()
  const q = useQuery({ queryKey: ['support', 'mine'], queryFn: listMyTickets })

  if (selected) {
    return (
      <CustomerConversation id={selected} onBack={() => select(undefined)} />
    )
  }
  return (
    <div className='grid gap-6 lg:grid-cols-[1fr_1fr]'>
      <NewQuestion onCreated={(ticket) => select(ticket.id)} />
      <div className='flex flex-col gap-2'>
        <h2 className='text-foreground text-base'>{t('Your conversations')}</h2>
        {q.isLoading ? (
          <p className='text-muted-foreground text-sm'>{t('Loading…')}</p>
        ) : (q.data ?? []).length === 0 ? (
          <p className='text-muted-foreground text-sm'>
            {t('No questions yet. Ask one and we will get back to you.')}
          </p>
        ) : (
          <ul className='border-border divide-border divide-y rounded-lg border'>
            {(q.data ?? []).map((row) => (
              <li key={row.id}>
                <button
                  className='hover:bg-muted/50 flex w-full items-center justify-between gap-3 px-4 py-3 text-left'
                  onClick={() => select(row.id)}
                >
                  <span className='text-foreground truncate text-sm'>
                    {row.subject}
                  </span>
                  <span className='flex shrink-0 items-center gap-2'>
                    <span className='text-muted-foreground text-xs'>
                      {when(row.updated_at)}
                    </span>
                    <StatusPill status={row.status} />
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

const INBOX_FILTERS: { label: string; value: SupportStatus | '' }[] = [
  { label: 'Needs reply', value: 'open' },
  { label: 'Waiting on customer', value: 'answered' },
  { label: 'Closed', value: 'closed' },
  { label: 'All', value: '' },
]

function InboxConversation(props: { id: number; onBack: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const key = ['support', 'inbox', 'ticket', props.id]
  const q = useQuery({ queryKey: key, queryFn: () => getInboxTicket(props.id) })
  const settle = (data: SupportConversation) => {
    qc.setQueryData(key, data)
    void qc.invalidateQueries({ queryKey: ['support', 'inbox', 'list'] })
  }
  const reply = useMutation({
    mutationFn: (body: string) => replyInbox(props.id, body),
    onSuccess: settle,
  })
  const status = useMutation({
    mutationFn: (s: SupportStatus) => setInboxStatus(props.id, s),
    onSuccess: settle,
  })

  if (q.isLoading)
    return <p className='text-muted-foreground'>{t('Loading…')}</p>
  if (q.error || !q.data)
    return (
      <p className='text-destructive'>
        {(q.error as Error)?.message ?? t('Not found')}
      </p>
    )
  const ticket = q.data.ticket
  return (
    <div className='flex flex-col gap-4'>
      <button
        className='text-muted-foreground self-start text-sm hover:underline'
        onClick={props.onBack}
      >
        {t('← Inbox')}
      </button>
      <div className='flex flex-wrap items-center gap-2'>
        <h2 className='text-foreground text-lg'>
          #{ticket.id} {ticket.subject}
        </h2>
        <StatusPill status={ticket.status} staff />
      </div>
      {q.data.customer ? (
        <p className='text-muted-foreground text-sm'>
          {[
            q.data.customer.username,
            q.data.customer.email,
            t('user #{{id}}', { id: q.data.customer.id }),
          ]
            .filter(Boolean)
            .join(' · ')}
        </p>
      ) : null}
      <Thread conversation={q.data} staffView />
      <Composer
        placeholder={t('Reply to the customer — they get it by email too')}
        submitLabel={t('Send reply')}
        pending={reply.isPending}
        onSubmit={(body) => reply.mutateAsync(body)}
      />
      <div className='flex gap-2'>
        {ticket.status !== 'closed' ? (
          <Button
            variant='outline'
            disabled={status.isPending}
            onClick={() => status.mutate('closed')}
          >
            {t('Close')}
          </Button>
        ) : (
          <Button
            variant='outline'
            disabled={status.isPending}
            onClick={() => status.mutate('open')}
          >
            {t('Reopen')}
          </Button>
        )}
      </div>
    </div>
  )
}

function StaffInbox() {
  const { t } = useTranslation()
  const [selected, select] = useSelectedTicket()
  const [filter, setFilter] = useState<SupportStatus | ''>('open')
  const q = useQuery({
    queryKey: ['support', 'inbox', 'list', filter],
    queryFn: () => listInbox(filter),
    refetchInterval: 60_000,
  })

  if (selected) {
    return <InboxConversation id={selected} onBack={() => select(undefined)} />
  }
  return (
    <div className='flex flex-col gap-4'>
      <div className='border-border flex self-start overflow-hidden rounded-md border'>
        {INBOX_FILTERS.map((f) => (
          <button
            key={f.label}
            className={`px-3 py-1.5 text-sm ${
              filter === f.value
                ? 'bg-primary text-primary-foreground'
                : 'text-muted-foreground hover:bg-muted/50'
            }`}
            onClick={() => setFilter(f.value)}
          >
            {t(f.label)}
          </button>
        ))}
      </div>
      {q.isLoading ? (
        <p className='text-muted-foreground text-sm'>{t('Loading…')}</p>
      ) : q.error ? (
        <p className='text-destructive text-sm'>{(q.error as Error).message}</p>
      ) : (q.data ?? []).length === 0 ? (
        <p className='text-muted-foreground text-sm'>{t('Nothing here.')}</p>
      ) : (
        <table className='border-border w-full overflow-hidden rounded-lg border text-sm'>
          <thead className='bg-muted/40 text-muted-foreground text-left text-xs'>
            <tr>
              <th className='px-4 py-2'>#</th>
              <th className='px-4 py-2'>{t('Subject')}</th>
              <th className='px-4 py-2'>{t('Customer')}</th>
              <th className='px-4 py-2'>{t('Last activity')}</th>
              <th className='px-4 py-2'>{t('Status')}</th>
            </tr>
          </thead>
          <tbody>
            {(q.data ?? []).map((row) => (
              <tr
                key={row.id}
                className='border-border/60 hover:bg-muted/40 cursor-pointer border-t'
                onClick={() => select(row.id)}
              >
                <td className='text-muted-foreground px-4 py-2'>{row.id}</td>
                <td className='text-foreground px-4 py-2'>{row.subject}</td>
                <td className='px-4 py-2'>
                  <div className='text-foreground'>{row.username}</div>
                  <div className='text-muted-foreground text-xs'>
                    {row.email}
                  </div>
                </td>
                <td className='text-muted-foreground px-4 py-2'>
                  {when(row.updated_at)}
                </td>
                <td className='px-4 py-2'>
                  <StatusPill status={row.status} staff />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

export function SupportPage() {
  const { t } = useTranslation()
  const role = useAuthStore((s) => s.auth.user?.role ?? 0)
  const staff = role >= ROLE.ADMIN
  return (
    <div className='mx-auto w-full max-w-6xl overflow-y-auto p-6'>
      <div className='mb-6'>
        <h1 className='text-foreground text-2xl'>
          {staff ? t('Support inbox') : t('Support')}
        </h1>
        <p className='text-muted-foreground mt-1 text-sm'>
          {staff
            ? t(
                'Customer questions. Every new customer message is also emailed to staff.'
              )
            : t('Questions, problems or feedback — write to us here.')}
        </p>
      </div>
      {staff ? <StaffInbox /> : <CustomerSupport />}
    </div>
  )
}
