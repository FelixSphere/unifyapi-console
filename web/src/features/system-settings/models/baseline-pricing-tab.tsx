/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
/*
UNIFYAPI-FORK: the "official price & discount" tab of the model pricing card.

This tab exists because the sibling tabs edit the wrong thing. They edit raw
billing ratios, which conflates two facts into one number -- what the vendor
charges, and what we choose to charge -- and stores the result in an options row
that replaces the entire code baseline on load. That is how claude-opus-4-8 came
to be sold at 0.085x of Anthropic's price: someone typed 0.2125 where 2.5 was
meant, and no reviewer or test could tell a typo from a deliberate 91% discount.

So here the official price is READ-ONLY (it lives in
setting/ratio_setting/unifyapi_catalog.go and is checked against models.dev),
and the only editable column is the discount. Everything is shown in USD per 1M
tokens, never in ratio units, so a wrong number looks wrong.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, RotateCcw, Save, ShieldAlert } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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

import { getPricingBaseline, updatePricingDiscount } from '../api'
import type { PricingBaselineModel } from '../types'
import {
  discountLabel,
  discountPayload,
  formatUSD,
  invalidDiscountModels,
  parseDiscount,
} from './baseline-pricing-logic'

export function BaselinePricingTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['pricing-baseline'],
    queryFn: getPricingBaseline,
  })

  const [drafts, setDrafts] = useState<Record<string, string>>({})
  const [filter, setFilter] = useState('')

  // Seed the editable column from the server once it arrives. Keyed on the
  // fetched discounts so a refetch after save re-syncs, but local edits are not
  // clobbered mid-typing by a background refetch of unchanged data.
  const serverDiscounts = data?.data?.discounts
  useEffect(() => {
    if (!serverDiscounts) return
    setDrafts(
      Object.fromEntries(
        Object.entries(serverDiscounts).map(([model, discount]) => [
          model,
          String(discount),
        ])
      )
    )
  }, [serverDiscounts])

  const mutation = useMutation({
    mutationFn: updatePricingDiscount,
    onSuccess: (response) => {
      if (!response.success) {
        // Server-side validation returns the per-model reasons; surfacing only
        // "failed" would leave the admin guessing which row is wrong.
        const detail = response.errors?.join('; ') ?? response.message
        toast.error(detail ?? t('Failed to save discounts'))
        return
      }
      toast.success(response.message ?? t('Discounts saved'))
      response.markups?.forEach((markup) => toast.warning(markup))
      queryClient.invalidateQueries({ queryKey: ['pricing-baseline'] })
      queryClient.invalidateQueries({ queryKey: ['system-options'] })
      // The relay bills the new price immediately, so the model square has to
      // stop showing the old one immediately too. Its query is keyed ['pricing']
      // with a 5-minute staleTime; without this the page quoted the previous
      // price for minutes after the save while customers were already charged
      // the new one.
      queryClient.invalidateQueries({ queryKey: ['pricing'] })
    },
    onError: (err: Error) => toast.error(err.message),
  })

  // Memoised off the fetched array rather than the `?? []` fallback: that
  // literal is a fresh array on every render, so depending on it would make
  // the filter below re-run on every keystroke over all 59 rows.
  const models = useMemo(() => data?.data?.models ?? [], [data?.data?.models])
  const groups = useMemo(
    () => Object.keys(data?.data?.group_ratios ?? {}).sort(),
    [data?.data?.group_ratios]
  )

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase()
    if (!needle) return models
    return models.filter(
      (row) =>
        row.model.toLowerCase().includes(needle) ||
        row.vendor.toLowerCase().includes(needle)
    )
  }, [models, filter])

  const invalidModels = useMemo(() => invalidDiscountModels(drafts), [drafts])

  const handleSave = useCallback(() => {
    if (invalidModels.length > 0) {
      toast.error(t('Invalid discount for: ') + invalidModels.join(', '))
      return
    }
    mutation.mutate(discountPayload(drafts))
  }, [drafts, invalidModels, mutation, t])

  const handleClearAll = useCallback(() => setDrafts({}), [])

  if (isLoading) return <div className='p-4 text-sm'>{t('Loading...')}</div>
  if (isError) {
    return (
      <Alert variant='destructive'>
        <AlertDescription>{(error as Error)?.message}</AlertDescription>
      </Alert>
    )
  }

  const shadowWarning = data?.data?.shadow_warning
  const shadows = data?.data?.shadows ?? []

  return (
    <div className='flex min-h-0 flex-col gap-4'>
      {shadowWarning ? (
        <Alert variant='destructive'>
          <ShieldAlert className='size-4' />
          <AlertTitle>
            {t('Database is overriding the code baseline')}
          </AlertTitle>
          <AlertDescription>
            <p>{shadowWarning}</p>
            <ul className='mt-2 list-disc pl-4 text-xs'>
              {shadows.slice(0, 8).map((shadow) => (
                <li key={`${shadow.option}/${shadow.model}`}>
                  <code>
                    {shadow.option}[{shadow.model}]
                  </code>{' '}
                  baseline={shadow.baseline} live={shadow.live} —{' '}
                  {shadow.reason}
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      ) : null}

      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'Official prices are read-only. This discount is the global fallback; a customer-model price configured in Group Pricing takes priority and is not multiplied again.'
          )}{' '}
          <strong>{t('Snapshot:')}</strong> {data?.data?.snapshot_date}
        </AlertDescription>
      </Alert>

      <div className='flex flex-wrap items-center gap-2'>
        <Input
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder={t('Filter by model or vendor')}
          className='max-w-xs'
        />
        <span className='text-muted-foreground text-xs'>
          {visible.length}/{models.length} {t('models')}
        </span>
        <div className='ml-auto flex gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={handleClearAll}
            disabled={mutation.isPending}
          >
            <RotateCcw className='size-4' />
            {t('Clear all discounts')}
          </Button>
          <Button
            type='button'
            size='sm'
            onClick={handleSave}
            disabled={mutation.isPending || invalidModels.length > 0}
          >
            <Save className='size-4' />
            {t('Save discounts')}
          </Button>
        </div>
      </div>

      {/*
        An explicit viewport-relative cap rather than flex-1: the parent chain
        here does not constrain height, so flex-1 grows to all 59 rows and
        stretches the page instead of scrolling inside the card. The header is
        sticky because the group columns are meaningless once their labels have
        scrolled away.
      */}
      <div className='max-h-[60vh] min-h-0 flex-1 overflow-auto rounded-md border'>
        <Table>
          <TableHeader className='bg-background sticky top-0 z-10'>
            <TableRow>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Vendor')}</TableHead>
              <TableHead className='text-right'>
                {t('Official in/out')}
              </TableHead>
              <TableHead className='w-40'>{t('Discount')}</TableHead>
              {groups.map((group) => (
                <TableHead key={group} className='text-right'>
                  {group}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {visible.map((row) => (
              <BaselineRow
                key={row.model}
                row={row}
                groups={groups}
                draft={drafts[row.model] ?? ''}
                onChange={(value) =>
                  setDrafts((previous) => ({ ...previous, [row.model]: value }))
                }
              />
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

/** renderDiscountLabel localises the pricing position. */
function renderDiscountLabel(
  t: (key: string, options?: Record<string, unknown>) => string,
  discount: number
): string {
  const label = discountLabel(discount)
  switch (label.kind) {
    case 'list':
      return t('official price')
    case 'markup':
      return t('+{{percent}}% markup', { percent: label.percent.toFixed(0) })
    default:
      return t('-{{percent}}%', { percent: label.percent.toFixed(0) })
  }
}

type BaselineRowProps = {
  row: PricingBaselineModel
  groups: string[]
  draft: string
  onChange: (value: string) => void
}

function BaselineRow({ row, groups, draft, onChange }: BaselineRowProps) {
  const { t } = useTranslation()
  const parsed = parseDiscount(draft)
  const invalid = Number.isNaN(parsed)
  const discount = invalid || parsed === null ? 1 : parsed

  return (
    <TableRow>
      <TableCell className='font-mono text-xs'>
        {row.model}
        {row.unverified ? (
          <Badge variant='outline' className='ml-2 gap-1 text-[10px]'>
            <AlertTriangle className='size-3' />
            {t('unverified')}
          </Badge>
        ) : null}
      </TableCell>
      <TableCell className='text-muted-foreground text-xs'>
        {row.vendor || '—'}
        {row.upstream_model ? (
          <span className='block font-mono text-[10px]'>
            {row.upstream_model}
          </span>
        ) : null}
      </TableCell>
      <TableCell className='text-right font-mono text-xs whitespace-nowrap'>
        {formatUSD(row.official_input_usd)} /{' '}
        {formatUSD(row.official_output_usd)}
      </TableCell>
      <TableCell className='w-28'>
        <Input
          value={draft}
          onChange={(event) => onChange(event.target.value)}
          placeholder='1.0'
          aria-invalid={invalid}
          className={`h-8 font-mono text-xs ${invalid ? 'border-destructive' : ''}`}
        />
        {/* block, not inline: as a span it flowed past the cell and overlapped
            the first group column. */}
        <span
          className={`block text-[10px] ${discount > 1 ? 'text-amber-600' : 'text-muted-foreground'}`}
        >
          {invalid ? t('must be > 0') : renderDiscountLabel(t, discount)}
        </span>
      </TableCell>
      {groups.map((group) => {
        const groupRatio = row.group_prices[group]?.group_ratio ?? 1
        const customerMultiplier = row.group_prices[group]?.customer_multiplier
        const factor = customerMultiplier ?? discount * groupRatio
        return (
          <TableCell
            key={group}
            className='text-right font-mono text-xs whitespace-nowrap'
          >
            {formatUSD(row.official_input_usd * factor)}
            {' / '}
            {formatUSD(row.official_output_usd * factor)}
          </TableCell>
        )
      })}
    </TableRow>
  )
}
