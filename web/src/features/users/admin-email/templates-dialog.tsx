/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Pencil, Plus, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  createAdminEmailTemplate,
  deleteAdminEmailTemplate,
  getAdminEmailTemplates,
  updateAdminEmailReplyTo,
  updateAdminEmailTemplate,
  type AdminEmailTemplate,
  type AdminEmailTemplateInput,
} from './api'

const EMPTY_TEMPLATE: AdminEmailTemplateInput = {
  name: '',
  subject: '',
  headline: '',
  body: '',
  cta_label: '',
  cta_url: '',
}

const TEMPLATES_KEY = ['admin-email', 'templates']

function ReplyToSetting(props: { current: string }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const isRoot = useAuthStore((s) => s.auth.user?.role === ROLE.SUPER_ADMIN)
  const [value, setValue] = useState(props.current)
  const { current } = props
  useEffect(() => setValue(current), [current])
  const save = useMutation({
    mutationFn: () => updateAdminEmailReplyTo(value.trim()),
    onSuccess: () => {
      toast.success(t('Reply-To saved'))
      queryClient.invalidateQueries({ queryKey: TEMPLATES_KEY })
    },
    onError: (error: Error) => toast.error(error.message),
  })
  return (
    <div className='space-y-1.5 rounded-md border p-3'>
      <Label htmlFor='admin-email-reply-to'>{t('Reply-To address')}</Label>
      <div className='flex gap-2'>
        <Input
          id='admin-email-reply-to'
          type='email'
          placeholder='support@example.com'
          value={value}
          disabled={!isRoot}
          onChange={(e) => setValue(e.target.value)}
        />
        {isRoot && (
          <Button
            variant='outline'
            disabled={save.isPending || value.trim() === props.current}
            onClick={() => save.mutate()}
          >
            {t('Save')}
          </Button>
        )}
      </div>
      <p className='text-muted-foreground text-xs'>
        {isRoot
          ? t(
              'Replies to emails sent from the Users page go here. Leave empty to send without a Reply-To (replies then go to the no-reply sender).'
            )
          : t(
              'Replies to emails sent from the Users page go here. Only a super admin can change it.'
            )}
      </p>
    </div>
  )
}

function TemplateEditor(props: {
  template: AdminEmailTemplate | null
  variables: string[]
  onDone: () => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<AdminEmailTemplateInput>(
    props.template ? { ...props.template } : EMPTY_TEMPLATE
  )
  const save = useMutation({
    mutationFn: () => {
      const input: AdminEmailTemplateInput = {
        name: form.name,
        subject: form.subject,
        headline: form.headline,
        body: form.body,
        cta_label: form.cta_label,
        cta_url: form.cta_url,
      }
      return props.template
        ? updateAdminEmailTemplate(props.template.id, input)
        : createAdminEmailTemplate(input)
    },
    onSuccess: () => {
      toast.success(t('Template saved'))
      queryClient.invalidateQueries({ queryKey: TEMPLATES_KEY })
      props.onDone()
    },
    onError: (error: Error) => toast.error(error.message),
  })
  const field = (key: keyof AdminEmailTemplateInput) => ({
    value: form[key],
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      setForm((f) => ({ ...f, [key]: e.target.value })),
  })
  return (
    <div className='space-y-3'>
      <div className='space-y-1.5'>
        <Label htmlFor='tpl-name'>{t('Template name')}</Label>
        <Input id='tpl-name' {...field('name')} />
      </div>
      <div className='space-y-1.5'>
        <Label htmlFor='tpl-subject'>{t('Subject')}</Label>
        <Input id='tpl-subject' {...field('subject')} />
      </div>
      <div className='space-y-1.5'>
        <Label htmlFor='tpl-headline'>{t('Headline (optional)')}</Label>
        <Input id='tpl-headline' {...field('headline')} />
      </div>
      <div className='space-y-1.5'>
        <Label htmlFor='tpl-body'>{t('Message')}</Label>
        <Textarea id='tpl-body' className='min-h-56' {...field('body')} />
        <p className='text-muted-foreground text-xs'>
          {t('Variables:')}{' '}
          <code className='break-words'>
            {props.variables.map((v) => `{{${v}}}`).join(' ')}
          </code>
        </p>
      </div>
      <div className='grid gap-3 sm:grid-cols-2'>
        <div className='space-y-1.5'>
          <Label htmlFor='tpl-cta-label'>{t('Button label (optional)')}</Label>
          <Input id='tpl-cta-label' {...field('cta_label')} />
        </div>
        <div className='space-y-1.5'>
          <Label htmlFor='tpl-cta-url'>{t('Button link (optional)')}</Label>
          <Input
            id='tpl-cta-url'
            placeholder='{{console_url}}'
            {...field('cta_url')}
          />
        </div>
      </div>
      <div className='flex justify-end gap-2'>
        <Button variant='outline' onClick={props.onDone}>
          {t('Cancel')}
        </Button>
        <Button disabled={save.isPending} onClick={() => save.mutate()}>
          {t('Save template')}
        </Button>
      </div>
    </div>
  )
}

export function TemplatesDialog(props: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [editing, setEditing] = useState<AdminEmailTemplate | 'new' | null>(
    null
  )
  const [deleting, setDeleting] = useState<AdminEmailTemplate | null>(null)
  const { data: state } = useQuery({
    queryKey: TEMPLATES_KEY,
    queryFn: getAdminEmailTemplates,
    enabled: props.open,
  })
  const remove = useMutation({
    mutationFn: (id: number) => deleteAdminEmailTemplate(id),
    onSuccess: () => {
      toast.success(t('Template deleted'))
      setDeleting(null)
      queryClient.invalidateQueries({ queryKey: TEMPLATES_KEY })
    },
    onError: (error: Error) => toast.error(error.message),
  })

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open) setEditing(null)
        props.onOpenChange(open)
      }}
    >
      <DialogContent className='max-h-[92vh] overflow-y-auto sm:max-w-3xl'>
        <DialogHeader>
          <DialogTitle>{t('Email templates')}</DialogTitle>
          <DialogDescription>
            {t('Reusable messages for emailing customers from the Users page.')}
          </DialogDescription>
        </DialogHeader>

        {editing ? (
          <TemplateEditor
            template={editing === 'new' ? null : editing}
            variables={state?.variables ?? []}
            onDone={() => setEditing(null)}
          />
        ) : (
          <div className='space-y-4'>
            <ReplyToSetting current={state?.reply_to ?? ''} />
            <div className='flex justify-end'>
              <Button size='sm' onClick={() => setEditing('new')}>
                <Plus className='size-4' aria-hidden='true' />
                {t('New template')}
              </Button>
            </div>
            {state && state.templates.length === 0 ? (
              <p className='text-muted-foreground py-6 text-center text-sm'>
                {t('No templates yet.')}
              </p>
            ) : (
              <ul className='divide-y rounded-md border'>
                {state?.templates.map((tpl) => (
                  <li
                    key={tpl.id}
                    className='flex items-center gap-3 px-3 py-2 text-sm'
                  >
                    <div className='min-w-0 flex-1'>
                      <div className='truncate font-medium'>{tpl.name}</div>
                      <div className='text-muted-foreground truncate text-xs'>
                        {tpl.subject}
                      </div>
                    </div>
                    <Button
                      variant='ghost'
                      size='icon'
                      aria-label={t('Edit {{name}}', { name: tpl.name })}
                      onClick={() => setEditing(tpl)}
                    >
                      <Pencil className='size-4' aria-hidden='true' />
                    </Button>
                    <Button
                      variant='ghost'
                      size='icon'
                      aria-label={t('Delete {{name}}', { name: tpl.name })}
                      onClick={() => setDeleting(tpl)}
                    >
                      <Trash2 className='size-4' aria-hidden='true' />
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}

        <ConfirmDialog
          open={deleting !== null}
          onOpenChange={(open) => !open && setDeleting(null)}
          title={t('Delete template?')}
          desc={t(
            '"{{name}}" will be removed for every admin. Emails already sent are not affected.',
            {
              name: deleting?.name ?? '',
            }
          )}
          destructive
          confirmText={t('Delete')}
          isLoading={remove.isPending}
          handleConfirm={() => deleting && remove.mutate(deleting.id)}
        />
      </DialogContent>
    </Dialog>
  )
}
