/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

/**
 * Stale-bundle detection.
 *
 * A browser tab keeps running the SPA bundle it loaded until it reloads. After
 * a release (a hot-swap of the console container) that tab talks to a newer
 * server: an endpoint it still calls may be gone, in which case the router
 * answers with the relay's OpenAI-style 404 body (`controller.RelayNotFound`).
 * That is not a server fault and must not land on /500; the user needs a
 * reload prompt.
 *
 * Both sides carry the same build id. Rsbuild stamps one id per production
 * build into the bundle (`import.meta.env.VITE_REACT_APP_BUILD_ID`) and into
 * index.html as `<meta name="unifyapi-build">`; the Go server reads the meta
 * tag from the embedded index.html at startup and echoes it on every response
 * as `X-UnifyAPI-Build` and in /api/status as `build_id` (common/build_id.go).
 * The VERSION string cannot serve this purpose: it is not bumped per release.
 */

export const SERVER_BUILD_HEADER = 'x-unifyapi-build'

export type StaleBundleReason = 'build-mismatch' | 'removed-endpoint'

/** Build id this bundle was compiled with. Empty in dev, where the check is off. */
export const CLIENT_BUILD_ID: string = readClientBuildId()

function readClientBuildId(): string {
  const raw: unknown = import.meta.env.VITE_REACT_APP_BUILD_ID
  return typeof raw === 'string' ? raw.trim() : ''
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

/** Read the server build id from Axios (or plain object) response headers. */
export function readServerBuildId(headers: unknown): string {
  if (!isRecord(headers)) return ''
  let value: unknown
  if (typeof headers.get === 'function') {
    value = (headers.get as (name: string) => unknown)(SERVER_BUILD_HEADER)
  } else {
    value = headers[SERVER_BUILD_HEADER] ?? headers['X-UnifyAPI-Build']
  }
  return typeof value === 'string' ? value.trim() : ''
}

export function isBuildMismatch(
  serverBuildId: string,
  clientBuildId: string
): boolean {
  return (
    clientBuildId !== '' &&
    serverBuildId !== '' &&
    serverBuildId !== clientBuildId
  )
}

/**
 * The router's answer for an unknown path under /api, /v1 or /assets:
 * `{"error":{"message":"Invalid URL (POST /api/…)","type":"invalid_request_error"}}`.
 */
export function isRelayNotFoundPayload(data: unknown): boolean {
  if (!isRecord(data) || !isRecord(data.error)) return false
  return (
    data.error.type === 'invalid_request_error' &&
    typeof data.error.message === 'string' &&
    data.error.message.startsWith('Invalid URL')
  )
}

function requestPath(config: unknown): string {
  if (!isRecord(config) || typeof config.url !== 'string') return ''
  const base = typeof config.baseURL === 'string' ? config.baseURL : ''
  try {
    return new URL(config.url, base || 'http://localhost').pathname
  } catch {
    return config.url
  }
}

/**
 * Classify a failed request. A mismatching build header is conclusive. A relay
 * 404 under /api counts as a removed endpoint unless the server proved it was
 * built from the same bundle, in which case it is a bug in this build, not
 * staleness, and the normal error path applies.
 */
export function getStaleBundleReason(
  error: unknown,
  clientBuildId: string = CLIENT_BUILD_ID
): StaleBundleReason | null {
  if (!isRecord(error) || !isRecord(error.response)) return null
  const response = error.response

  const serverBuildId = readServerBuildId(response.headers)
  if (isBuildMismatch(serverBuildId, clientBuildId)) return 'build-mismatch'

  const sameBuild = clientBuildId !== '' && serverBuildId === clientBuildId
  if (
    !sameBuild &&
    response.status === 404 &&
    requestPath(error.config).startsWith('/api/') &&
    isRelayNotFoundPayload(response.data)
  ) {
    return 'removed-endpoint'
  }
  return null
}

/** /500 is for genuine server failures only: a response with status >= 500. */
export function isServerFailure(error: unknown): boolean {
  if (!isRecord(error) || !isRecord(error.response)) return false
  const status = error.response.status
  return typeof status === 'number' && status >= 500
}

type StaleBundleListener = (reason: StaleBundleReason) => void

let detectedReason: StaleBundleReason | null = null
const listeners = new Set<StaleBundleListener>()

/** Record that this bundle is stale. Only the first detection notifies. */
export function markStaleBundle(reason: StaleBundleReason): void {
  if (detectedReason) return
  detectedReason = reason
  for (const listener of listeners) listener(reason)
}

/** Subscribe to the first detection; replays it if it already happened. */
export function subscribeStaleBundle(
  listener: StaleBundleListener
): () => void {
  listeners.add(listener)
  if (detectedReason) listener(detectedReason)
  return () => {
    listeners.delete(listener)
  }
}

export function resetStaleBundleForTests(): void {
  detectedReason = null
  listeners.clear()
}

/** Compare the build id carried by any response against this bundle's. */
export function observeServerBuild(
  headers: unknown,
  clientBuildId: string = CLIENT_BUILD_ID
): void {
  if (isBuildMismatch(readServerBuildId(headers), clientBuildId)) {
    markStaleBundle('build-mismatch')
  }
}

/** Classify a failed request and record the result. */
export function noteFailedRequest(
  error: unknown,
  clientBuildId: string = CLIENT_BUILD_ID
): StaleBundleReason | null {
  const reason = getStaleBundleReason(error, clientBuildId)
  if (reason) markStaleBundle(reason)
  return reason
}
