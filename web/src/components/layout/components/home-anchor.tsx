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
import { Link } from '@tanstack/react-router'

import { isExternalHref } from '@/lib/marketing-site'

type HomeAnchorProps = {
  to: string
  className?: string
  onClick?: () => void
  children: React.ReactNode
}

/**
 * UNIFYAPI-BRAND: the brand logo's link. Upstream renders a router Link, which
 * cannot leave the origin; ours points at the marketing site, so an absolute
 * URL renders a plain anchor (same tab -- it is the site's own home, not a
 * third party) and anything else stays a router Link.
 */
export function HomeAnchor({
  to,
  className,
  onClick,
  children,
}: HomeAnchorProps) {
  if (isExternalHref(to)) {
    return (
      <a href={to} className={className} onClick={onClick}>
        {children}
      </a>
    )
  }
  return (
    <Link to={to} className={className} onClick={onClick}>
      {children}
    </Link>
  )
}
