/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

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
*/

/**
 * UNIFYAPI-BRAND: the marketing site the console's public chrome points back
 * to. The logo and the "Home" item go here, not to the SPA's `/`: that route
 * is upstream's landing page, which the product never shows on purpose.
 */
export const MARKETING_SITE_URL = 'https://www.unifyapi.ai/'

/** True for an absolute http(s) URL, which the router must not treat as a route. */
export function isExternalHref(href: string): boolean {
  return /^https?:\/\//i.test(href)
}

/**
 * Where a client-side navigation to `/` ends up: the same place the edge
 * sends a hard navigation -- the dashboard when signed in, the sign-in page
 * otherwise -- so upstream's landing page is unreachable either way.
 */
export function rootDestination(signedIn: boolean): '/dashboard' | '/sign-in' {
  return signedIn ? '/dashboard' : '/sign-in'
}
