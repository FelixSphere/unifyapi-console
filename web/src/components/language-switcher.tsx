/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Check, Globe } from 'lucide-react'
import { useCallback } from 'react'
import { useTranslation } from 'react-i18next'

import { LANGUAGE_TRIGGER_LABEL } from '@/brand/language-trigger'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  INTERFACE_LANGUAGE_OPTIONS,
  normalizeInterfaceLanguage,
  type InterfaceLanguageCode,
} from '@/i18n/languages'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

export function LanguageSwitcher() {
  const { i18n, t } = useTranslation()
  const user = useAuthStore((s) => s.auth.user)
  const currentLanguage = normalizeInterfaceLanguage(
    i18n.language
  ) as InterfaceLanguageCode
  const currentOption = INTERFACE_LANGUAGE_OPTIONS.find(
    (lang) => lang.code === currentLanguage
  )
  const handleChangeLanguage = useCallback(
    async (code: string) => {
      await i18n.changeLanguage(code)
      if (user) {
        try {
          await api.put('/api/user/self', { language: code })
        } catch {
          // Best-effort persistence; don't block the UI on failure
        }
      }
    },
    [i18n, user]
  )

  return (
    <DropdownMenu modal={false}>
      {/* UNIFYAPI-BRAND: a globe plus the current language in its own script,
          so the control is recognisable without reading the interface
          language. See brand/language-trigger.ts. */}
      <DropdownMenuTrigger
        render={
          <Button
            variant='ghost'
            className='h-9 gap-1.5 px-2 text-sm font-medium'
            aria-label={`${t('Change language')} (${currentOption?.label ?? 'English'})`}
            title='Language'
          />
        }
      >
        <Globe className='size-4' aria-hidden='true' />
        <span aria-hidden='true'>
          {LANGUAGE_TRIGGER_LABEL[currentLanguage] ?? 'EN'}
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align='end'>
        {INTERFACE_LANGUAGE_OPTIONS.map((lang) => (
          <DropdownMenuItem
            key={lang.code}
            onClick={() => handleChangeLanguage(lang.code)}
          >
            {lang.label}
            <Check
              size={14}
              className={cn(
                'ms-auto',
                currentLanguage !== lang.code && 'hidden'
              )}
            />
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
