/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
import { api } from '@/lib/api'

/**
 * Download through the authenticated HTTP client. A normal anchor navigation
 * cannot attach the dashboard Authorization header, so protected CSV routes
 * otherwise return an auth JSON error disguised as a download.
 */
export async function downloadAuthenticatedFile(path: string): Promise<string> {
  const { blob, filename } = await fetchAuthenticatedFile(path)
  const objectURL = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = objectURL
  anchor.download = filename
  anchor.style.display = 'none'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  // Safari can cancel a download when its object URL is revoked in the same
  // task as click(). Release it on the next task after navigation has begun.
  window.setTimeout(() => URL.revokeObjectURL(objectURL), 0)
  return filename
}

type DownloadRequester = (
  path: string,
  config: { responseType: 'blob' }
) => Promise<{ data: Blob; headers: Record<string, unknown> }>

export async function fetchAuthenticatedFile(
  path: string,
  request: DownloadRequester = (url, config) => api.get<Blob>(url, config)
): Promise<{ blob: Blob; filename: string }> {
  const response = await request(path, { responseType: 'blob' })
  return {
    blob: response.data,
    filename: downloadFilename(
      typeof response.headers['content-disposition'] === 'string'
        ? response.headers['content-disposition']
        : undefined,
      'unifyapi-export.csv'
    ),
  }
}

export function downloadFilename(
  contentDisposition: string | undefined,
  fallback: string
): string {
  if (!contentDisposition) return fallback
  const utf8 = contentDisposition.match(/filename\*=UTF-8''([^;]+)/i)
  if (utf8?.[1]) {
    try {
      return decodeURIComponent(utf8[1])
    } catch {
      return fallback
    }
  }
  const plain = contentDisposition.match(/filename="?([^";]+)"?/i)
  return plain?.[1] || fallback
}
