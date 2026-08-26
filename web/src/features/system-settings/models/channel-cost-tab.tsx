/*
UNIFYAPI-FORK: the "upstream purchasing cost" tab.

The second of the three prices UnifyAPI keeps apart -- what our upstream charges
us. It exists purely so reconciliation has a cost basis, and it is the one
pricing number that must never reach a customer's invoice: routing is load
balanced, so the same request goes to a different channel on different days. If
a channel's cost fed into what we charge, an identical request would bill
differently by route and no customer could reconcile their own invoice.

Expressed as a multiplier on the vendor's official list price rather than as
absolute prices, because a reseller contract is almost always "list minus N%" --
one number per channel captures it, and it stays correct when the vendor
reprices.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, RotateCcw, Save } from 'lucide-react'
import { useCallback, useEffect, useMemo, useState } from 'react'
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
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'

import { getChannelCost, updateChannelCost } from '../api'
import type { ChannelCostRow } from '../types'
import {
  costLabel,
  costPayload,
  invalidCostChannels,
  maxSafeDiscount,
  parseCostRatio,
} from './channel-cost-logic'

export function ChannelCostTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['channel-cost'],
    queryFn: getChannelCost,
  })

  const [drafts, setDrafts] = useState<Record<string, string>>({})

  // Seed from the server's configured values. Channels at list price are left
  // blank rather than pre-filled with "1", so the column reads as "the
  // contracts we have" instead of a wall of ones.
  const channels = data?.data?.channels
  useEffect(() => {
    if (!channels) return
    setDrafts(
      Object.fromEntries(
        channels
          .filter((row) => row.configured)
          .map((row) => [String(row.id), String(row.cost_ratio)])
      )
    )
  }, [channels])

  const mutation = useMutation({
    mutationFn: updateChannelCost,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.errors?.join('; ') ?? response.message ?? t('Failed to save'))
        return
      }
      toast.success(response.message ?? t('Upstream costs saved'))
      queryClient.invalidateQueries({ queryKey: ['channel-cost'] })
      // Upstream cost never changes a customer price, but it does change the
      // cost basis that the reconciliation report and the pricing payload
      // publish alongside it.
      queryClient.invalidateQueries({ queryKey: ['pricing'] })
    },
    onError: (err: Error) => toast.error(err.message),
  })

  const rows = channels ?? []
  const invalid = useMemo(() => invalidCostChannels(drafts), [drafts])

  const handleSave = useCallback(() => {
    if (invalid.length > 0) {
      toast.error(t('Invalid cost ratio for channel: ') + invalid.join(', '))
      return
    }
    mutation.mutate(costPayload(drafts))
  }, [drafts, invalid, mutation, t])

  const uncataloguedTotal = rows.reduce(
    (sum, row) => sum + row.uncatalogued_count,
    0
  )

  if (isLoading) return <div className='p-4 text-sm'>{t('Loading...')}</div>
  if (isError) {
    return (
      <Alert variant='destructive'>
        <AlertDescription>{(error as Error)?.message}</AlertDescription>
      </Alert>
    )
  }

  return (
    <div className='flex min-h-0 flex-col gap-4'>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'What our upstream charges us, as a multiplier on the vendor official list price. Used for reconciliation only -- it never changes what customers are billed. A channel left blank is costed at list price, which understates margin rather than overstating it.'
          )}
        </AlertDescription>
      </Alert>

      {/*
        Selling at list while buying at list is exactly zero margin, so this is
        not a nicety -- it is the ceiling on every customer discount. Stated
        here because this is the screen where the number is known.
      */}
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'All margin comes from buying below list: at list price in and list price out, margin is exactly zero. So a customer discount can never be deeper than the purchasing discount on that vendor.'
          )}
        </AlertDescription>
      </Alert>

      {uncataloguedTotal > 0 ? (
        <Alert variant='destructive'>
          <AlertTriangle className='size-4' />
          <AlertDescription className='text-xs'>
            {t(
              'Some channels serve models that are not in the pricing catalog. Their traffic cannot be costed, so margin for those channels is overstated.'
            )}
          </AlertDescription>
        </Alert>
      ) : null}

      <div className='flex flex-wrap items-center gap-2'>
        <span className='text-muted-foreground text-xs'>
          {rows.filter((row) => row.configured).length}/{rows.length}{' '}
          {t('channels have a negotiated rate configured')}
        </span>
        <div className='ml-auto flex gap-2'>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={() => setDrafts({})}
            disabled={mutation.isPending}
          >
            <RotateCcw className='size-4' />
            {t('Reset all to list price')}
          </Button>
          <Button
            type='button'
            size='sm'
            onClick={handleSave}
            disabled={mutation.isPending || invalid.length > 0}
          >
            <Save className='size-4' />
            {t('Save upstream costs')}
          </Button>
        </div>
      </div>

      <div className='max-h-[60vh] min-h-0 flex-1 overflow-auto rounded-md border'>
        <Table>
          <TableHeader className='bg-background sticky top-0 z-10'>
            <TableRow>
              <TableHead>{t('Channel')}</TableHead>
              <TableHead>{t('Vendor')}</TableHead>
              <TableHead className='text-right'>{t('Models')}</TableHead>
              <TableHead className='w-32'>{t('Cost ratio')}</TableHead>
              <TableHead>{t('Purchasing discount')}</TableHead>
              <TableHead className='text-right'>
                {t('Max safe customer discount')}
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => (
              <ChannelCostRowView
                key={row.id}
                row={row}
                draft={drafts[String(row.id)] ?? ''}
                onChange={(value) =>
                  setDrafts((previous) => ({
                    ...previous,
                    [String(row.id)]: value,
                  }))
                }
              />
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}

/** renderCostLabel localises the purchasing position. */
function renderCostLabel(
  t: (key: string, options?: Record<string, unknown>) => string,
  ratio: number
): string {
  const label = costLabel(ratio)
  switch (label.kind) {
    case 'list':
      return t('list price')
    case 'above-list':
      return t('paying {{percent}}% ABOVE list', {
        percent: label.percent.toFixed(0),
      })
    default:
      return t('{{percent}}% off list', { percent: label.percent.toFixed(0) })
  }
}

type ChannelCostRowViewProps = {
  row: ChannelCostRow
  draft: string
  onChange: (value: string) => void
}

function ChannelCostRowView({ row, draft, onChange }: ChannelCostRowViewProps) {
  const { t } = useTranslation()
  const parsed = parseCostRatio(draft)
  const invalid = Number.isNaN(parsed)
  const ratio = invalid || parsed === null ? 1 : parsed

  return (
    <TableRow className={row.status !== 1 ? 'opacity-50' : undefined}>
      <TableCell className='text-xs'>
        <span className='font-mono'>#{row.id}</span> {row.name}
        {row.status !== 1 ? (
          <Badge variant='outline' className='ml-2 text-[10px]'>
            {t('disabled')}
          </Badge>
        ) : null}
      </TableCell>
      <TableCell className='text-xs'>
        {row.vendors.length > 0 ? row.vendors.join(', ') : '—'}
      </TableCell>
      <TableCell className='text-right text-xs'>
        {row.model_count}
        {row.uncatalogued_count > 0 ? (
          <Tooltip>
            {/* This codebase's TooltipTrigger takes `render`, not `asChild`. */}
            <TooltipTrigger
              render={
                <Badge
                  variant='outline'
                  className='text-destructive ml-2 gap-1 text-[10px]'
                />
              }
            >
              <AlertTriangle className='size-3' />
              {row.uncatalogued_count}
            </TooltipTrigger>
            <TooltipContent className='max-w-sm text-xs'>
              {t('Not in the pricing catalog, so not costable:')}{' '}
              {(row.uncatalogued_models ?? []).join(', ')}
            </TooltipContent>
          </Tooltip>
        ) : null}
      </TableCell>
      <TableCell>
        <Input
          value={draft}
          onChange={(event) => onChange(event.target.value)}
          placeholder='1.0'
          aria-invalid={invalid}
          className={`h-8 font-mono text-xs ${invalid ? 'border-destructive' : ''}`}
        />
      </TableCell>
      <TableCell
        className={`text-xs ${ratio > 1 ? 'text-destructive font-medium' : 'text-muted-foreground'}`}
      >
        {invalid ? t('must be between 0 and 5') : renderCostLabel(t, ratio)}
      </TableCell>
      <TableCell className='text-right font-mono text-xs'>
        {invalid ? '—' : maxSafeDiscount(ratio).toFixed(4)}
      </TableCell>
    </TableRow>
  )
}
