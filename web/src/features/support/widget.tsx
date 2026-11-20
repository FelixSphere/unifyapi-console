/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useRouterState } from '@tanstack/react-router'
import { MessageCircle, Send, UserRound, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { useAuthStore } from '@/stores/auth-store'

import {
  addMyMessage,
  closeMyTicket,
  createTicket,
  getAIStatus,
  getMyTicket,
  handoffMyTicket,
  listMyTickets,
  streamAI,
  type SupportConversation,
} from './api'
import './i18n'
import { SUPPORT_UNREAD_KEY, unreadBadge, useSupportUnread } from './use-unread'
import {
  canHandOff,
  deriveSubject,
  isHandoff,
  messageForAI,
  pickConversation,
  shouldPoll,
  initialOpen,
  showWidget,
  WIDGET_CLOSED_KEY,
  WIDGET_POLL_MS,
  widgetMode,
} from './widget-logic'

// The floating support chat on every signed-in page (operator request
// 2026-11-19). It is a second face of /support, not a second system: the same
// tickets, the same endpoints, the same AI first responder and the same
// read/unread state. Mounted once in authenticated-layout.tsx.

const BODY_MAX = 5000

const AI_TOOL_LABEL: Record<string, string> = {
  get_my_account: 'Checking your account…',
  get_request: 'Looking up that request…',
  get_model_price: 'Looking up the price…',
  escalate_to_human: 'Asking a teammate to join…',
}

function usePageVisible() {
  const [visible, setVisible] = useState(
    () => typeof document === 'undefined' || !document.hidden
  )
  useEffect(() => {
    const onChange = () => setVisible(!document.hidden)
    document.addEventListener('visibilitychange', onChange)
    return () => document.removeEventListener('visibilitychange', onChange)
  }, [])
  return visible
}

// Ask the AI assistant to answer the customer's newest message, once per
// message -- the Support page's useAIAnswer, plus the answer text, which is
// shown the moment the agent sends it rather than after it is stored.
function useWidgetAI(
  conversation: SupportConversation | undefined,
  enabled: boolean | undefined,
  onStored: () => Promise<unknown>
) {
  const asked = useRef<number | null>(null)
  const stored = useRef(onStored)
  stored.current = onStored
  const [progress, setProgress] = useState<string | null>(null)
  const [answer, setAnswer] = useState<string | null>(null)
  const [failed, setFailed] = useState(false)
  const [now, setNow] = useState(() => Date.now() / 1000)
  // Re-read the clock whenever the conversation changes, not on every render.
  useEffect(() => setNow(Date.now() / 1000), [conversation])
  const target = messageForAI(conversation, enabled, now)
  const targetId = target?.id
  const ticketId = target?.ticket_id
  useEffect(() => {
    if (!targetId || !ticketId || asked.current === targetId) return
    asked.current = targetId
    setFailed(false)
    setAnswer(null)
    setProgress('AI assistant is thinking…')
    void streamAI(ticketId, (e) => {
      if (e.event === 'tool') {
        setProgress(AI_TOOL_LABEL[e.name] ?? 'AI assistant is thinking…')
      }
      if (e.event === 'answer') {
        setProgress(null)
        setAnswer(e.text)
      }
    })
      .then((outcome) => {
        if (outcome === 'failed') setFailed(true)
      })
      .catch(() => setFailed(true))
      .finally(() => {
        setProgress(null)
        void stored.current().finally(() => setAnswer(null))
      })
  }, [targetId, ticketId])
  return { progress, answer, failed }
}

function when(ts: number) {
  return new Date(ts * 1000).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
  })
}

function Bubble(props: {
  who: string
  at?: number
  mine: boolean
  text: string
}) {
  return (
    <li
      className={`max-w-[85%] rounded-lg border px-3 py-2 ${
        props.mine
          ? 'border-primary/30 bg-primary/5 self-end'
          : 'border-border bg-card self-start'
      }`}
    >
      <div className='text-muted-foreground mb-0.5 text-[11px]'>
        {props.who}
        {props.at ? ` · ${when(props.at)}` : ''}
      </div>
      <div className='text-foreground text-sm break-words whitespace-pre-wrap'>
        {props.text}
      </div>
    </li>
  )
}

function author(m: { from_ai?: boolean; from_staff: boolean }) {
  if (m.from_ai) {
    return 'AI assistant'
  }
  return m.from_staff ? 'Unify API support' : 'You'
}

// What the AI assistant is doing for the newest message: its answer as soon
// as it arrives, else what it is looking up, else that a person will answer.
function AIStatus(props: {
  answer: string | null
  progress: string | null
  failed: boolean
}) {
  const { t } = useTranslation()
  if (props.answer) {
    return <Bubble who={t('AI assistant')} mine={false} text={props.answer} />
  }
  if (props.progress) {
    return (
      <li className='border-border bg-card text-muted-foreground self-start rounded-lg border px-3 py-2 text-sm'>
        <span className='animate-pulse'>{t(props.progress)}</span>
      </li>
    )
  }
  if (props.failed) {
    return <Notice>{t('A teammate will reply here soon.')}</Notice>
  }
  return null
}

function Notice(props: { children: React.ReactNode }) {
  return (
    <li className='text-muted-foreground self-center px-2 text-center text-xs'>
      {props.children}
    </li>
  )
}

function ChatPanel(props: { onClose: () => void }) {
  const { t } = useTranslation()
  const qc = useQueryClient()
  const visible = usePageVisible()
  // undefined: show the newest unclosed conversation; null: start a new one
  // (after the customer ended one); a number: that conversation.
  const [chosen, setChosen] = useState<number | null | undefined>(undefined)
  const [ended, setEnded] = useState(false)
  const [body, setBody] = useState('')
  const [error, setError] = useState<string | null>(null)
  const listRef = useRef<HTMLOListElement>(null)

  const aiEnabled = useQuery({
    queryKey: ['support', 'ai'],
    queryFn: getAIStatus,
    staleTime: 5 * 60_000,
  }).data?.enabled
  const tickets = useQuery({
    queryKey: ['support', 'mine'],
    queryFn: listMyTickets,
  })
  const ticketId =
    chosen === undefined ? pickConversation(tickets.data) : chosen
  const key = ['support', 'mine', ticketId]
  const conv = useQuery({
    queryKey: key,
    enabled: !!ticketId,
    queryFn: () =>
      getMyTicket(ticketId as number).then((d) => {
        // The server marked it read on this GET; let the badges catch up.
        void qc.invalidateQueries({ queryKey: SUPPORT_UNREAD_KEY })
        return d
      }),
    refetchInterval: shouldPoll({ open: true, visible, ticketId })
      ? WIDGET_POLL_MS
      : false,
  })
  const conversation = ticketId ? conv.data : undefined
  const settle = (data: SupportConversation) => {
    qc.setQueryData(['support', 'mine', data.ticket.id], data)
    void qc.invalidateQueries({ queryKey: ['support', 'mine'], exact: true })
  }
  const ai = useWidgetAI(conversation, aiEnabled, () =>
    Promise.all([
      qc.invalidateQueries({ queryKey: key }),
      qc.invalidateQueries({ queryKey: ['support', 'mine'], exact: true }),
    ])
  )

  const send = useMutation({
    mutationFn: async (text: string) => {
      if (ticketId) return addMyMessage(ticketId, text)
      const ticket = await createTicket(deriveSubject(text), text)
      return getMyTicket(ticket.id)
    },
    onSuccess: (data) => {
      settle(data)
      setChosen(data.ticket.id)
      setEnded(false)
      setBody('')
    },
    onError: (e) => setError((e as Error).message),
  })
  const handoff = useMutation({
    mutationFn: () => handoffMyTicket(ticketId as number),
    onSuccess: settle,
    onError: (e) => setError((e as Error).message),
  })
  const close = useMutation({
    mutationFn: () => closeMyTicket(ticketId as number),
    onSuccess: (data) => {
      settle(data)
      setChosen(null)
      setEnded(true)
    },
    onError: (e) => setError((e as Error).message),
  })

  const messages = conversation?.messages ?? []
  const mode = widgetMode(aiEnabled, messages)
  const count = messages.length
  useEffect(() => {
    const el = listRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [count, ai.answer, ai.progress])

  const submit = () => {
    const text = body.trim()
    if (!text || send.isPending) return
    setError(null)
    send.mutate(text)
  }

  return (
    <section
      role='dialog'
      aria-label={t('Unify API support')}
      className='border-border bg-background text-foreground fixed inset-x-2 bottom-14 z-40 flex h-[min(34rem,calc(100svh-8rem))] flex-col overflow-hidden rounded-xl border shadow-xl sm:right-4 sm:left-auto sm:w-96'
    >
      <header className='border-border flex items-start justify-between gap-2 border-b px-4 py-3'>
        <div className='min-w-0'>
          <h2 className='text-sm font-semibold'>{t('Unify API support')}</h2>
          <p className='text-muted-foreground mt-0.5 text-xs'>
            {mode === 'ai'
              ? t(
                  'Our AI assistant answers first. You can ask for a person at any time.'
                )
              : t('We will reply here and by email.')}
          </p>
        </div>
        <Button
          variant='ghost'
          size='icon'
          className='size-7 shrink-0'
          aria-label={t('Close support chat')}
          onClick={props.onClose}
        >
          <X className='size-4' />
        </Button>
      </header>

      <ol
        ref={listRef}
        className='flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-3 py-3'
      >
        {ticketId && conv.isLoading ? <Notice>{t('Loading…')}</Notice> : null}
        {!ticketId && ended ? (
          <Notice>
            {t('Conversation ended. Your next message starts a new one.')}
          </Notice>
        ) : null}
        {!ticketId && !ended ? (
          <Notice>
            {t(
              'How can we help? Ask anything about your account, keys, models or billing.'
            )}
          </Notice>
        ) : null}
        {messages.map((m) =>
          isHandoff(m) ? (
            <Notice key={m.id}>
              {t(
                'You asked to talk to a person. A teammate will reply here and by email.'
              )}
            </Notice>
          ) : (
            <Bubble
              key={m.id}
              mine={!m.from_staff}
              at={m.created_at}
              text={m.body}
              who={t(author(m))}
            />
          )
        )}
        <AIStatus {...ai} />
      </ol>

      <div className='border-border flex flex-col gap-2 border-t px-3 py-2'>
        {error ? <p className='text-destructive text-xs'>{error}</p> : null}
        <div className='flex items-end gap-2'>
          <Textarea
            value={body}
            maxLength={BODY_MAX}
            rows={2}
            aria-label={t('Your message')}
            placeholder={t('Type your message…')}
            className='max-h-32 min-h-10 flex-1 resize-none text-sm'
            onChange={(e) => setBody(e.target.value)}
            onKeyDown={(e) => {
              if (
                e.key === 'Enter' &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault()
                submit()
              }
            }}
          />
          <Button
            size='icon'
            aria-label={send.isPending ? t('Sending…') : t('Send')}
            disabled={send.isPending || body.trim() === ''}
            onClick={submit}
          >
            <Send className='size-4' />
          </Button>
        </div>
        <div className='flex flex-wrap items-center gap-x-3 gap-y-1 text-xs'>
          {canHandOff(conversation) ? (
            <button
              type='button'
              className='text-primary inline-flex items-center gap-1 hover:underline disabled:opacity-50'
              disabled={handoff.isPending}
              onClick={() => {
                setError(null)
                handoff.mutate()
              }}
            >
              <UserRound className='size-3.5' />
              {t('Talk to a human')}
            </button>
          ) : null}
          {conversation ? (
            <button
              type='button'
              className='text-muted-foreground hover:underline disabled:opacity-50'
              disabled={close.isPending}
              onClick={() => {
                setError(null)
                close.mutate()
              }}
            >
              {t('End conversation')}
            </button>
          ) : null}
          <Link
            to='/support'
            className='text-muted-foreground ml-auto hover:underline'
            onClick={props.onClose}
          >
            {t('See all conversations')}
          </Link>
        </div>
      </div>
    </section>
  )
}

// The launcher is a slim tab on the right edge, in the lower third, rather
// than the usual round button in the corner: every list page in the console
// puts its pagination in the bottom-right corner, and a corner button covered
// the next/last-page controls at every width (seen at 375px and on desktop).
// Flush to the edge it overlaps only the page's own right padding, and it
// stays clear of the attribution bar under the content.
// Storage can be missing or throw (private windows, blocked site data); the
// chat then simply starts open, which is the default anyway.
function readClosed(): boolean {
  try {
    return window.localStorage.getItem(WIDGET_CLOSED_KEY) === '1'
  } catch {
    return false
  }
}

function writeClosed(closed: boolean) {
  try {
    if (closed) {
      window.localStorage.setItem(WIDGET_CLOSED_KEY, '1')
    } else {
      window.localStorage.removeItem(WIDGET_CLOSED_KEY)
    }
  } catch {
    // remembered for this page only
  }
}

function Launcher() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(() =>
    typeof window === 'undefined'
      ? false
      : initialOpen(readClosed(), window.innerWidth)
  )
  const badge = unreadBadge(useSupportUnread().data?.count)
  const close = () => {
    setOpen(false)
    writeClosed(true)
  }
  useEffect(() => {
    if (!open) {
      return
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false)
        writeClosed(true)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])
  if (open) {
    return <ChatPanel onClose={close} />
  }
  return (
    <button
      type='button'
      aria-label={t('Open support chat')}
      title={t('Open support chat')}
      aria-expanded={false}
      className='bg-primary text-primary-foreground focus-visible:ring-ring fixed right-0 bottom-[30%] z-40 flex h-12 w-8 items-center justify-center rounded-l-lg shadow-md transition-[width] hover:w-10 focus-visible:ring-2 focus-visible:outline-none'
      onClick={() => {
        setOpen(true)
        writeClosed(false)
      }}
    >
      <MessageCircle className='size-4' />
      {badge ? (
        <span className='bg-destructive text-destructive-foreground absolute -top-2 -left-2 min-w-5 rounded-full px-1 text-center text-[11px] leading-5'>
          {badge}
        </span>
      ) : null}
    </button>
  )
}

/** The floating support chat, for everyone signed in; nothing on /support. */
export function SupportWidget() {
  const user = useAuthStore((s) => s.auth.user)
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  if (!showWidget(pathname, !!user)) return null
  return <Launcher />
}
