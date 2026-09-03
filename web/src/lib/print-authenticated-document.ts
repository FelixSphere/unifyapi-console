/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

type PrintWindow = Pick<Window, 'close' | 'document' | 'focus' | 'print'> & {
  opener: Window | null
}

type HTMLRequester = (
  path: string,
  config: { responseType: 'text'; disableDuplicate: true }
) => Promise<{ data: string }>

/**
 * Fetch through the authenticated API client, then print from a same-click
 * popup. Direct navigation cannot attach the dashboard bearer token, which is
 * the same reason the old CSV anchor returned a 401 JSON document.
 */
export async function printAuthenticatedDocument(
  path: string,
  request: HTMLRequester = (url, config) => api.get<string>(url, config),
  openWindow: () => PrintWindow | null = () =>
    window.open('', '_blank') as PrintWindow | null
): Promise<void> {
  const target = openWindow()
  if (!target) throw new Error('Pop-up blocked. Allow pop-ups and try again.')
  target.opener = null
  target.document.open()
  target.document.write('<!doctype html><title>Preparing invoice...</title>')
  target.document.close()

  try {
    const response = await request(path, {
      responseType: 'text',
      disableDuplicate: true,
    })
    target.document.open()
    target.document.write(response.data)
    target.document.close()
    target.focus()
    target.print()
  } catch (error) {
    target.close()
    throw error
  }
}
