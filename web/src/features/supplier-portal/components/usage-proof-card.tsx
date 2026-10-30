/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the answer to "how do I know you are not under-reporting my
// usage?".
//
// A seller has to take our word for the revenue -- our price list, our
// customers. They do NOT have to take our word for the usage, and usage is
// what their share is computed from. Their vendor console shows tokens per day
// per model on their own account, from a source we cannot touch. So this
// reports the same shape, says plainly that it is meant to be compared, and
// hands over a CSV for diffing against a vendor export.
//
// The strongest guarantee is not on this screen at all: they hold the key.
// They can cap the spend or revoke it in their own account the moment the two
// stop agreeing. Saying so is part of the answer.

import { useQuery } from '@tanstack/react-query'
import { Download, ShieldCheck } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { formatUSD } from '@/features/system-settings/billing/credit-supply-logic'

import { getSupplierUsageDetail, supplierUsageExportUrl } from '../api'

const WINDOWS = [7, 30, 90]

export function UsageProofCard({ vendorLabel }: { vendorLabel: string }) {
  const { t } = useTranslation()
  const [days, setDays] = useState(30)
  const detail = useQuery({
    queryKey: ['supplier', 'usage-detail', days],
    queryFn: () => getSupplierUsageDetail(days),
  })
  const rows = detail.data?.rows ?? []
  const totals = detail.data?.totals

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <ShieldCheck className='size-4' />
          {t('Check our numbers against your own')}
        </CardTitle>
        <CardDescription>
          {t(
            'Your share is a share of what your credits sold for, and that starts from how much of them was used. You do not have to take our word for the usage: this is the same breakdown {{vendor}} shows you on your own account. Compare the token counts. If they ever disagree, tell us — and remember the key is yours, so you can cap or revoke it in your vendor account at any moment.',
            { vendor: vendorLabel }
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <NativeSelect
            className='w-44'
            value={String(days)}
            onChange={(event) => setDays(Number(event.target.value))}
            data-testid='usage-window'
          >
            {WINDOWS.map((window) => (
              <NativeSelectOption key={window} value={String(window)}>
                {t('Last {{count}} days', { count: window })}
              </NativeSelectOption>
            ))}
          </NativeSelect>
          <Button
            type='button'
            size='sm'
            variant='outline'
            render={<a href={supplierUsageExportUrl(days)} />}
          >
            <Download className='size-4' />
            {t('Download as CSV')}
          </Button>
        </div>

        {totals && totals.unpriced_requests > 0 ? (
          <Alert>
            <AlertDescription className='text-sm'>
              {t(
                '{{count}} requests ran on a model we have no published price for, so their vendor cost is shown as unpriced. They still sold, and you are still paid on what they sold.',
                { count: totals.unpriced_requests }
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        <div className='overflow-x-auto'>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Day')}</TableHead>
                <TableHead>{t('Model')}</TableHead>
                <TableHead className='text-right'>{t('Requests')}</TableHead>
                <TableHead className='text-right'>{t('Input')}</TableHead>
                <TableHead className='text-right'>{t('Cached')}</TableHead>
                <TableHead className='text-right'>{t('Output')}</TableHead>
                <TableHead className='text-right'>
                  {t('Used from your balance')}
                </TableHead>
                <TableHead className='text-right'>{t('Sold for')}</TableHead>
                <TableHead className='text-right'>{t('Your share')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={`${row.day}-${row.model}`}>
                  <TableCell className='whitespace-nowrap'>{row.day}</TableCell>
                  <TableCell className='font-medium'>{row.model}</TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {row.requests.toLocaleString()}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {row.prompt_tokens.toLocaleString()}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {row.cached_tokens.toLocaleString()}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {row.completion_tokens.toLocaleString()}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {row.priced ? formatUSD(row.list_usd) : t('unpriced')}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {formatUSD(row.sold_usd)}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {formatUSD(row.share_usd)}
                  </TableCell>
                </TableRow>
              ))}
              {totals && rows.length > 0 ? (
                <TableRow className='font-medium'>
                  <TableCell colSpan={2}>{t('Total')}</TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {totals.requests.toLocaleString()}
                  </TableCell>
                  <TableCell colSpan={3} />
                  <TableCell className='text-right tabular-nums'>
                    {formatUSD(totals.list_usd)}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {formatUSD(totals.sold_usd)}
                  </TableCell>
                  <TableCell className='text-right tabular-nums'>
                    {formatUSD(totals.share_usd)}
                  </TableCell>
                </TableRow>
              ) : null}
              {rows.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={9}
                    className='text-muted-foreground text-center'
                  >
                    {detail.isLoading
                      ? t('Loading...')
                      : t('No traffic on your keys in this window.')}
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </div>

        <p className='text-muted-foreground text-xs'>
          {t(
            '“Used from your balance” is priced at the vendor’s own published rate, so it is the figure your credit balance went down by. “Sold for” is what customers paid us for that traffic; your share is that figure times the rate your key was taken on.'
          )}
        </p>
      </CardContent>
    </Card>
  )
}
