import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, Plus, Save, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { getGroupModelPricing, updateGroupModelPricing } from '../api'
import type { GroupModelPricingModel } from '../types'
import { formatUSD } from './baseline-pricing-logic'
import {
  customerModelPricePayload,
  fallbackCustomerMultiplier,
  invalidCustomerModelPrices,
  mergeCustomerModelDrafts,
  parseCustomerMultiplier,
  priceAtMultiplier,
} from './group-model-pricing-logic'

type DraftsByGroup = Record<string, Record<string, string>>

export function GroupModelPricingEditor() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data, isLoading, isError } = useQuery({
    queryKey: ['group-model-pricing'],
    queryFn: getGroupModelPricing,
  })
  const [drafts, setDrafts] = useState<DraftsByGroup>({})
  const dirtyGroups = useRef(new Set<string>())
  const [selectedModels, setSelectedModels] = useState<Record<string, string>>(
    {}
  )

  useEffect(() => {
    if (!data?.data?.discounts) return
    setDrafts((current) =>
      mergeCustomerModelDrafts(
        data.data.discounts,
        current,
        dirtyGroups.current
      )
    )
  }, [data?.data?.discounts])

  const mutation = useMutation({
    mutationFn: ({
      group,
      values,
    }: {
      group: string
      values: Record<string, number>
    }) => updateGroupModelPricing(group, values),
    onSuccess: (response, variables) => {
      if (!response.success) {
        toast.error(
          response.errors?.join('; ') ?? response.message ?? t('Save failed')
        )
        return
      }
      dirtyGroups.current.delete(variables.group)
      setDrafts((current) => ({
        ...current,
        [variables.group]: Object.fromEntries(
          Object.entries(variables.values).map(([model, ratio]) => [
            model,
            String(ratio),
          ])
        ),
      }))
      toast.success(response.message ?? t('Saved successfully'))
      queryClient.invalidateQueries({ queryKey: ['group-model-pricing'] })
      queryClient.invalidateQueries({ queryKey: ['pricing-baseline'] })
      queryClient.invalidateQueries({ queryKey: ['pricing'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const models = useMemo(() => data?.data?.models ?? [], [data?.data?.models])
  const byName = useMemo(
    () => new Map(models.map((model) => [model.model, model])),
    [models]
  )

  const addModel = useCallback(
    (group: string) => {
      const model = selectedModels[group]
      if (!model) return
      const initial = fallbackCustomerMultiplier(
        model,
        group,
        data?.data?.fallback_discounts ?? {},
        data?.data?.group_ratios ?? {}
      )
      setDrafts((previous) => ({
        ...previous,
        [group]: { ...previous[group], [model]: String(initial) },
      }))
      dirtyGroups.current.add(group)
      setSelectedModels((previous) => ({ ...previous, [group]: '' }))
    },
    [data?.data?.fallback_discounts, data?.data?.group_ratios, selectedModels]
  )

  if (isLoading) return <div className='p-4 text-sm'>{t('Loading...')}</div>
  if (isError) {
    return (
      <div className='text-destructive p-4 text-sm'>
        {t('Failed to load data')}
      </div>
    )
  }

  return (
    <Card>
      <CardHeader className='bg-muted/20 border-b'>
        <CardTitle>{t('Customer model prices')}</CardTitle>
        <CardDescription>
          {t(
            'Set each customer group model price as a final multiplier over the official price. It replaces global model discount and group ratio for that model.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3 pt-4'>
        {(data?.data?.groups ?? []).map((group) => {
          const groupDrafts = drafts[group] ?? {}
          const configuredNames = Object.keys(groupDrafts).sort()
          const candidates = models.filter(
            (model) => !Object.hasOwn(groupDrafts, model.model)
          )
          const invalid = invalidCustomerModelPrices(groupDrafts)
          return (
            <Collapsible
              key={group}
              defaultOpen={configuredNames.length > 0}
              className='rounded-lg border'
            >
              <div className='flex items-center gap-3 px-4 py-3'>
                <CollapsibleTrigger
                  render={<Button variant='ghost' size='icon-sm' />}
                >
                  <ChevronDown className='size-4' />
                </CollapsibleTrigger>
                <strong>{group}</strong>
                <Badge variant='secondary'>
                  {configuredNames.length} {t('model prices')}
                </Badge>
                <div className='ml-auto flex min-w-0 items-center gap-2'>
                  <Select
                    value={selectedModels[group] || null}
                    onValueChange={(value) =>
                      typeof value === 'string' &&
                      setSelectedModels((previous) => ({
                        ...previous,
                        [group]: value,
                      }))
                    }
                  >
                    <SelectTrigger className='w-64'>
                      <SelectValue placeholder={t('Select a model')} />
                    </SelectTrigger>
                    <SelectContent>
                      {candidates.map((model) => (
                        <SelectItem key={model.model} value={model.model}>
                          {model.model}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() => addModel(group)}
                    disabled={!selectedModels[group]}
                  >
                    <Plus className='size-4' />
                    {t('Add model')}
                  </Button>
                </div>
              </div>
              <CollapsibleContent>
                {configuredNames.length === 0 ? (
                  <p className='text-muted-foreground border-t p-6 text-center text-sm'>
                    {t(
                      'No customer-specific model prices. Global pricing is used.'
                    )}
                  </p>
                ) : (
                  <CustomerPriceTable
                    group={group}
                    modelNames={configuredNames}
                    byName={byName}
                    drafts={groupDrafts}
                    onChange={(model, value) =>
                      setDrafts((previous) => {
                        dirtyGroups.current.add(group)
                        return {
                          ...previous,
                          [group]: { ...previous[group], [model]: value },
                        }
                      })
                    }
                    onDelete={(model) =>
                      setDrafts((previous) => {
                        dirtyGroups.current.add(group)
                        const nextGroup = { ...previous[group] }
                        delete nextGroup[model]
                        return { ...previous, [group]: nextGroup }
                      })
                    }
                  />
                )}
                <div className='flex justify-end border-t p-3'>
                  <Button
                    type='button'
                    size='sm'
                    disabled={mutation.isPending || invalid.length > 0}
                    onClick={() =>
                      mutation.mutate({
                        group,
                        values: customerModelPricePayload(groupDrafts),
                      })
                    }
                  >
                    <Save className='size-4' />
                    {t('Save customer prices')}
                  </Button>
                </div>
              </CollapsibleContent>
            </Collapsible>
          )
        })}
      </CardContent>
    </Card>
  )
}

type CustomerPriceTableProps = {
  group: string
  modelNames: string[]
  byName: Map<string, GroupModelPricingModel>
  drafts: Record<string, string>
  onChange: (model: string, value: string) => void
  onDelete: (model: string) => void
}

function CustomerPriceTable({
  modelNames,
  byName,
  drafts,
  onChange,
  onDelete,
}: CustomerPriceTableProps) {
  const { t } = useTranslation()
  return (
    <div className='max-h-[45vh] overflow-auto border-t'>
      <Table>
        <TableHeader className='bg-background sticky top-0'>
          <TableRow>
            <TableHead>{t('Model')}</TableHead>
            <TableHead>{t('Vendor')}</TableHead>
            <TableHead className='text-right'>{t('Official in/out')}</TableHead>
            <TableHead className='w-40'>{t('Final multiplier')}</TableHead>
            <TableHead className='text-right'>{t('Customer in/out')}</TableHead>
            <TableHead className='w-12' />
          </TableRow>
        </TableHeader>
        <TableBody>
          {modelNames.map((modelName) => {
            const model = byName.get(modelName)
            if (!model) return null
            const parsed = parseCustomerMultiplier(drafts[modelName] ?? '')
            const invalid = Number.isNaN(parsed)
            const price = priceAtMultiplier(
              model.official_input_usd,
              model.official_output_usd,
              invalid || parsed === null ? 1 : parsed
            )
            return (
              <TableRow key={modelName}>
                <TableCell className='font-mono text-xs'>{modelName}</TableCell>
                <TableCell className='text-muted-foreground text-xs'>
                  {model.vendor || '—'}
                </TableCell>
                <TableCell className='text-right font-mono text-xs'>
                  {formatUSD(model.official_input_usd)} /{' '}
                  {formatUSD(model.official_output_usd)}
                </TableCell>
                <TableCell>
                  <Input
                    value={drafts[modelName] ?? ''}
                    onChange={(event) =>
                      onChange(modelName, event.target.value)
                    }
                    aria-invalid={invalid}
                    className='h-8 font-mono text-xs'
                  />
                  <span className='text-muted-foreground text-[10px]'>
                    {t('1 = official price')}
                  </span>
                </TableCell>
                <TableCell className='text-right font-mono text-xs'>
                  {formatUSD(price.input)} / {formatUSD(price.output)}
                </TableCell>
                <TableCell>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => onDelete(modelName)}
                    aria-label={t('Delete')}
                  >
                    <Trash2 className='size-4' />
                  </Button>
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
