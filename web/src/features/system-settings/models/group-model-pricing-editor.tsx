import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, RotateCcw, Save } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
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
  effectiveCustomerMultiplier,
  fallbackCustomerMultiplier,
  formatCustomerMultiplier,
  invalidCustomerModelPrices,
  mergeCustomerModelDrafts,
  priceAtMultiplier,
  visibleCustomerModelNames,
  visibleCustomerPricingGroups,
} from './group-model-pricing-logic'

type DraftsByGroup = Record<string, Record<string, string>>

type GroupModelPricingEditorProps = {
  draftGroupRatios?: Record<string, number>
}

export function GroupModelPricingEditor({
  draftGroupRatios,
}: GroupModelPricingEditorProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data, isLoading, isError } = useQuery({
    queryKey: ['group-model-pricing'],
    queryFn: getGroupModelPricing,
  })
  const [drafts, setDrafts] = useState<DraftsByGroup>({})
  const dirtyGroups = useRef(new Set<string>())
  const [modelFilters, setModelFilters] = useState<Record<string, string>>({})

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
  const modelNames = useMemo(() => models.map((model) => model.model), [models])
  const byName = useMemo(
    () => new Map(models.map((model) => [model.model, model])),
    [models]
  )
  const savedGroups = useMemo(
    () => data?.data?.groups ?? [],
    [data?.data?.groups]
  )
  const savedGroupSet = useMemo(() => new Set(savedGroups), [savedGroups])
  const groups = useMemo(
    () => visibleCustomerPricingGroups(savedGroups, draftGroupRatios),
    [draftGroupRatios, savedGroups]
  )
  const effectiveGroupRatios = useMemo(
    () => ({
      ...data?.data?.group_ratios,
      ...draftGroupRatios,
    }),
    [data?.data?.group_ratios, draftGroupRatios]
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
            'Every available model starts at its inherited default multiplier. Edit only customer-specific exceptions; each override is a final multiplier over the official price.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='space-y-3 pt-4'>
        {groups.map((group) => {
          const groupDrafts = drafts[group] ?? {}
          const overrideCount = Object.keys(groupDrafts).length
          const isSaved = savedGroupSet.has(group)
          const visibleModelNames = visibleCustomerModelNames(
            modelNames,
            modelFilters[group] ?? ''
          )
          const invalid = invalidCustomerModelPrices(groupDrafts)
          return (
            <Collapsible
              key={group}
              defaultOpen={!isSaved || overrideCount > 0}
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
                  {modelNames.length} {t('models')}
                </Badge>
                <Badge variant={overrideCount > 0 ? 'default' : 'outline'}>
                  {overrideCount} {t('Override')}
                </Badge>
                {!isSaved && (
                  <Badge variant='warning'>{t('Save group ratios')}</Badge>
                )}
                <div className='ml-auto flex min-w-0 items-center gap-2'>
                  <Input
                    value={modelFilters[group] ?? ''}
                    onChange={(event) =>
                      setModelFilters((previous) => ({
                        ...previous,
                        [group]: event.target.value,
                      }))
                    }
                    placeholder={t('Search models')}
                    className='h-8 w-64'
                  />
                </div>
              </div>
              <CollapsibleContent>
                {!isSaved && (
                  <p className='text-muted-foreground border-t px-4 py-3 text-sm'>
                    {t(
                      'This pricing group is not saved yet. Save group ratios above before saving customer model overrides.'
                    )}
                  </p>
                )}
                {visibleModelNames.length === 0 ? (
                  <p className='text-muted-foreground border-t p-6 text-center text-sm'>
                    {t('No models found')}
                  </p>
                ) : (
                  <CustomerPriceTable
                    group={group}
                    modelNames={visibleModelNames}
                    byName={byName}
                    drafts={groupDrafts}
                    modelDiscounts={data?.data?.fallback_discounts ?? {}}
                    groupRatios={effectiveGroupRatios}
                    onChange={(model, value) =>
                      setDrafts((previous) => {
                        dirtyGroups.current.add(group)
                        return {
                          ...previous,
                          [group]: { ...previous[group], [model]: value },
                        }
                      })
                    }
                    onReset={(model) =>
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
                    disabled={
                      !isSaved || mutation.isPending || invalid.length > 0
                    }
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
  modelDiscounts: Record<string, number>
  groupRatios: Record<string, number>
  onChange: (model: string, value: string) => void
  onReset: (model: string) => void
}

export function CustomerPriceTable({
  group,
  modelNames,
  byName,
  drafts,
  modelDiscounts,
  groupRatios,
  onChange,
  onReset,
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
            const overridden = Object.hasOwn(drafts, modelName)
            const effective = effectiveCustomerMultiplier(
              modelName,
              group,
              drafts,
              modelDiscounts,
              groupRatios
            )
            const effectiveForPrice =
              typeof effective === 'number' && !Number.isNaN(effective)
                ? effective
                : 1
            const invalid = effectiveForPrice !== effective
            const displayedMultiplier = overridden
              ? (drafts[modelName] ?? '')
              : formatCustomerMultiplier(
                  fallbackCustomerMultiplier(
                    modelName,
                    group,
                    modelDiscounts,
                    groupRatios
                  )
                )
            const price = priceAtMultiplier(
              model.official_input_usd,
              model.official_output_usd,
              effectiveForPrice
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
                    value={displayedMultiplier}
                    onChange={(event) =>
                      onChange(modelName, event.target.value)
                    }
                    aria-invalid={invalid}
                    className='h-8 font-mono text-xs'
                  />
                  <span className='text-muted-foreground text-[10px]'>
                    {overridden ? t('Override') : t('default')}
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
                    onClick={() => onReset(modelName)}
                    disabled={!overridden}
                    aria-label={t('Reset to default')}
                  >
                    <RotateCcw className='size-4' />
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
