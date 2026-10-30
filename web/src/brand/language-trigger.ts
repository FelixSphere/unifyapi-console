/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import type { InterfaceLanguageCode } from '@/i18n/languages'

/**
 * UNIFYAPI-BRAND: the text on the header's language switcher.
 *
 * Upstream's trigger was icon-only, and the icon (lucide `Languages`, a 文
 * beside an A) depicts two specific scripts -- to a Vietnamese, Russian or
 * Japanese reader it points at other languages. Its only words were a
 * screen-reader label translated into the CURRENT interface language, so a
 * visitor who landed in a language they cannot read had nothing on screen to
 * recognise. The trigger now shows a globe plus the current language, written
 * in that language's own script, short enough for a crowded header.
 */
export const LANGUAGE_TRIGGER_LABEL: Record<InterfaceLanguageCode, string> = {
  zhCN: '中文',
  en: 'EN',
  fr: 'FR',
  ru: 'RU',
  ja: '日本語',
  vi: 'VI',
  zhTW: '繁中',
}
