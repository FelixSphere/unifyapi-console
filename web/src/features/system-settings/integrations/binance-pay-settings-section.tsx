/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery } from '@tanstack/react-query'
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  PlugZap,
  XCircle,
} from 'lucide-react'
import { useState } from 'react'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { cn } from '@/lib/utils'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  getBinancePayStatus,
  testBinancePayAccount,
  type BinancePayAccountStatus,
} from './binance-pay-api'
import {
  depositAddressesToLines,
  parseDepositAddressLines,
} from './binance-pay-deposit-addresses'

// ============================================================================
// Binance Pay (personal accounts) gateway settings
// ============================================================================

/**
 * Option keys. The binance.com account keeps the original names so an
 * existing deployment's configuration survives; Binance.US has its own set.
 * API keys and secrets are redacted by GET /api/option, so the form never
 * knows the stored value: an empty field means "keep what is stored", and
 * whether a usable key is stored comes from the status endpoint.
 */
export interface BinancePaySettingsValues {
  BinancePayEnabled: boolean
  BinancePayReceiverId: string
  BinancePayReceiverNickname: string
  /** JSON: [{"network":"TRX","address":"T..."}] */
  BinancePayDepositAddresses: string
  BinancePayUSEnabled: boolean
  BinancePayUSReceiverNickname: string
  BinancePayUSDepositAddresses: string
  BinancePayCurrency: string
  BinancePayUnitPrice: number
  BinancePayMinTopUp: number
  BinancePayOrderTTLMinutes: number
  BinancePayRecommendForPartners: boolean
  BinancePayOverpayTolerancePercent: number
}

const keyShape = z
  .string()
  .trim()
  .refine((v) => v === '' || /^[A-Za-z0-9]{64}$/.test(v), {
    message: 'A Binance API key or secret is exactly 64 letters and digits',
  })

const addressLines = z
  .string()
  .refine((v) => parseDepositAddressLines(v).invalidLine === null, {
    message: 'Use "NETWORK address" per line, e.g. TRX TXYZ…',
  })

const schema = z.object({
  // binance.com
  BinancePayEnabled: z.boolean(),
  BinancePayApiKey: keyShape,
  BinancePaySecretKey: keyShape,
  BinancePayReceiverId: z
    .string()
    .trim()
    .refine((v) => v === '' || /^[0-9]{6,20}$/.test(v), {
      message: 'A Pay ID (Binance UID) is 6 to 20 digits',
    }),
  BinancePayReceiverNickname: z.string().trim(),
  BinancePayDepositAddressesText: addressLines,
  // Binance.US
  BinancePayUSEnabled: z.boolean(),
  BinancePayUSApiKey: keyShape,
  BinancePayUSSecretKey: keyShape,
  BinancePayUSReceiverNickname: z.string().trim(),
  BinancePayUSDepositAddressesText: addressLines,
  // shared
  BinancePayCurrency: z
    .string()
    .trim()
    .min(2)
    .max(10)
    .regex(/^[A-Za-z0-9]+$/, 'Asset code only, e.g. USDT'),
  BinancePayUnitPrice: z.coerce.number().positive(),
  BinancePayMinTopUp: z.coerce.number().int().min(1),
  BinancePayOrderTTLMinutes: z.coerce.number().int().min(5).max(1440),
  BinancePayRecommendForPartners: z.boolean(),
  BinancePayOverpayTolerancePercent: z.coerce.number().min(0).max(50),
})

type Values = z.infer<typeof schema>

function addressesJson(text: string): string {
  return JSON.stringify(parseDepositAddressLines(text).addresses)
}

function normalisedStoredAddresses(json: string): string {
  return addressesJson(depositAddressesToLines(json))
}

function AccountStatusLine({
  status,
  loading,
}: {
  status?: BinancePayAccountStatus
  loading: boolean
}) {
  const { t } = useTranslation()
  if (loading && !status) {
    return (
      <div className='text-muted-foreground flex items-center gap-2 text-xs'>
        <Loader2 className='h-3.5 w-3.5 animate-spin' />
        {t('Reading account status…')}
      </div>
    )
  }
  if (!status) return null

  const check = status.last_check
  return (
    <div className='space-y-1 rounded-md border p-3 text-xs'>
      <div className='flex flex-wrap items-center gap-2'>
        {status.configured ? (
          <CheckCircle2 className='h-3.5 w-3.5 text-green-600' />
        ) : (
          <XCircle className='text-muted-foreground h-3.5 w-3.5' />
        )}
        <span className='font-medium'>
          {status.configured
            ? t('Live on the wallet page')
            : t('Not offered to customers')}
        </span>
        {!status.configured && status.configured_reason && (
          <span className='text-muted-foreground'>
            — {t(status.configured_reason)}
          </span>
        )}
      </div>
      <div className='text-muted-foreground grid grid-cols-2 gap-x-4 gap-y-0.5 sm:grid-cols-4'>
        <span>
          {t('API key')}:{' '}
          {status.has_credentials
            ? t('stored, 64 chars')
            : status.api_key_length > 0
              ? t('stored but not a valid key ({{n}} chars)', {
                  n: status.api_key_length,
                })
              : t('not set')}
        </span>
        {status.supports_pay && (
          <span>
            {t('Pay ID')}: {status.pay_id || t('not set')}
            {status.pay_id && !status.pay_id_valid && (
              <span className='text-red-600'> ({t('invalid')})</span>
            )}
          </span>
        )}
        <span>
          {t('Deposit addresses')}: {status.address_count}
        </span>
        <span>
          {t('Pending orders')}: {status.pending_orders}
        </span>
      </div>
      {check && (
        <div
          className={cn(
            'flex items-start gap-2 pt-1',
            check.ok ? 'text-green-700 dark:text-green-400' : 'text-red-600'
          )}
        >
          {check.ok ? (
            <CheckCircle2 className='mt-0.5 h-3.5 w-3.5 shrink-0' />
          ) : (
            <AlertTriangle className='mt-0.5 h-3.5 w-3.5 shrink-0' />
          )}
          <span className='break-all'>
            {check.ok
              ? t('Last read from Binance succeeded at {{time}}', {
                  time: new Date(check.checked_at * 1000).toLocaleTimeString(),
                })
              : t('Last read from Binance failed at {{time}}: {{error}}', {
                  time: new Date(check.checked_at * 1000).toLocaleTimeString(),
                  error: check.error,
                })}
          </span>
        </div>
      )}
    </div>
  )
}

export function BinancePaySettingsSection({
  defaultValues,
}: {
  defaultValues: BinancePaySettingsValues
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const statusQuery = useQuery({
    queryKey: ['binance-pay-status'],
    queryFn: getBinancePayStatus,
    refetchInterval: 30_000,
  })
  const [testing, setTesting] = useState<string | null>(null)

  const statusFor = (platform: string) =>
    statusQuery.data?.accounts.find((a) => a.platform === platform)

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: {
      BinancePayEnabled: defaultValues.BinancePayEnabled,
      BinancePayApiKey: '',
      BinancePaySecretKey: '',
      BinancePayReceiverId: defaultValues.BinancePayReceiverId,
      BinancePayReceiverNickname: defaultValues.BinancePayReceiverNickname,
      BinancePayDepositAddressesText: depositAddressesToLines(
        defaultValues.BinancePayDepositAddresses
      ),
      BinancePayUSEnabled: defaultValues.BinancePayUSEnabled,
      BinancePayUSApiKey: '',
      BinancePayUSSecretKey: '',
      BinancePayUSReceiverNickname: defaultValues.BinancePayUSReceiverNickname,
      BinancePayUSDepositAddressesText: depositAddressesToLines(
        defaultValues.BinancePayUSDepositAddresses
      ),
      BinancePayCurrency: defaultValues.BinancePayCurrency || 'USDT',
      BinancePayUnitPrice: defaultValues.BinancePayUnitPrice || 1,
      BinancePayMinTopUp: defaultValues.BinancePayMinTopUp || 1,
      BinancePayOrderTTLMinutes: defaultValues.BinancePayOrderTTLMinutes || 60,
      BinancePayRecommendForPartners:
        defaultValues.BinancePayRecommendForPartners,
      BinancePayOverpayTolerancePercent:
        defaultValues.BinancePayOverpayTolerancePercent ?? 5,
    },
  })

  const { isDirty, isSubmitting } = form.formState

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []
    const push = (
      key: keyof BinancePaySettingsValues,
      next: string,
      previous: string | number | boolean
    ) => {
      if (String(previous) !== next) updates.push({ key, value: next })
    }
    // Secrets: only sent when the operator typed a new one.
    const pushSecret = (key: string, next: string) => {
      if (next.trim()) updates.push({ key, value: next.trim() })
    }

    push(
      'BinancePayEnabled',
      String(values.BinancePayEnabled),
      defaultValues.BinancePayEnabled
    )
    pushSecret('BinancePayApiKey', values.BinancePayApiKey)
    pushSecret('BinancePaySecretKey', values.BinancePaySecretKey)
    push(
      'BinancePayReceiverId',
      values.BinancePayReceiverId,
      defaultValues.BinancePayReceiverId
    )
    push(
      'BinancePayReceiverNickname',
      values.BinancePayReceiverNickname,
      defaultValues.BinancePayReceiverNickname
    )
    if (
      addressesJson(values.BinancePayDepositAddressesText) !==
      normalisedStoredAddresses(defaultValues.BinancePayDepositAddresses)
    ) {
      updates.push({
        key: 'BinancePayDepositAddresses',
        value: addressesJson(values.BinancePayDepositAddressesText),
      })
    }

    push(
      'BinancePayUSEnabled',
      String(values.BinancePayUSEnabled),
      defaultValues.BinancePayUSEnabled
    )
    pushSecret('BinancePayUSApiKey', values.BinancePayUSApiKey)
    pushSecret('BinancePayUSSecretKey', values.BinancePayUSSecretKey)
    push(
      'BinancePayUSReceiverNickname',
      values.BinancePayUSReceiverNickname,
      defaultValues.BinancePayUSReceiverNickname
    )
    if (
      addressesJson(values.BinancePayUSDepositAddressesText) !==
      normalisedStoredAddresses(defaultValues.BinancePayUSDepositAddresses)
    ) {
      updates.push({
        key: 'BinancePayUSDepositAddresses',
        value: addressesJson(values.BinancePayUSDepositAddressesText),
      })
    }

    push(
      'BinancePayCurrency',
      values.BinancePayCurrency.toUpperCase(),
      defaultValues.BinancePayCurrency
    )
    push(
      'BinancePayUnitPrice',
      String(values.BinancePayUnitPrice),
      defaultValues.BinancePayUnitPrice
    )
    push(
      'BinancePayMinTopUp',
      String(values.BinancePayMinTopUp),
      defaultValues.BinancePayMinTopUp
    )
    push(
      'BinancePayOrderTTLMinutes',
      String(values.BinancePayOrderTTLMinutes),
      defaultValues.BinancePayOrderTTLMinutes
    )
    push(
      'BinancePayRecommendForPartners',
      String(values.BinancePayRecommendForPartners),
      defaultValues.BinancePayRecommendForPartners
    )
    push(
      'BinancePayOverpayTolerancePercent',
      String(values.BinancePayOverpayTolerancePercent),
      defaultValues.BinancePayOverpayTolerancePercent
    )

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }
    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
    toast.success(t('Binance Pay settings saved'))
    form.reset({
      ...values,
      BinancePayApiKey: '',
      BinancePaySecretKey: '',
      BinancePayUSApiKey: '',
      BinancePayUSSecretKey: '',
    })
    void statusQuery.refetch()
  }

  async function handleTest(platform: string) {
    setTesting(platform)
    try {
      const response = await testBinancePayAccount(platform)
      if (response.success) {
        const d = response.data
        toast.success(
          t('Connected. {{deposits}} deposits in the last 24h{{pay}}.', {
            deposits: d?.deposits_24h ?? 0,
            pay:
              d?.pay_transactions_24h !== undefined
                ? t(', {{n}} Binance Pay transfers', {
                    n: d.pay_transactions_24h,
                  })
                : '',
          })
        )
      } else {
        toast.error(response.message || t('Connection test failed'))
      }
    } catch {
      toast.error(t('Connection test failed'))
    } finally {
      setTesting(null)
      void statusQuery.refetch()
    }
  }

  const renderAccountCard = (opts: {
    platform: 'binance.com' | 'binance.us'
    title: string
    intro: string
    enabledField: 'BinancePayEnabled' | 'BinancePayUSEnabled'
    apiKeyField: 'BinancePayApiKey' | 'BinancePayUSApiKey'
    secretField: 'BinancePaySecretKey' | 'BinancePayUSSecretKey'
    nicknameField: 'BinancePayReceiverNickname' | 'BinancePayUSReceiverNickname'
    addressesField:
      | 'BinancePayDepositAddressesText'
      | 'BinancePayUSDepositAddressesText'
    showPayId: boolean
  }) => {
    const status = statusFor(opts.platform)
    return (
      <div className='space-y-4 rounded-lg border p-4'>
        <div className='flex flex-wrap items-start justify-between gap-2'>
          <div>
            <h4 className='font-medium'>{opts.title}</h4>
            <p className='text-muted-foreground text-xs'>{opts.intro}</p>
          </div>
          <Button
            type='button'
            variant='outline'
            size='sm'
            className='gap-2'
            disabled={testing !== null || !status?.has_credentials}
            title={
              status && !status.has_credentials
                ? t('Save a valid API key and secret first')
                : undefined
            }
            onClick={() => void handleTest(opts.platform)}
          >
            {testing === opts.platform ? (
              <Loader2 className='h-4 w-4 animate-spin' />
            ) : (
              <PlugZap className='h-4 w-4' />
            )}
            {t('Test connection')}
          </Button>
        </div>

        <AccountStatusLine status={status} loading={statusQuery.isLoading} />

        <FormField
          control={form.control}
          name={opts.enabledField}
          render={({ field }) => (
            <SettingsSwitchItem>
              <SettingsSwitchContent>
                <FormLabel>{t('Enable this account')}</FormLabel>
                <FormDescription>
                  {t(
                    'Offered on the wallet page only once a valid key, secret and a way to pay are stored.'
                  )}
                </FormDescription>
              </SettingsSwitchContent>
              <FormControl>
                <Switch
                  checked={field.value}
                  onCheckedChange={field.onChange}
                />
              </FormControl>
            </SettingsSwitchItem>
          )}
        />

        <div className='grid gap-4 sm:grid-cols-2'>
          <FormField
            control={form.control}
            name={opts.apiKeyField}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API key (read-only permission)')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='off'
                    placeholder={
                      status?.has_credentials
                        ? t('Stored. Paste a new key to replace it.')
                        : t('64 characters from {{platform}} API Management', {
                            platform: opts.platform,
                          })
                    }
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name={opts.secretField}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('API secret')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    autoComplete='off'
                    placeholder={
                      status?.has_credentials
                        ? t('Stored. Paste a new secret to replace it.')
                        : t('Shown once when the key is created')
                    }
                    {...field}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>

        <div className='grid gap-4 sm:grid-cols-2'>
          {opts.showPayId && (
            <FormField
              control={form.control}
              name='BinancePayReceiverId'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Receiving Pay ID (Binance UID)')}</FormLabel>
                  <FormControl>
                    <Input placeholder='34355667' {...field} />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Shown to payers as the recipient, and used to make sure a matched transfer was received by this account.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          )}
          <FormField
            control={form.control}
            name={opts.nicknameField}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Recipient display name')}</FormLabel>
                <FormControl>
                  <Input {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'Optional. The name Binance shows for your account, so payers can verify the recipient.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>

        <FormField
          control={form.control}
          name={opts.addressesField}
          render={({ field }) => (
            <FormItem>
              <FormLabel>
                {opts.showPayId
                  ? t('On-chain deposit addresses (optional)')
                  : t('On-chain deposit addresses (required)')}
              </FormLabel>
              <FormControl>
                <Textarea
                  rows={3}
                  placeholder={'TRX TXYZ…\nBSC 0x…'}
                  className='font-mono text-xs'
                  {...field}
                />
              </FormControl>
              <FormDescription>
                {t(
                  'One per line as "NETWORK address", copied from Deposit in this account\'s wallet for the asset below. The arriving amount must match the order.'
                )}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      </div>
    )
  }

  return (
    <SettingsSection title={t('Binance Pay (personal accounts)')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty}
            saveLabel='Save Binance Pay settings'
          />

          <Alert>
            <AlertDescription className='text-xs'>
              {t(
                'Customers transfer a stablecoin to your own Binance account and the console confirms it by reading that account’s history with a read-only API key (grant "Enable Reading" only). Binance (binance.com) and Binance.US are separate companies with separate accounts, keys and customers, so each is configured and offered on its own; a customer picks the one where they hold an account. Each order gets a unique amount; overpayment within the tolerance is accepted, anything else waits for you to match it.'
              )}
            </AlertDescription>
          </Alert>

          {statusQuery.data && !statusQuery.data.compliance_confirmed && (
            <Alert>
              <AlertTriangle className='h-4 w-4' />
              <AlertDescription className='text-xs'>
                {t(
                  'Payment compliance terms are not confirmed yet (Payment Gateway section). Until then no gateway is shown to customers.'
                )}
              </AlertDescription>
            </Alert>
          )}

          {renderAccountCard({
            platform: 'binance.com',
            title: t('Binance (binance.com)'),
            intro: t(
              'Global Binance. Customers pay by Binance Pay to your Pay ID (free, instant) or on-chain.'
            ),
            enabledField: 'BinancePayEnabled',
            apiKeyField: 'BinancePayApiKey',
            secretField: 'BinancePaySecretKey',
            nicknameField: 'BinancePayReceiverNickname',
            addressesField: 'BinancePayDepositAddressesText',
            showPayId: true,
          })}

          {renderAccountCard({
            platform: 'binance.us',
            title: t('Binance.US'),
            intro: t(
              'US entity, separate from binance.com. No Binance Pay: customers pay on-chain to your deposit address.'
            ),
            enabledField: 'BinancePayUSEnabled',
            apiKeyField: 'BinancePayUSApiKey',
            secretField: 'BinancePayUSSecretKey',
            nicknameField: 'BinancePayUSReceiverNickname',
            addressesField: 'BinancePayUSDepositAddressesText',
            showPayId: false,
          })}

          <div className='space-y-4 rounded-lg border p-4'>
            <h4 className='font-medium'>{t('Shared settings')}</h4>
            <FormField
              control={form.control}
              name='BinancePayRecommendForPartners'
              render={({ field }) => (
                <SettingsSwitchItem>
                  <SettingsSwitchContent>
                    <FormLabel>
                      {t('Recommend to partnership customers')}
                    </FormLabel>
                    <FormDescription>
                      {t(
                        'Users in a Partnership Program customer group see the Binance options first, marked as recommended.'
                      )}
                    </FormDescription>
                  </SettingsSwitchContent>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </SettingsSwitchItem>
              )}
            />
            <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-5'>
              <FormField
                control={form.control}
                name='BinancePayCurrency'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Asset')}</FormLabel>
                    <FormControl>
                      <Input
                        placeholder='USDT'
                        {...field}
                        onChange={(event) =>
                          field.onChange(event.target.value.toUpperCase())
                        }
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='BinancePayUnitPrice'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Price per 1 USD of credit')}</FormLabel>
                    <FormControl>
                      <Input type='number' step='0.01' min={0} {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='BinancePayMinTopUp'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Minimum top-up quantity')}</FormLabel>
                    <FormControl>
                      <Input type='number' min={1} step={1} {...field} />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='BinancePayOrderTTLMinutes'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Order expires after (minutes)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={5}
                        max={1440}
                        step={1}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />
              <FormField
                control={form.control}
                name='BinancePayOverpayTolerancePercent'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Accept overpayment up to (%)')}</FormLabel>
                    <FormControl>
                      <Input
                        type='number'
                        min={0}
                        max={50}
                        step='0.5'
                        {...field}
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Credited automatically when only one pending order fits. Short payments always wait for you. 0 disables.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </div>
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
