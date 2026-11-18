/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'

import {
  getModelLaunchState,
  previewModelLaunch,
  sendModelLaunch,
  type ModelLaunchPreview,
} from './api'
import { when } from './format'

// The new-model announcement email. Nothing is mailed until the operator has
// seen the rendered email and pressed Send; the server re-checks every model
// (enabled, priced, not announced before) on both calls.

export function ModelLaunch() {
  const qc = useQueryClient()
  const [models, setModels] = useState('')
  const [note, setNote] = useState('')
  const [preview, setPreview] = useState<ModelLaunchPreview | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const state = useQuery({
    queryKey: ['ops-model-launch'],
    queryFn: getModelLaunchState,
    // A send runs in the background; poll while one is unfinished.
    refetchInterval: (q) =>
      q.state.data?.launches.some((l) => l.finished_at === 0) ? 3000 : false,
  })

  const picked = models
    .split(/[\s,]+/)
    .map((m) => m.trim())
    .filter(Boolean)

  const previewRun = useMutation({
    mutationFn: () => previewModelLaunch(picked, note),
    onSuccess: (p) => {
      setError(null)
      setNotice(null)
      setPreview(p)
    },
    onError: (e: Error) => {
      setPreview(null)
      setError(e.message)
    },
  })

  const sendRun = useMutation({
    mutationFn: async () => {
      if (!preview) return null
      if (
        !window.confirm(
          `Email "${preview.subject}" to ${preview.recipients} customers now? This cannot be recalled.`
        )
      ) {
        return null
      }
      return sendModelLaunch(picked, note)
    },
    onSuccess: (launch) => {
      if (!launch) return
      setError(null)
      setPreview(null)
      setModels('')
      setNote('')
      setNotice(
        `Announcement #${launch.id} started; progress is in the table below.`
      )
      void qc.invalidateQueries({ queryKey: ['ops-model-launch'] })
    },
    onError: (e: Error) => setError(e.message),
  })

  const data = state.data
  return (
    <section className='border-border bg-card mb-6 overflow-hidden rounded-lg border'>
      <div className='p-4'>
        <h2 className='text-foreground text-base'>New-model announcement</h2>
        <p className='text-muted-foreground mt-0.5 text-xs'>
          One email to every customer who gets the weekly update
          {data ? ` (${data.recipients} now)` : ''}. Prices are read from
          billing at send time.
          {data && !data.enabled
            ? ' Sending is off on this deployment (UNIFYAPI_RETENTION_EMAIL).'
            : ''}
        </p>
      </div>
      {state.error ? (
        <div className='text-destructive border-border border-t px-4 py-2 text-xs'>
          {state.error.message}
        </div>
      ) : null}
      <div className='border-border space-y-3 border-t p-4'>
        {data?.candidates.length ? (
          <div className='text-xs'>
            <span className='text-muted-foreground'>
              Not yet told to anyone:{' '}
            </span>
            {data.candidates.map((m) => (
              <button
                key={m}
                type='button'
                onClick={() =>
                  setModels((cur) =>
                    picked.includes(m) ? cur : `${cur} ${m}`.trim()
                  )
                }
                className='text-primary mr-2 font-mono hover:underline'
              >
                {m}
              </button>
            ))}
          </div>
        ) : null}
        <label className='block text-xs'>
          <span className='text-muted-foreground'>Model ids</span>
          <input
            value={models}
            onChange={(e) => {
              setModels(e.target.value)
              setPreview(null)
            }}
            placeholder='gpt-6.2-sol gpt-6.2-astra'
            className='border-border bg-background mt-1 block w-full rounded-md border px-2 py-1.5 font-mono text-sm'
          />
        </label>
        <label className='block text-xs'>
          <span className='text-muted-foreground'>
            Note (optional, replaces the default first paragraph)
          </span>
          <textarea
            value={note}
            maxLength={600}
            rows={2}
            onChange={(e) => {
              setNote(e.target.value)
              setPreview(null)
            }}
            className='border-border bg-background mt-1 block w-full rounded-md border px-2 py-1.5 text-sm'
          />
        </label>
        <div className='flex flex-wrap items-center gap-2'>
          <button
            type='button'
            disabled={!picked.length || previewRun.isPending}
            onClick={() => previewRun.mutate()}
            className='border-border text-foreground hover:bg-muted rounded-md border px-3 py-1.5 text-xs disabled:opacity-50'
          >
            {previewRun.isPending ? 'Rendering…' : 'Preview'}
          </button>
          <button
            type='button'
            disabled={!preview || !preview.enabled || sendRun.isPending}
            onClick={() => sendRun.mutate()}
            className='bg-primary text-primary-foreground rounded-md px-3 py-1.5 text-xs disabled:opacity-50'
          >
            {preview
              ? `Send to ${preview.recipients} customers`
              : 'Send (preview first)'}
          </button>
          {error ? (
            <span className='text-destructive text-xs'>{error}</span>
          ) : null}
          {notice ? (
            <span className='text-muted-foreground text-xs'>{notice}</span>
          ) : null}
        </div>
        {preview ? (
          <iframe
            title='Email preview'
            sandbox=''
            srcDoc={preview.html}
            className='border-border h-[560px] w-full rounded-md border bg-white'
          />
        ) : null}
      </div>
      {data?.launches.length ? (
        <div className='overflow-x-auto border-t'>
          <table className='w-full text-sm'>
            <thead className='bg-muted/50 text-muted-foreground text-left text-xs'>
              <tr>
                <th className='px-4 py-2.5 font-normal'>Sent</th>
                <th className='px-4 py-2.5 font-normal'>Models</th>
                <th className='px-4 py-2.5 text-right font-normal'>
                  Delivered / failed
                </th>
              </tr>
            </thead>
            <tbody>
              {data.launches.map((l) => (
                <tr key={l.id} className='border-border/60 border-t'>
                  <td className='text-muted-foreground px-4 py-2 text-xs'>
                    {when(l.created_at)}
                  </td>
                  <td className='px-4 py-2 font-mono text-xs'>
                    {l.models.split('\n').join(', ')}
                  </td>
                  <td className='px-4 py-2 text-right text-xs'>
                    {l.finished_at ? `${l.sent} / ${l.failed}` : 'sending…'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  )
}
