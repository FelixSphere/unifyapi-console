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
import { DEFAULT_LOGO } from '@/lib/constants'

export function applyFaviconToDom(url: string) {
  if (typeof document === 'undefined' || !url) return
  // UNIFYAPI-BRAND: the static <link rel=icon> set in index.html is the
  // UnifyAI mark from the kit (SVG + ICO + apple-touch). Upstream replaces it
  // with the `Logo` option on every load, which turned the tab icon into the
  // raster tile even when nobody had customised the logo. Only an admin's own
  // Logo value may override the kit icons.
  if (url === DEFAULT_LOGO) return
  try {
    const next = new URL(url, window.location.href).href
    const existing =
      document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]')
    if (existing.length === 1 && existing[0].href === next) return
    const link = document.createElement('link')
    link.rel = 'icon'
    link.href = url
    existing.forEach((l) => l.remove())
    document.head.appendChild(link)
  } catch {
    // Ignore malformed URLs
  }
}
