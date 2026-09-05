/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: the one credit-supply surface every customer sees. It sits in
// Wallet and on /supplier: a non-supplier is invited to sell us unused vendor
// credits (an application, no credentials); an applicant sees where their
// application stands; an approved supplier sees a summary and the way in.

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { ArrowRight, Coins } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { formatUSD } from '@/features/system-settings/billing/credit-supply-logic'

import {
  applyForSupplier,
  getSupplierPortal,
  type SupplierApplication,
  type SupplierPortalData,
} from '../api'

export function SellCreditsCard({
  compact = false,
}: {
  // compact: the Wallet placement, which links out to the portal instead of
  // repeating it.
  compact?: boolean
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [applyOpen, setApplyOpen] = useState(false)
  const me = useQuery({
    queryKey: ['supplier', 'me'],
    queryFn: getSupplierPortal,
    retry: false,
  })

  if (me.isLoading) return null

  const data: SupplierPortalData | undefined = me.data
  const status = data?.supplier.status

  return (
    <Card>
      <CardHeader>
        <CardTitle className='flex items-center gap-2 text-base'>
          <Coins className='size-4' />
          {t('Sell your unused vendor credits')}
        </CardTitle>
        <CardDescription>
          {t(
            'Hold OpenAI, Anthropic or Google credits you will not use? Offer them to UnifyAPI: we route customer traffic through your account, draw the credits down at the vendor’s list price, and pay you an agreed rate per dollar consumed.'
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className='flex flex-col gap-3'>
        {!data ? (
          <div className='flex flex-wrap items-center justify-between gap-2'>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Applying takes a minute and asks for no credentials. Once approved you can submit credits from your supplier portal.'
              )}
            </p>
            <Button type='button' size='sm' onClick={() => setApplyOpen(true)}>
              {t('Apply to become a supplier')}
            </Button>
          </div>
        ) : null}

        {data && status === 'pending' ? (
          <Alert>
            <AlertDescription className='text-sm'>
              <Badge variant='outline' className='mr-2'>
                {t('Under review')}
              </Badge>
              {t(
                'Your application as {{name}} is with the operator. You will be able to submit credits as soon as it is approved.',
                { name: data.supplier.name }
              )}
            </AlertDescription>
          </Alert>
        ) : null}

        {data && status === 'rejected' ? (
          <Alert variant='destructive'>
            <AlertDescription className='text-sm'>
              <Badge variant='secondary' className='mr-2'>
                {t('Not approved')}
              </Badge>
              {data.supplier.status_reason ||
                t('The operator did not approve this application.')}
            </AlertDescription>
          </Alert>
        ) : null}

        {data && (status === 'active' || status === 'suspended') ? (
          <div className='grid gap-3 sm:grid-cols-4'>
            <Metric
              label={t('Face value supplied')}
              value={formatUSD(data.totals.face_usd)}
            />
            <Metric
              label={t('Consumed')}
              value={formatUSD(data.totals.consumed_usd)}
            />
            <Metric
              label={t('Remaining')}
              value={formatUSD(data.totals.remaining_usd)}
            />
            <Metric
              label={t('Owed to you to date')}
              value={formatUSD(data.totals.payable_usd)}
            />
          </div>
        ) : null}

        {data && status === 'suspended' ? (
          <Alert variant='destructive'>
            <AlertDescription className='text-sm'>
              {t('Your supplier account is suspended: {{reason}}', {
                reason: data.supplier.status_reason || '—',
              })}
            </AlertDescription>
          </Alert>
        ) : null}

        {data && compact ? (
          <div className='flex justify-end'>
            <Button
              type='button'
              size='sm'
              variant='outline'
              render={<Link to='/supplier' />}
            >
              {t('Open supplier portal')}
              <ArrowRight className='size-4' />
            </Button>
          </div>
        ) : null}
      </CardContent>

      <ApplyDialog
        open={applyOpen}
        onOpenChange={setApplyOpen}
        onApplied={() => {
          setApplyOpen(false)
          queryClient.invalidateQueries({ queryKey: ['supplier'] })
        }}
      />
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className='bg-muted/40 rounded-lg p-2.5'>
      <div className='text-muted-foreground text-[11px]'>{label}</div>
      <div className='mt-0.5 text-base font-semibold tabular-nums'>{value}</div>
    </div>
  )
}

function ApplyDialog({
  open,
  onOpenChange,
  onApplied,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onApplied: () => void
}) {
  const { t } = useTranslation()
  const [form, setForm] = useState<SupplierApplication>({
    name: '',
    contact_email: '',
    note: '',
    attested: false,
  })
  const mutation = useMutation({
    mutationFn: applyForSupplier,
    onSuccess: () => {
      toast.success(
        t('Application submitted. We will review it and get back to you.')
      )
      onApplied()
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const submit = () => {
    if (!form.name.trim() || !form.contact_email.includes('@')) {
      toast.error(t('A name and a contact email are required.'))
      return
    }
    if (!form.attested) {
      toast.error(t('Confirm that you own or control the vendor accounts.'))
      return
    }
    mutation.mutate({
      ...form,
      name: form.name.trim(),
      contact_email: form.contact_email.trim(),
      note: form.note.trim(),
    })
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Apply to become a supplier')}
      description={t(
        'Tell us who you are and what you hold. Do not paste API keys here — credentials are only ever submitted per lot, after approval, into a channel we control.'
      )}
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => onOpenChange(false)}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={submit}
            disabled={mutation.isPending || !form.attested}
          >
            {t('Submit application')}
          </Button>
        </>
      }
    >
      <div className='grid gap-4'>
        <div className='grid gap-2'>
          <Label>{t('Company or team name')}</Label>
          <Input
            value={form.name}
            placeholder='Acme Labs'
            onChange={(event) => setForm({ ...form, name: event.target.value })}
          />
        </div>
        <div className='grid gap-2'>
          <Label>{t('Contact email')}</Label>
          <Input
            type='email'
            value={form.contact_email}
            onChange={(event) =>
              setForm({ ...form, contact_email: event.target.value })
            }
          />
        </div>
        <div className='grid gap-2'>
          <Label>
            {t('What you hold (vendors, rough amounts, how obtained)')}
          </Label>
          <Textarea
            rows={3}
            value={form.note}
            placeholder={t(
              'e.g. ~$5,000 of Anthropic startup-program credits expiring in December'
            )}
            onChange={(event) => setForm({ ...form, note: event.target.value })}
          />
        </div>
        <Alert>
          <AlertDescription className='flex items-start gap-3 text-xs'>
            <Switch
              checked={form.attested}
              onCheckedChange={(attested) => setForm({ ...form, attested })}
            />
            <span>
              {t(
                'I own or control the vendor accounts I intend to offer and am authorised to let UnifyAPI consume their credits. I understand the operator reviews each lot against the vendor’s terms.'
              )}
            </span>
          </AlertDescription>
        </Alert>
      </div>
    </Dialog>
  )
}
