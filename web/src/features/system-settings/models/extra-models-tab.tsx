/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: the "extra model pricing" tab.

This replaced the raw-ratio editor, and the difference is the whole point. That
editor wrote the ModelRatio options row, which REPLACES the code catalog
wholesale — one save discarded the price basis for every other model, and
production once held such a row with 2,877 keys. This table merges: it can only
add models the catalog does not carry, so nothing here can touch a price that
has provenance and an automated drift check.

Prices are entered in USD per 1M tokens, the same unit as the catalog, and the
billing ratio is shown derived and read-only beside them. That asymmetry is
deliberate: the ratio is the unreadable form, and a decimal typed one place off
in that form billed a model at 8.5% of cost here for weeks.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  AlertTriangle,
  DownloadCloud,
  Info,
  Pencil,
  Plus,
  Save,
  Trash2,
  X,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { getExtraModels, lookupModelPrice, updateExtraModels } from '../api'
import type {
  ExtraModelDraft,
  ExtraModelRow,
  ModelPriceCandidate,
} from '../types'
import {
  completionRatioFromUSD,
  draftToPayload,
  emptyDraft,
  formatUSDPrice,
  ratioFromUSD,
  validateDraft,
} from './extra-models-logic'

export function ExtraModelsTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['extra-models'],
    queryFn: getExtraModels,
  })

  const [rows, setRows] = useState<ExtraModelRow[]>([])
  const [draft, setDraft] = useState<ExtraModelDraft>(emptyDraft)

  const serverRows = data?.data?.models
  useEffect(() => {
    if (serverRows) setRows(serverRows)
  }, [serverRows])

  const catalogued = data?.data?.catalogued_models
  const errors = useMemo(
    () =>
      validateDraft(
        draft,
        catalogued ?? [],
        rows.map((row) => row.model)
      ),
    [draft, catalogued, rows]
  )
  const touched =
    draft.model !== '' || draft.input_usd !== '' || draft.output_usd !== ''

  const [candidates, setCandidates] = useState<ModelPriceCandidate[] | null>(
    null
  )

  /* Sync fills the form from the vendor's published price so nobody retypes
     four numbers off another tab -- retyping is where a decimal slips, and a
     slipped decimal billed a model at 8.5% of cost on this deployment.

     It fills, it does not save. The candidate list is shown because the same id
     is listed by many providers at different prices; picking silently would be
     the console making a commercial decision. */
  const lookup = useMutation({
    mutationFn: lookupModelPrice,
    onSuccess: (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message ?? t('Not found on models.dev'))
        setCandidates(null)
        return
      }
      setCandidates(response.data.candidates)
    },
    onError: (err: Error) => toast.error(err.message),
  })

  const applyCandidate = (candidate: ModelPriceCandidate) => {
    setDraft((current) => ({
      ...current,
      input_usd: String(candidate.input_usd),
      output_usd: String(candidate.output_usd),
      cache_read_usd: candidate.cache_read_usd
        ? String(candidate.cache_read_usd)
        : '',
      cache_write_usd: candidate.cache_write_usd
        ? String(candidate.cache_write_usd)
        : '',
      vendor: candidate.provider,
      note: current.note || `models.dev · ${candidate.provider}`,
    }))
    setCandidates(null)
  }

  const mutation = useMutation({
    mutationFn: updateExtraModels,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(
          response.errors?.join('; ') ?? response.message ?? t('Failed to save')
        )
        return
      }
      toast.success(response.message ?? t('Saved'))
      queryClient.invalidateQueries({ queryKey: ['extra-models'] })
      // The catalog and the public price list both change when an extra is
      // added, so neither may keep serving a cached answer.
      queryClient.invalidateQueries({ queryKey: ['pricing'] })
      queryClient.invalidateQueries({ queryKey: ['pricing-baseline'] })
    },
    onError: (err: Error) => toast.error(err.message),
  })

  const save = (next: ExtraModelRow[]) => {
    mutation.mutate(
      Object.fromEntries(
        next.map((row) => [
          row.model,
          draftToPayload({
            model: row.model,
            input_usd: String(row.input_usd),
            output_usd: String(row.output_usd),
            cache_read_usd: row.cache_read_usd
              ? String(row.cache_read_usd)
              : '',
            cache_write_usd: row.cache_write_usd
              ? String(row.cache_write_usd)
              : '',
            vendor: row.vendor ?? '',
            note: row.note ?? '',
          }),
        ])
      )
    )
  }

  const addDraft = () => {
    if (errors.length > 0) return
    const input = Number.parseFloat(draft.input_usd)
    const output = Number.parseFloat(draft.output_usd)
    const added: ExtraModelRow = {
      model: draft.model,
      vendor: draft.vendor || undefined,
      note: draft.note || undefined,
      input_usd: input,
      output_usd: output,
      cache_read_usd: draft.cache_read_usd
        ? Number.parseFloat(draft.cache_read_usd)
        : undefined,
      cache_write_usd: draft.cache_write_usd
        ? Number.parseFloat(draft.cache_write_usd)
        : undefined,
      discount: 1,
      model_ratio: ratioFromUSD(input),
      completion_ratio: completionRatioFromUSD(input, output),
    }
    const next = [...rows, added]
    setRows(next)
    setDraft(emptyDraft())
    save(next)
  }

  /* Editing an existing row loads it back into the same form rather than
     offering a second, subtly different one. Two edit paths for one table is how
     they drift. */
  const edit = (row: ExtraModelRow) => {
    setDraft({
      model: row.model,
      input_usd: String(row.input_usd),
      output_usd: String(row.output_usd),
      cache_read_usd: row.cache_read_usd ? String(row.cache_read_usd) : '',
      cache_write_usd: row.cache_write_usd ? String(row.cache_write_usd) : '',
      vendor: row.vendor ?? '',
      note: row.note ?? '',
    })
    setRows(rows.filter((r) => r.model !== row.model))
    setCandidates(null)
  }

  const remove = (model: string) => {
    const next = rows.filter((row) => row.model !== model)
    setRows(next)
    save(next)
  }

  const errorFor = (field: keyof ExtraModelDraft) =>
    errors.find((e) => e.field === field)?.message

  return (
    <div className='flex flex-col gap-4'>
      <Alert>
        <Info className='size-4' />
        <AlertDescription className='text-xs'>
          {t(
            'Prices here are added ON TOP of the built-in catalog and never overwrite it. Use this for a model the catalog does not carry yet — it takes effect immediately, with no release. A model the catalog already prices cannot be entered here; change what customers pay for it under 官方报价与折扣 instead.'
          )}
        </AlertDescription>
      </Alert>

      <Alert variant='destructive'>
        <AlertTriangle className='size-4' />
        <AlertDescription className='text-xs'>
          {t(
            'Nobody is watching these prices for you. Catalogued models are checked against the vendor daily and a price change is reported; a price typed here is checked by no one, so re-read the vendor page yourself when it matters.'
          )}
        </AlertDescription>
      </Alert>

      {isLoading ? (
        <div className='text-muted-foreground p-6 text-sm'>
          {t('Loading...')}
        </div>
      ) : null}
      {!isLoading && isError ? (
        <Alert variant='destructive'>
          <AlertDescription>{(error as Error)?.message}</AlertDescription>
        </Alert>
      ) : null}

      {!isLoading && !isError ? (
        <div className='overflow-x-auto rounded-md border'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Model')}</TableHead>
                <TableHead className='text-right'>{t('Input $/1M')}</TableHead>
                <TableHead className='text-right'>{t('Output $/1M')}</TableHead>
                <TableHead className='text-right'>
                  {t('Cache read $/1M')}
                </TableHead>
                <TableHead className='text-right'>
                  {t('Billing ratio')}
                </TableHead>
                <TableHead className='w-20' />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.model}>
                  <TableCell className='font-mono text-xs'>
                    {row.model}
                    <Badge variant='outline' className='ml-2 text-[10px]'>
                      {t('unverified')}
                    </Badge>
                    {row.discount !== 1 ? (
                      <Badge variant='outline' className='ml-2 text-[10px]'>
                        {t('discount')} {row.discount}
                      </Badge>
                    ) : null}
                    {row.note ? (
                      <span className='text-muted-foreground block text-[10px]'>
                        {row.note}
                      </span>
                    ) : null}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {formatUSDPrice(row.input_usd)}
                  </TableCell>
                  <TableCell className='text-right font-mono text-xs tabular-nums'>
                    {formatUSDPrice(row.output_usd)}
                  </TableCell>
                  <TableCell className='text-muted-foreground text-right font-mono text-xs tabular-nums'>
                    {row.cache_read_usd
                      ? formatUSDPrice(row.cache_read_usd)
                      : '—'}
                  </TableCell>
                  <TableCell className='text-muted-foreground text-right font-mono text-xs tabular-nums'>
                    {row.model_ratio.toFixed(4)} ×{' '}
                    {row.completion_ratio.toFixed(2)}
                  </TableCell>
                  <TableCell className='whitespace-nowrap'>
                    <Button
                      size='sm'
                      variant='ghost'
                      disabled={mutation.isPending}
                      onClick={() => edit(row)}
                      aria-label={t('Edit this price')}
                    >
                      <Pencil className='size-4' />
                    </Button>
                    <Button
                      size='sm'
                      variant='ghost'
                      disabled={mutation.isPending}
                      onClick={() => remove(row.model)}
                      aria-label={t('Remove this model')}
                    >
                      <Trash2 className='text-destructive size-4' />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
              {rows.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={6}
                    className='text-muted-foreground py-6 text-center text-sm'
                  >
                    {t(
                      'No extra models. Everything on sale is priced by the catalog.'
                    )}
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>
      ) : null}

      <div className='rounded-md border p-3'>
        <div className='text-muted-foreground mb-2 text-[10px] font-semibold tracking-wider uppercase'>
          {t('Add a model')}
        </div>
        <div className='flex flex-wrap items-end gap-2'>
          <Field
            label={t('Model name')}
            className='w-56'
            value={draft.model}
            onChange={(v) => setDraft({ ...draft, model: v })}
            error={touched ? errorFor('model') : undefined}
            placeholder='some-vendor-model'
          />
          <Button
            size='sm'
            variant='outline'
            disabled={!draft.model.trim() || lookup.isPending}
            onClick={() => lookup.mutate(draft.model.trim())}
          >
            <DownloadCloud className='size-4' />
            {lookup.isPending ? t('Looking up...') : t('Sync official price')}
          </Button>
          <Field
            label={t('Input $/1M')}
            className='w-28'
            value={draft.input_usd}
            onChange={(v) => setDraft({ ...draft, input_usd: v })}
            error={touched ? errorFor('input_usd') : undefined}
            placeholder='1.40'
          />
          <Field
            label={t('Output $/1M')}
            className='w-28'
            value={draft.output_usd}
            onChange={(v) => setDraft({ ...draft, output_usd: v })}
            error={touched ? errorFor('output_usd') : undefined}
            placeholder='4.40'
          />
          <Field
            label={t('Cache read $/1M')}
            className='w-32'
            value={draft.cache_read_usd}
            onChange={(v) => setDraft({ ...draft, cache_read_usd: v })}
            error={touched ? errorFor('cache_read_usd') : undefined}
            placeholder={t('optional')}
          />
          <Field
            label={t('Note')}
            className='min-w-40 flex-1'
            value={draft.note}
            onChange={(v) => setDraft({ ...draft, note: v })}
            placeholder={t('where this price came from')}
          />
          <Button
            size='sm'
            disabled={errors.length > 0 || !touched || mutation.isPending}
            onClick={addDraft}
          >
            {mutation.isPending ? (
              <Save className='size-4' />
            ) : (
              <Plus className='size-4' />
            )}
            {t('Add and save')}
          </Button>
        </div>

        {candidates ? (
          <div className='mt-3 rounded-md border'>
            <div className='flex items-center justify-between border-b px-3 py-2'>
              <span className='text-muted-foreground text-xs'>
                {t(
                  '{{count}} providers publish a price for this model. Pick one.',
                  {
                    count: candidates.length,
                  }
                )}
              </span>
              <Button
                size='sm'
                variant='ghost'
                onClick={() => setCandidates(null)}
              >
                <X className='size-4' />
              </Button>
            </div>
            <div className='max-h-56 overflow-auto'>
              {candidates.map((candidate) => (
                <button
                  type='button'
                  key={`${candidate.provider}:${candidate.model}`}
                  className='hover:bg-muted/50 flex w-full items-center gap-3 px-3 py-1.5 text-left text-xs'
                  onClick={() => applyCandidate(candidate)}
                >
                  <span className='font-mono'>{candidate.provider}</span>
                  {candidate.first_party ? (
                    <Badge variant='outline' className='text-[10px]'>
                      {t('vendor')}
                    </Badge>
                  ) : null}
                  <span className='text-muted-foreground ml-auto font-mono tabular-nums'>
                    {formatUSDPrice(candidate.input_usd)} /{' '}
                    {formatUSDPrice(candidate.output_usd)}
                  </span>
                </button>
              ))}
            </div>
            <p className='text-muted-foreground border-t px-3 py-2 text-[10px]'>
              {t(
                'models.dev aggregates these and can lag the vendor — it carried DeepSeek pre-increase prices for 17 days. Syncing saves typing, not checking.'
              )}
            </p>
          </div>
        ) : null}

        {/* Shown live rather than after a failed save: the most common mistake
            is naming a model the catalog already prices, and finding that out
            on submit means retyping the row. */}
        {touched && errors.length > 0 ? (
          <p className='text-destructive mt-2 text-xs'>{errors[0].message}</p>
        ) : null}
        {touched &&
        errors.length === 0 &&
        draft.input_usd &&
        draft.output_usd ? (
          <p className='text-muted-foreground mt-2 text-xs'>
            {t(
              'Will bill at ratio {{ratio}} with a {{completion}}x output multiplier.',
              {
                ratio: ratioFromUSD(Number.parseFloat(draft.input_usd)).toFixed(
                  4
                ),
                completion: completionRatioFromUSD(
                  Number.parseFloat(draft.input_usd),
                  Number.parseFloat(draft.output_usd)
                ).toFixed(2),
              }
            )}
          </p>
        ) : null}
      </div>
    </div>
  )
}

function Field({
  label,
  value,
  onChange,
  placeholder,
  error,
  className,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  placeholder?: string
  error?: string
  className?: string
}) {
  return (
    <label className={`flex flex-col gap-1 text-xs ${className ?? ''}`}>
      <span className='text-muted-foreground'>{label}</span>
      <Input
        className={`h-8 text-xs ${error ? 'border-destructive' : ''}`}
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </label>
  )
}
