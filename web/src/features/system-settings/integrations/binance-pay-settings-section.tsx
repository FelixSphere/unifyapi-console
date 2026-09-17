/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Alert, AlertDescription } from '@/components/ui/alert'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  depositAddressesToLines,
  parseDepositAddressLines,
} from './binance-pay-deposit-addresses'

// ============================================================================
// Binance Pay (personal account) gateway settings
// ============================================================================

export interface BinancePaySettingsValues {
  BinancePayEnabled: boolean
  BinancePayApiKey: string
  BinancePaySecretKey: string
  BinancePayReceiverId: string
  BinancePayReceiverNickname: string
  BinancePayCurrency: string
  BinancePayUnitPrice: number
  BinancePayMinTopUp: number
  BinancePayOrderTTLMinutes: number
  /** JSON: [{"network":"TRX","address":"T..."}] */
  BinancePayDepositAddresses: string
  BinancePayRecommendForPartners: boolean
  /** 'binance.com' | 'binance.us' */
  BinancePayPlatform: string
  BinancePayOverpayTolerancePercent: number
}

export const BINANCE_PAY_PLATFORMS = [
  { value: 'binance.com', label: 'Binance (binance.com)' },
  { value: 'binance.us', label: 'Binance.US (binance.us)' },
] as const

const schema = z
  .object({
    BinancePayEnabled: z.boolean(),
    BinancePayApiKey: z.string(),
    BinancePaySecretKey: z.string(),
    BinancePayReceiverId: z.string().trim(),
    BinancePayReceiverNickname: z.string().trim(),
    BinancePayCurrency: z
      .string()
      .trim()
      .min(2)
      .max(10)
      .regex(/^[A-Za-z0-9]+$/, 'Asset code only, e.g. USDT'),
    BinancePayUnitPrice: z.coerce.number().positive(),
    BinancePayMinTopUp: z.coerce.number().int().min(1),
    BinancePayOrderTTLMinutes: z.coerce.number().int().min(5).max(1440),
    BinancePayDepositAddressesText: z.string(),
    BinancePayRecommendForPartners: z.boolean(),
    BinancePayPlatform: z.enum(['binance.com', 'binance.us']),
    BinancePayOverpayTolerancePercent: z.coerce.number().min(0).max(50),
  })
  .superRefine((values, ctx) => {
    if (values.BinancePayEnabled) {
      if (!values.BinancePayApiKey.trim()) {
        ctx.addIssue({
          code: 'custom',
          path: ['BinancePayApiKey'],
          message: 'Required when Binance Pay is enabled',
        })
      }
      if (!values.BinancePaySecretKey.trim()) {
        ctx.addIssue({
          code: 'custom',
          path: ['BinancePaySecretKey'],
          message: 'Required when Binance Pay is enabled',
        })
      }
      const hasAddresses =
        parseDepositAddressLines(values.BinancePayDepositAddressesText)
          .addresses.length > 0
      if (values.BinancePayPlatform === 'binance.us' && !hasAddresses) {
        ctx.addIssue({
          code: 'custom',
          path: ['BinancePayDepositAddressesText'],
          message:
            'Binance.US has no Binance Pay; at least one deposit address is required',
        })
      }
      if (
        values.BinancePayPlatform === 'binance.com' &&
        !values.BinancePayReceiverId &&
        !hasAddresses
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['BinancePayReceiverId'],
          message: 'Enter a Pay ID or at least one deposit address',
        })
      }
    }
    const { invalidLine } = parseDepositAddressLines(
      values.BinancePayDepositAddressesText
    )
    if (invalidLine) {
      ctx.addIssue({
        code: 'custom',
        path: ['BinancePayDepositAddressesText'],
        message: `Use "NETWORK address" per line; could not read: ${invalidLine}`,
      })
    }
  })

type Values = z.infer<typeof schema>

export function BinancePaySettingsSection({
  defaultValues,
}: {
  defaultValues: BinancePaySettingsValues
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    defaultValues: {
      BinancePayEnabled: defaultValues.BinancePayEnabled,
      BinancePayApiKey: defaultValues.BinancePayApiKey,
      BinancePaySecretKey: defaultValues.BinancePaySecretKey,
      BinancePayReceiverId: defaultValues.BinancePayReceiverId,
      BinancePayReceiverNickname: defaultValues.BinancePayReceiverNickname,
      BinancePayCurrency: defaultValues.BinancePayCurrency || 'USDT',
      BinancePayUnitPrice: defaultValues.BinancePayUnitPrice || 1,
      BinancePayMinTopUp: defaultValues.BinancePayMinTopUp || 1,
      BinancePayOrderTTLMinutes: defaultValues.BinancePayOrderTTLMinutes || 60,
      BinancePayDepositAddressesText: depositAddressesToLines(
        defaultValues.BinancePayDepositAddresses
      ),
      BinancePayRecommendForPartners:
        defaultValues.BinancePayRecommendForPartners,
      BinancePayPlatform:
        defaultValues.BinancePayPlatform === 'binance.us'
          ? 'binance.us'
          : 'binance.com',
      BinancePayOverpayTolerancePercent:
        defaultValues.BinancePayOverpayTolerancePercent ?? 5,
    },
  })

  const { isDirty, isSubmitting } = form.formState
  const enabled = form.watch('BinancePayEnabled')
  const platform = form.watch('BinancePayPlatform')
  const isUS = platform === 'binance.us'

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []
    const push = (key: keyof BinancePaySettingsValues, next: string) => {
      const previous = defaultValues[key]
      if (String(previous) !== next) {
        updates.push({ key, value: next })
      }
    }

    push('BinancePayEnabled', String(values.BinancePayEnabled))
    push('BinancePayApiKey', values.BinancePayApiKey.trim())
    push('BinancePaySecretKey', values.BinancePaySecretKey.trim())
    push('BinancePayReceiverId', values.BinancePayReceiverId)
    push('BinancePayReceiverNickname', values.BinancePayReceiverNickname)
    push('BinancePayCurrency', values.BinancePayCurrency.toUpperCase())
    push('BinancePayUnitPrice', String(values.BinancePayUnitPrice))
    push('BinancePayMinTopUp', String(values.BinancePayMinTopUp))
    push('BinancePayOrderTTLMinutes', String(values.BinancePayOrderTTLMinutes))
    push(
      'BinancePayRecommendForPartners',
      String(values.BinancePayRecommendForPartners)
    )
    push('BinancePayPlatform', values.BinancePayPlatform)
    push(
      'BinancePayOverpayTolerancePercent',
      String(values.BinancePayOverpayTolerancePercent)
    )

    const { addresses } = parseDepositAddressLines(
      values.BinancePayDepositAddressesText
    )
    const addressesJson = JSON.stringify(addresses)
    const previousJson = JSON.stringify(
      parseDepositAddressLines(
        depositAddressesToLines(defaultValues.BinancePayDepositAddresses)
      ).addresses
    )
    if (addressesJson !== previousJson) {
      updates.push({ key: 'BinancePayDepositAddresses', value: addressesJson })
    }

    if (updates.length === 0) {
      toast.info(t('No changes to save'))
      return
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
    toast.success(t('Binance Pay settings saved'))
    form.reset(values)
  }

  return (
    <SettingsSection title={t('Binance Pay (personal account)')}>
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
                'Customers send a stablecoin transfer to your own Binance account (Binance Pay, or on-chain to your deposit address). The console confirms payments by reading that account’s history with a read-only API key, so grant the key "Enable Reading" only — never trading or withdrawals. Each order gets a unique amount; the payer must send exactly that amount.'
              )}
            </AlertDescription>
          </Alert>

          <FormField
            control={form.control}
            name='BinancePayEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable Binance Pay')}</FormLabel>
                  <FormDescription>
                    {t(
                      'Shows Binance Pay on the wallet page once the compliance terms are confirmed and the fields below are set.'
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
                      'Users in a Partnership Program customer group see Binance Pay first, marked as recommended.'
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
              name='BinancePayPlatform'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Binance platform')}</FormLabel>
                  <Select
                    items={BINANCE_PAY_PLATFORMS.map((p) => ({
                      value: p.value,
                      label: p.label,
                    }))}
                    value={field.value}
                    onValueChange={(value) => {
                      if (value) field.onChange(value)
                    }}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {BINANCE_PAY_PLATFORMS.map((p) => (
                          <SelectItem key={p.value} value={p.value}>
                            {p.label}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {isUS
                      ? t(
                          'Binance.US is a separate company: keys from binance.us only work here, and it has no Binance Pay, so customers pay on-chain to your deposit address.'
                        )
                      : t(
                          'Keys created at binance.com. Customers can pay by Binance Pay (Pay ID) or on-chain.'
                        )}
                  </FormDescription>
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
                      'A transfer slightly above the unique amount is credited automatically when only one pending order fits. Short payments always wait for an administrator. 0 disables.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='BinancePayApiKey'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Binance API key (read-only)')}</FormLabel>
                  <FormControl>
                    <Input type='password' autoComplete='off' {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='BinancePaySecretKey'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Binance API secret')}</FormLabel>
                  <FormControl>
                    <Input type='password' autoComplete='off' {...field} />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>

          <div className='grid gap-4 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='BinancePayReceiverId'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Receiving Pay ID (Binance UID)')}</FormLabel>
                  <FormControl>
                    <Input placeholder='34355667' disabled={isUS} {...field} />
                  </FormControl>
                  <FormDescription>
                    {isUS
                      ? t('Not used on Binance.US (no Binance Pay).')
                      : t(
                          'Shown to payers as the recipient, and used to make sure a matched transfer was received by this account.'
                        )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='BinancePayReceiverNickname'
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

          <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
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
          </div>

          <FormField
            control={form.control}
            name='BinancePayDepositAddressesText'
            render={({ field }) => (
              <FormItem>
                <FormLabel>
                  {t('On-chain deposit addresses (optional)')}
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
                    'One per line as "NETWORK address", taken from Deposit in your Binance wallet. Lets payers without a Binance account pay on-chain; the arriving amount must still match exactly.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          {enabled && (
            <p className='text-muted-foreground text-xs'>
              {t(
                'Pending orders are checked against your Binance history every minute, and whenever a payer presses "I\'ve paid" (at most once every 15 seconds).'
              )}
            </p>
          )}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
