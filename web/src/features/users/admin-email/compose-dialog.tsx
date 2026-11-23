/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Mail } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { useDebounce } from '@/hooks'

import {
  getAdminEmailTemplates,
  previewAdminEmail,
  sendAdminEmail,
  type AdminEmailContent,
  type AdminEmailRecipient,
} from './api'
import type { ComposeRecipient } from './compose-context'
import {
  BLANK_CONTENT,
  contentFromTemplate,
  hasContent,
  replyToWarning,
} from './lib'
import { BatchStatus, UserSendHistory } from './send-status'

type Step = 'compose' | 'confirm' | 'sent'

type ComposeDialogProps = {
  recipients: ComposeRecipient[]
  onClose: () => void
  onManageTemplates: () => void
}

function SkippedList(props: { recipients: AdminEmailRecipient[] }) {
  const { t } = useTranslation()
  const skipped = props.recipients.filter((r) => r.status === 'skipped')
  if (skipped.length === 0) return null
  return (
    <div className='rounded-md border p-3 text-sm'>
      <p className='mb-1 font-medium'>
        {t('Skipped ({{count}}), will not be emailed:', {
          count: skipped.length,
        })}
      </p>
      <ul className='text-muted-foreground max-h-32 space-y-0.5 overflow-y-auto text-xs'>
        {skipped.map((r) => (
          <li key={r.user_id}>
            {r.username || `#${r.user_id}`}
            {r.email ? ` <${r.email}>` : ''} · {r.reason}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function ComposeDialog(props: ComposeDialogProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [content, setContent] = useState<AdminEmailContent>(BLANK_CONTENT)
  const [step, setStep] = useState<Step>('compose')
  const [batchId, setBatchId] = useState('')
  const userIds = useMemo(
    () => props.recipients.map((r) => r.id),
    [props.recipients]
  )
  const single = props.recipients.length === 1 ? props.recipients[0] : null

  const { data: state } = useQuery({
    queryKey: ['admin-email', 'templates'],
    queryFn: getAdminEmailTemplates,
  })
  const debounced = useDebounce(content, 400)
  const preview = useQuery({
    queryKey: ['admin-email', 'preview', debounced, userIds],
    queryFn: () => previewAdminEmail(debounced, userIds),
    enabled: hasContent(debounced),
    retry: false,
  })

  const send = useMutation({
    mutationFn: () => sendAdminEmail(content, userIds),
    onSuccess: (result) => {
      setBatchId(result.batch_id)
      setStep('sent')
      toast.success(t('{{count}} email(s) queued', { count: result.queued }))
      queryClient.invalidateQueries({ queryKey: ['admin-email', 'history'] })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  const replyTo = state?.reply_to ?? ''
  const warning = state ? replyToWarning(replyTo, content) : 'none'
  const field = (key: keyof AdminEmailContent) => ({
    value: String(content[key]),
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setContent((c) => ({ ...c, [key]: e.target.value })),
  })
  const willSend = preview.data?.will_send ?? 0
  const previewCurrent = preview.data && debounced === content

  const title = single
    ? t('Send email to {{name}}', { name: single.username })
    : t('Send email to {{count}} users', { count: props.recipients.length })

  return (
    <Dialog open onOpenChange={(open) => !open && props.onClose()}>
      <DialogContent className='max-h-[92vh] overflow-y-auto sm:max-w-6xl'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Mail className='size-4' aria-hidden='true' />
            {title}
          </DialogTitle>
          <DialogDescription>
            {single
              ? single.email || t('This user has no email address.')
              : t(
                  'Bulk sends skip users without an email address, disabled accounts and users who unsubscribed from product email.'
                )}
          </DialogDescription>
        </DialogHeader>

        {step === 'sent' && batchId && <BatchStatus batchId={batchId} />}
        {step === 'sent' && !batchId && (
          <p className='text-sm'>{t('Nobody could be emailed.')}</p>
        )}

        {step === 'confirm' && preview.data && (
          <div className='space-y-3'>
            <Alert>
              <AlertTriangle aria-hidden='true' />
              <AlertTitle>
                {t('Send "{{subject}}" to {{count}} recipient(s)?', {
                  subject: preview.data.subject,
                  count: willSend,
                })}
              </AlertTitle>
              <AlertDescription>
                {t('This sends real email and cannot be undone.')}
                {preview.data.reply_to
                  ? ` ${t('Replies go to {{address}}.', { address: preview.data.reply_to })}`
                  : ` ${t('No Reply-To is set: replies go to the no-reply sender.')}`}
              </AlertDescription>
            </Alert>
            <SkippedList recipients={preview.data.recipients} />
          </div>
        )}

        {step === 'compose' && (
          <Tabs defaultValue='compose'>
            {single && (
              <TabsList>
                <TabsTrigger value='compose'>{t('Compose')}</TabsTrigger>
                <TabsTrigger value='history'>{t('History')}</TabsTrigger>
              </TabsList>
            )}
            <TabsContent value='compose'>
              <div className='grid gap-6 lg:grid-cols-2'>
                <div className='space-y-4'>
                  <div className='space-y-1.5'>
                    <div className='flex items-center justify-between'>
                      <Label htmlFor='admin-email-template'>
                        {t('Template')}
                      </Label>
                      <Button
                        variant='link'
                        size='sm'
                        className='h-auto p-0'
                        onClick={props.onManageTemplates}
                      >
                        {t('Manage templates')}
                      </Button>
                    </div>
                    <NativeSelect
                      id='admin-email-template'
                      className='w-full'
                      value={String(content.template_id)}
                      onChange={(e) =>
                        setContent(
                          contentFromTemplate(
                            state?.templates.find(
                              (tpl) => tpl.id === Number(e.target.value)
                            )
                          )
                        )
                      }
                    >
                      <NativeSelectOption value='0'>
                        {t('Blank message')}
                      </NativeSelectOption>
                      {state?.templates.map((tpl) => (
                        <NativeSelectOption key={tpl.id} value={String(tpl.id)}>
                          {tpl.name}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  </div>
                  <div className='space-y-1.5'>
                    <Label htmlFor='admin-email-subject'>{t('Subject')}</Label>
                    <Input id='admin-email-subject' {...field('subject')} />
                  </div>
                  <div className='space-y-1.5'>
                    <Label htmlFor='admin-email-headline'>
                      {t('Headline (optional)')}
                    </Label>
                    <Input id='admin-email-headline' {...field('headline')} />
                  </div>
                  <div className='space-y-1.5'>
                    <Label htmlFor='admin-email-body'>{t('Message')}</Label>
                    <Textarea
                      id='admin-email-body'
                      className='min-h-56'
                      {...field('body')}
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'Plain text. A blank line starts a new paragraph; links become clickable. Variables:'
                      )}{' '}
                      <code className='break-words'>
                        {(state?.variables ?? [])
                          .map((v) => `{{${v}}}`)
                          .join(' ')}
                      </code>
                    </p>
                  </div>
                  <div className='grid gap-3 sm:grid-cols-2'>
                    <div className='space-y-1.5'>
                      <Label htmlFor='admin-email-cta-label'>
                        {t('Button label (optional)')}
                      </Label>
                      <Input
                        id='admin-email-cta-label'
                        {...field('cta_label')}
                      />
                    </div>
                    <div className='space-y-1.5'>
                      <Label htmlFor='admin-email-cta-url'>
                        {t('Button link (optional)')}
                      </Label>
                      <Input
                        id='admin-email-cta-url'
                        placeholder='{{console_url}}'
                        {...field('cta_url')}
                      />
                    </div>
                  </div>
                  {warning !== 'none' && (
                    <Alert
                      variant={
                        warning === 'blocking' ? 'destructive' : 'default'
                      }
                    >
                      <AlertTriangle aria-hidden='true' />
                      <AlertTitle>{t('No Reply-To address is set')}</AlertTitle>
                      <AlertDescription>
                        {warning === 'blocking'
                          ? t(
                              'This message asks the customer to reply, but replies would go to the no-reply sender. Set a Reply-To address under Manage templates first.'
                            )
                          : t(
                              'Replies to this email go to the no-reply sender. Set a Reply-To address under Manage templates.'
                            )}
                      </AlertDescription>
                    </Alert>
                  )}
                  {state && !state.smtp_ready && (
                    <Alert variant='destructive'>
                      <AlertTriangle aria-hidden='true' />
                      <AlertTitle>{t('SMTP is not configured')}</AlertTitle>
                      <AlertDescription>
                        {t(
                          'You can preview, but nothing can be sent until SMTP is set up.'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}
                </div>

                <div className='space-y-3'>
                  <div className='flex items-center justify-between text-sm'>
                    <span className='font-medium'>{t('Preview')}</span>
                    {preview.data && (
                      <span className='text-muted-foreground'>
                        {t('As sent to user #{{id}}', {
                          id: preview.data.preview_user_id,
                        })}
                      </span>
                    )}
                  </div>
                  {preview.error && (
                    <p className='text-destructive text-sm' role='alert'>
                      {(preview.error as Error).message}
                    </p>
                  )}
                  {preview.data ? (
                    <>
                      <p className='text-sm'>
                        <span className='text-muted-foreground'>
                          {t('Subject')}:{' '}
                        </span>
                        {preview.data.subject}
                      </p>
                      <iframe
                        title={t('Email preview')}
                        sandbox=''
                        srcDoc={preview.data.html}
                        className='h-[480px] w-full rounded-md border bg-[#FAFAF7]'
                      />
                      <p className='text-sm'>
                        {t('Will send to {{count}} recipient(s).', {
                          count: willSend,
                        })}
                        {preview.data.unsubscribe_link &&
                          ` ${t('Includes an unsubscribe link (bulk send).')}`}
                      </p>
                      <SkippedList recipients={preview.data.recipients} />
                    </>
                  ) : (
                    !preview.error && (
                      <p className='text-muted-foreground rounded-md border border-dashed p-8 text-center text-sm'>
                        {t(
                          'Pick a template or write a subject and a message to see the email.'
                        )}
                      </p>
                    )
                  )}
                </div>
              </div>
            </TabsContent>
            {single && (
              <TabsContent value='history'>
                <UserSendHistory userId={single.id} />
              </TabsContent>
            )}
          </Tabs>
        )}

        <DialogFooter>
          {step === 'compose' && (
            <>
              <Button variant='outline' onClick={props.onClose}>
                {t('Cancel')}
              </Button>
              <Button
                disabled={
                  !previewCurrent ||
                  willSend === 0 ||
                  !state?.smtp_ready ||
                  warning === 'blocking'
                }
                onClick={() => setStep('confirm')}
              >
                {t('Review and send')}
              </Button>
            </>
          )}
          {step === 'confirm' && (
            <>
              <Button variant='outline' onClick={() => setStep('compose')}>
                {t('Back')}
              </Button>
              <Button disabled={send.isPending} onClick={() => send.mutate()}>
                {send.isPending
                  ? t('Sending...')
                  : t('Send to {{count}} recipient(s)', { count: willSend })}
              </Button>
            </>
          )}
          {step === 'sent' && (
            <Button onClick={props.onClose}>{t('Close')}</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
