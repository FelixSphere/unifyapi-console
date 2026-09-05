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
import {
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from '@tanstack/react-query'
import { RouterProvider, createRouter } from '@tanstack/react-router'
import { AxiosError } from 'axios'
import i18next from 'i18next'
import { StrictMode } from 'react'
import ReactDOM from 'react-dom/client'
import { toast } from 'sonner'

import { api, getStatus } from '@/lib/api'
import { installBuildMetadata } from '@/lib/build-metadata'
import { applyFaviconToDom } from '@/lib/dom-utils'
import '@/lib/dayjs'
import { initializeFrontendCache } from '@/lib/frontend-cache'
import { handleServerError } from '@/lib/handle-server-error'
import {
  getStaleBundleReason,
  isServerFailure,
  subscribeStaleBundle,
} from '@/lib/stale-bundle'

import { DirectionProvider } from './context/direction-provider'
import { FontProvider } from './context/font-provider'
import { ThemeProvider } from './context/theme-provider'
import './i18n/config'
// Generated Routes
import { routeTree } from './routeTree.gen'

// Styles
// UNIFYAPI-BRAND: unifyapi.css imports upstream's index.css first, then layers
// our palette/fonts on top. Keeps index.css byte-untouched. See BRANDING.md.
import './styles/unifyapi.css'

// Ensure VChart theme is initialized before any chart mounts (prevents white default theme flash)
// VChart theme is driven by our ThemeProvider (html.light/html.dark) via per-chart `theme` prop.
initializeFrontendCache()
installBuildMetadata()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        // eslint-disable-next-line no-console
        if (import.meta.env.DEV) console.log({ failureCount, error })

        if (failureCount >= 0 && import.meta.env.DEV) return false
        if (failureCount > 3 && import.meta.env.PROD) return false

        return !(
          error instanceof AxiosError &&
          [401, 403].includes(error.response?.status ?? 0)
        )
      },
      // Keep focused tabs from silently re-running heavy pages like logs.
      refetchOnWindowFocus: false,
      staleTime: 10 * 1000, // 10s
    },
    mutations: {
      onError: (error) => {
        // A stale bundle is not a server fault; the reload prompt covers it.
        if (getStaleBundleReason(error)) return
        handleServerError(error)

        if (error instanceof AxiosError) {
          if (error.response?.status === 304) {
            toast.error(i18next.t('Content not modified!'))
          }
        }
      },
    },
  },
  queryCache: new QueryCache({
    onError: (error) => {
      // /500 is for genuine server failures (status >= 500) only. A tab still
      // running the bundle from before a release gets 404s for endpoints that
      // no longer exist; those raise the reload prompt below instead.
      if (isServerFailure(error)) {
        toast.error(i18next.t('Internal Server Error!'))
        router.navigate({ to: '/500' })
      }
    },
  }),
})

// Create a new router instance
const router = createRouter({
  routeTree,
  context: { queryClient },
  defaultPreload: 'intent',
  defaultPreloadStaleTime: 0,
})

// Stale-bundle handling: prompt once, never reload on the user's behalf (a
// form half filled in must survive). See lib/stale-bundle.ts.
subscribeStaleBundle(() => {
  toast.info(i18next.t('A new version of the console is available.'), {
    id: 'stale-bundle',
    duration: Number.POSITIVE_INFINITY,
    description: i18next.t(
      'Reload the page to continue with the latest version.'
    ),
    action: {
      label: i18next.t('Reload'),
      onClick: () => window.location.reload(),
    },
  })
})
// Besides comparing the build id on every API response, probe the server on
// an interval and when the tab comes back to the foreground: a tab left open
// across a release would otherwise only learn of it from its next failure.
;(function watchServerBuild() {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  const PROBE_INTERVAL_MS = 5 * 60 * 1000
  const PROBE_MIN_GAP_MS = 60 * 1000
  let lastProbeAt = Date.now()
  const probe = () => {
    if (Date.now() - lastProbeAt < PROBE_MIN_GAP_MS) return
    lastProbeAt = Date.now()
    // The response interceptor reads X-UnifyAPI-Build; the body is not needed.
    api.get('/api/status', { skipErrorHandler: true }).catch(() => {
      /* offline or rate limited: the next probe tries again */
    })
  }
  window.setInterval(probe, PROBE_INTERVAL_MS)
  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') probe()
  })
})()

// Register the router instance for type safety
declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

// Render the app
const rootElement = document.querySelector<HTMLElement>('#root')
if (!rootElement) {
  throw new Error('Root element not found')
}
// Set document.title and favicon from cached status, then refresh from network
;(function initSystemBranding() {
  try {
    if (typeof window === 'undefined' || typeof document === 'undefined') return
    const apply = (name: string) => {
      document.title = name
      const metaTitle = document.querySelector(
        'meta[name="title"]'
      ) as HTMLMetaElement | null
      if (metaTitle) metaTitle.setAttribute('content', name)
    }
    // Cache-first
    try {
      const saved = localStorage.getItem('status')
      if (saved) {
        const s = JSON.parse(saved)
        if (s?.system_name) apply(s.system_name)
        if (s?.logo) applyFaviconToDom(s.logo)
      }
    } catch {
      /* empty */
    }
    // Background refresh
    getStatus()
      .then((s) => {
        if (s?.system_name) {
          apply(s.system_name as string)
          try {
            localStorage.setItem('status', JSON.stringify(s))
          } catch {
            /* empty */
          }
        }
        if (s?.logo) applyFaviconToDom(s.logo as string)
      })
      .catch(() => {
        /* empty */
      })
  } catch {
    /* empty */
  }
})()
if (!rootElement.innerHTML) {
  const root = ReactDOM.createRoot(rootElement)
  root.render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        {/* UNIFYAPI-BRAND: light-only. CSS alone can't reach the JS consumers
            of resolvedTheme (VChart, Sonner). See BRANDING.md. */}
        <ThemeProvider defaultTheme='light'>
          <FontProvider>
            <DirectionProvider>
              <RouterProvider router={router} />
            </DirectionProvider>
          </FontProvider>
        </ThemeProvider>
      </QueryClientProvider>
    </StrictMode>
  )
}
