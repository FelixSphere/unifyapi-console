/*
UNIFYAPI-FORK: first-class company x model pricing and route binding.

The admin chooses the business objects they actually think in -- company,
model, price multiplier and dedicated upstream key/channel. No hidden user
groups or route-group naming convention leaks into this screen.
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CircleAlert, Save, Trash2 } from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  deleteCustomerModelContract,
  getCustomerModelContracts,
  updateCustomerModelContractMode,
  upsertCustomerModelContract,
} from '../api'
import { SettingsSection } from '../components/settings-section'
import {
  contractFor,
  contractPrice,
  eligibleContractChannels,
  formatContractUSD,
  validateContractDraft,
} from './customer-model-contract-logic'

export function CustomerModelContractsSection() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['customer-model-contracts'],
    queryFn: getCustomerModelContracts,
  })

  const tenants = useMemo(
    () => data?.data?.tenants ?? [],
    [data?.data?.tenants]
  )
  const models = useMemo(() => data?.data?.models ?? [], [data?.data?.models])
  const channels = useMemo(
    () => data?.data?.channels ?? [],
    [data?.data?.channels]
  )
  const contracts = useMemo(
    () => data?.data?.contracts ?? [],
    [data?.data?.contracts]
  )

  const [tenantId, setTenantId] = useState(0)
  const [model, setModel] = useState('')
  const [discountDraft, setDiscountDraft] = useState('1')
  const [channelIds, setChannelIds] = useState<number[]>([])
  const [enabled, setEnabled] = useState(true)

  useEffect(() => {
    if (tenantId === 0 && tenants.length > 0) setTenantId(tenants[0].id)
  }, [tenantId, tenants])
  useEffect(() => {
    if (model === '' && models.length > 0) setModel(models[0].model)
  }, [model, models])

  const selectedContract = useMemo(
    () => contractFor(contracts, tenantId, model),
    [contracts, tenantId, model]
  )
  const selectedTenant = tenants.find((tenant) => tenant.id === tenantId)

  useEffect(() => {
    setDiscountDraft(String(selectedContract?.discount ?? 1))
    setChannelIds(selectedContract?.channel_ids ?? [])
    setEnabled(selectedContract?.enabled ?? true)
  }, [tenantId, model, selectedContract])

  const selectedModel = models.find((entry) => entry.model === model)
  const discount = Number(discountDraft)
  const finalPrice = contractPrice(selectedModel, discount)
  const eligibleChannels = useMemo(
    () => eligibleContractChannels(channels, model, selectedContract?.id ?? 0),
    [channels, model, selectedContract?.id]
  )
  const channelById = useMemo(
    () => new Map(channels.map((channel) => [channel.id, channel])),
    [channels]
  )
  const problems = validateContractDraft({
    tenantId,
    model,
    discount,
    channelIds,
    enabled,
  })

  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ['customer-model-contracts'] })
    queryClient.invalidateQueries({ queryKey: ['pricing'] })
  }
  const saveMutation = useMutation({
    mutationFn: upsertCustomerModelContract,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.errors?.join('; ') ?? response.message)
        return
      }
      toast.success(response.message)
      refresh()
    },
    onError: (mutationError: Error) => toast.error(mutationError.message),
  })
  const deleteMutation = useMutation({
    mutationFn: deleteCustomerModelContract,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.message)
        return
      }
      toast.success(response.message)
      refresh()
    },
    onError: (mutationError: Error) => toast.error(mutationError.message),
  })
  const modeMutation = useMutation({
    mutationFn: updateCustomerModelContractMode,
    onSuccess: (response) => {
      if (!response.success) {
        toast.error(response.message)
        return
      }
      toast.success(response.message)
      refresh()
    },
    onError: (mutationError: Error) => toast.error(mutationError.message),
  })

  const selectedTenantContracts = contracts.filter(
    (contract) => contract.tenant_id === tenantId
  )

  if (isLoading) return <div className='p-4 text-sm'>{t('Loading...')}</div>
  if (isError) {
    return (
      <Alert variant='destructive'>
        <AlertDescription>{(error as Error).message}</AlertDescription>
      </Alert>
    )
  }

  return (
    <SettingsSection title={t('Customer model pricing')}>
      <Alert>
        <AlertDescription className='text-xs'>
          {t(
            'One company can have a different price and upstream key for every model. Customer price = official model price x this contract discount. Global model discounts and legacy group ratios are not applied again.'
          )}
        </AlertDescription>
      </Alert>

      <div className='grid gap-4 rounded-lg border p-4 lg:grid-cols-2'>
        <div className='grid gap-1.5'>
          <Label>{t('Customer company')}</Label>
          <Select
            items={tenants.map((tenant) => ({
              value: String(tenant.id),
              label: `${tenant.name} (${tenant.slug})`,
            }))}
            value={tenantId > 0 ? String(tenantId) : null}
            onValueChange={(value) =>
              value !== null && setTenantId(Number(value))
            }
          >
            <SelectTrigger>
              <SelectValue placeholder={t('Select a customer company')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {tenants.map((tenant) => (
                  <SelectItem key={tenant.id} value={String(tenant.id)}>
                    {tenant.name} ({tenant.slug})
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <div className='bg-muted/40 flex items-start justify-between gap-4 rounded-md p-3 lg:col-span-2'>
          <div className='grid gap-1'>
            <Label>{t('Dedicated-contract mode')}</Label>
            <p className='text-muted-foreground text-xs'>
              {t(
                'When enabled, this company can see and call only models with an enabled customer-model contract. Missing contracts fail closed instead of using a generic channel.'
              )}
            </p>
          </div>
          <Switch
            checked={selectedTenant?.strict_model_contracts ?? false}
            disabled={modeMutation.isPending || !selectedTenant}
            onCheckedChange={(next) => {
              if (
                next &&
                selectedTenantContracts.filter((contract) => contract.enabled)
                  .length === 0
              ) {
                toast.error(
                  t('Create at least one enabled model contract first.')
                )
                return
              }
              if (
                next &&
                !window.confirm(
                  t(
                    'Enable dedicated-contract mode? Any model without an enabled contract will stop working for this company.'
                  )
                )
              ) {
                return
              }
              modeMutation.mutate({ tenant_id: tenantId, strict: next })
            }}
          />
        </div>

        <div className='grid gap-1.5'>
          <Label>{t('Model')}</Label>
          <Select
            items={models.map((entry) => ({
              value: entry.model,
              label: `${entry.model} · ${entry.vendor || 'manual'}`,
            }))}
            value={model || null}
            onValueChange={(value) => value !== null && setModel(value)}
          >
            <SelectTrigger>
              <SelectValue placeholder={t('Select a model')} />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                {models.map((entry) => (
                  <SelectItem key={entry.model} value={entry.model}>
                    {entry.model} · {entry.vendor || 'manual'}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>

        <div className='grid gap-1.5'>
          <Label htmlFor='customer-model-discount'>
            {t('Contract discount')}
          </Label>
          <Input
            id='customer-model-discount'
            type='number'
            min='0.000001'
            max='10'
            step='0.01'
            value={discountDraft}
            onChange={(event) => setDiscountDraft(event.target.value)}
          />
          <p className='text-muted-foreground text-xs'>
            0.8 = {t('20% off')}; 1 = {t('official price')}; 1.1 ={' '}
            {t('10% markup')}
          </p>
        </div>

        <div className='bg-muted/40 grid gap-1.5 rounded-md p-3'>
          <span className='text-muted-foreground text-xs'>
            {t('Price preview (USD / 1M tokens)')}
          </span>
          <div className='flex flex-wrap gap-4 font-mono text-sm'>
            <span>
              {t('Official')}:{' '}
              {selectedModel
                ? formatContractUSD(selectedModel.official_input_usd)
                : '—'}{' '}
              /{' '}
              {selectedModel
                ? formatContractUSD(selectedModel.official_output_usd)
                : '—'}
            </span>
            <span className='font-semibold'>
              {t('Customer pays')}:{' '}
              {finalPrice ? formatContractUSD(finalPrice.input) : '—'} /{' '}
              {finalPrice ? formatContractUSD(finalPrice.output) : '—'}
            </span>
          </div>
        </div>

        <div className='grid gap-2 lg:col-span-2'>
          <div className='flex items-center justify-between'>
            <div>
              <Label>{t('Dedicated upstream channels')}</Label>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Only enabled channels containing exactly this one model are selectable.'
                )}
              </p>
            </div>
            <div className='flex items-center gap-2'>
              <Switch checked={enabled} onCheckedChange={setEnabled} />
              <span className='text-sm'>
                {enabled ? t('Enabled') : t('Disabled')}
              </span>
            </div>
          </div>

          {eligibleChannels.length === 0 ? (
            <Alert variant='destructive'>
              <CircleAlert className='size-4' />
              <AlertTitle>{t('No dedicated channel is ready')}</AlertTitle>
              <AlertDescription className='text-xs'>
                {t(
                  "Go to Channels, create a channel that contains only {{model}}, enter this customer's upstream key, test it, then return here.",
                  { model }
                )}{' '}
                <a className='underline' href='/channels'>
                  {t('Open Channels')}
                </a>
              </AlertDescription>
            </Alert>
          ) : (
            <div className='grid gap-2 sm:grid-cols-2'>
              {eligibleChannels.map((channel) => {
                const checked = channelIds.includes(channel.id)
                return (
                  <label
                    key={channel.id}
                    className='flex cursor-pointer items-start gap-2 rounded-md border p-3'
                  >
                    <Checkbox
                      checked={checked}
                      onCheckedChange={(next) =>
                        setChannelIds((previous) =>
                          next
                            ? [...new Set([...previous, channel.id])]
                            : previous.filter((id) => id !== channel.id)
                        )
                      }
                    />
                    <span className='grid gap-0.5 text-sm'>
                      <span className='font-medium'>
                        #{channel.id} {channel.name}
                      </span>
                      <span className='text-muted-foreground text-xs'>
                        {t('Priority')} {channel.priority} · {channel.group}
                      </span>
                    </span>
                  </label>
                )
              })}
            </div>
          )}
        </div>

        {problems.length > 0 ? (
          <ul className='text-destructive list-disc pl-5 text-xs lg:col-span-2'>
            {problems.map((problem) => (
              <li key={problem}>{t(problem)}</li>
            ))}
          </ul>
        ) : null}

        <div className='flex justify-end gap-2 lg:col-span-2'>
          {selectedContract ? (
            <Button
              variant='destructive'
              onClick={() => {
                if (window.confirm(t('Delete this customer-model contract?'))) {
                  deleteMutation.mutate(selectedContract.id)
                }
              }}
              disabled={deleteMutation.isPending}
            >
              <Trash2 className='size-4' />
              {t('Delete contract')}
            </Button>
          ) : null}
          <Button
            onClick={() =>
              saveMutation.mutate({
                tenant_id: tenantId,
                model,
                discount,
                channel_ids: channelIds,
                enabled,
              })
            }
            disabled={saveMutation.isPending || problems.length > 0}
          >
            <Save className='size-4' />
            {t('Save contract')}
          </Button>
        </div>
      </div>

      <div className='overflow-auto rounded-md border'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Model')}</TableHead>
              <TableHead>{t('Discount')}</TableHead>
              <TableHead>{t('Customer price in/out')}</TableHead>
              <TableHead>{t('Channels')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead className='text-right'>{t('Action')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {selectedTenantContracts.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={6}
                  className='text-muted-foreground text-center'
                >
                  {t('No model contracts for this company yet.')}
                </TableCell>
              </TableRow>
            ) : (
              selectedTenantContracts.map((contract) => (
                <TableRow key={contract.id}>
                  <TableCell className='font-mono text-xs'>
                    {contract.model}
                  </TableCell>
                  <TableCell>{contract.discount.toFixed(4)}×</TableCell>
                  <TableCell className='font-mono text-xs'>
                    {formatContractUSD(contract.customer_input_usd)} /{' '}
                    {formatContractUSD(contract.customer_output_usd)}
                  </TableCell>
                  <TableCell>
                    {contract.channel_ids
                      .map((id) => {
                        const name = channelById.get(id)?.name
                        return name ? `#${id} ${name}` : `#${id}`
                      })
                      .join(', ')}
                  </TableCell>
                  <TableCell>
                    <Badge variant={contract.enabled ? 'default' : 'secondary'}>
                      {contract.enabled ? t('Enabled') : t('Disabled')}
                    </Badge>
                  </TableCell>
                  <TableCell className='text-right'>
                    <Button
                      variant='outline'
                      size='sm'
                      onClick={() => setModel(contract.model)}
                    >
                      {t('Edit')}
                    </Button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </SettingsSection>
  )
}
