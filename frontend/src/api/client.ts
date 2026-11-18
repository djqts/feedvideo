/**
 * API Client — Axios instance configuration
 *
 * ## API Path Convention
 * All request paths use the `/v1/...` prefix (e.g., `api.get('/v1/feed')`).
 * Path rewriting happens at the proxy layer:
 *   - Vite and nginx forward business `/api/v1/*` paths unchanged.
 *   - Auth aliases such as `/api/auth/refresh` map to gateway root routes.
 *
 * Therefore, always write `/v1/...` in application code — never `/api/v1/...`.
 * The Axios `baseURL` is `/api`, so the full URL becomes `/api/v1/...` which
 * the backend serves unchanged.
 *
 * ## Instances
 * - `api` (default export): 30s timeout, auto-refresh on 401
 * - `uploadApi`: 600s timeout, shares refresh coordination, does NOT auto-replay multipart
 */

import axios from 'axios'
import type { AxiosError, InternalAxiosRequestConfig } from 'axios'
import { createAuthCoordinator, createRefreshSlot, emitTerminalAuthFailure } from './auth/coordinator'
import type { RefreshOutcome } from './auth/coordinator'
import { SessionExpiredError, AuthGenerationChangedError, NoSessionError } from './auth/errors'
import { createBrowserSessionCoordination } from './auth/sessionCoordination'
import type { SessionScope } from './auth/sessionCoordination'
import { parseAuthUserID, requireAuthMeResponse } from './auth/parse'

declare module 'axios' {
  export interface AxiosRequestConfig {
    /** Opt out of the 401 refresh flow (e.g. /me — 401 means anonymous). */
    skipAuthRefresh?: boolean
    /** Internal: marks a request already replayed once after refresh. */
    _retry?: boolean
    /** @deprecated Kept for compatibility; it does not permit automatic write replay. */
    allowAuthRetry?: boolean
    /** Arbitrary metadata attached by interceptors (e.g. request start time). */
    metadata?: Record<string, unknown>
  }
}

/** Slow-network threshold in milliseconds. */
const SLOW_NETWORK_THRESHOLD_MS = 3000

// The gateway refresh contract needs the last verified UID. This is an identity
// hint only: the HttpOnly refresh cookie is still required and verified server-side.
const REFRESH_USER_KEY = 'feedvideo:refresh-user-id'
let refreshUserID: string | null = null

function rememberRefreshUser(uid: string | null): void {
  refreshUserID = uid
  try {
    if (uid) localStorage.setItem(REFRESH_USER_KEY, uid)
    else localStorage.removeItem(REFRESH_USER_KEY)
  } catch { /* Keep the in-memory hint when browser storage is unavailable. */ }
}

function refreshPayload(): { user_id?: string } {
  let uid = refreshUserID
  try { uid = localStorage.getItem(REFRESH_USER_KEY) } catch { /* Use memory. */ }
  const exactUID = parseAuthUserID(uid)
  return exactUID ? { user_id: exactUID } : {}
}

/**
 * 读取双提交 CSRF token（P2.1）：登录时服务端下发非 HttpOnly 的 csrf_token
 * Cookie，前端在每次非安全方法请求头中回传 X-CSRF-Token。
 */
function getCSRFToken(): string {
  if (typeof document === 'undefined') return ''
  const match = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/)
  if (!match) return ''
  try {
    return decodeURIComponent(match[1])
  } catch {
    // A corrupt cookie must not bypass the normal HTTP error path. The
    // server still rejects a missing/mismatched token on authenticated writes.
    return ''
  }
}

/** 为写请求附加 X-CSRF-Token（安全方法不附加）。 */
function attachCSRFToken(config: InternalAxiosRequestConfig): InternalAxiosRequestConfig {
  const method = (config.method ?? 'get').toLowerCase()
  if (method === 'get' || method === 'head' || method === 'options') return config
  const token = getCSRFToken()
  if (!token) return config
  const headers = config.headers
  if (headers && typeof (headers as { set?: unknown }).set === 'function') {
    ;(headers as { set: (n: string, v: string) => void }).set('X-CSRF-Token', token)
  } else if (headers) {
    ;(headers as Record<string, unknown>)['X-CSRF-Token'] = token
  }
  return config
}

/**
 * Fires a `slow-network` custom event on `window` if the request took longer
 * than the threshold. Called from both success and error response interceptors
 * BEFORE the response/error is returned to the caller.
 */
function checkSlowNetwork(config: InternalAxiosRequestConfig): void {
  const startTime = config.metadata?.startTime as number | undefined
  if (!startTime) return
  const elapsed = Date.now() - startTime
  if (elapsed > SLOW_NETWORK_THRESHOLD_MS) {
    window.dispatchEvent(
      new CustomEvent('slow-network', {
        detail: { duration: elapsed, url: config.url },
      }),
    )
  }
}

const api = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '/api',
  withCredentials: true,
  timeout: 30000,
})

export const uploadApi = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || '/api',
  withCredentials: true,
  timeout: 600000,
})

/** Module-level singletons: refresh dedup slot + auth event coordinator. */
export const authCoordinator = createAuthCoordinator()
const refreshSlot = createRefreshSlot()
export const sessionCoordination = createBrowserSessionCoordination()
export function invalidateSession(): void {
  sessionCoordination.invalidate()
  rememberRefreshUser(null)
}

function staleRequest(config: InternalAxiosRequestConfig): boolean {
  const generation = config.metadata?.authGeneration
  const scope = config.metadata?.sessionScope as SessionScope | undefined
  const actor = config.metadata?.expectedActor
  return (typeof generation === 'number' && generation !== authCoordinator.generation) || (!!scope && !sessionCoordination.current(scope)) ||
    (typeof actor === 'string' && actor !== sessionCoordination.confirmedActor())
}

function generationError(source: string, generation: number) {
  return new AuthGenerationChangedError({ generation, source, timestamp: Date.now(), retryable: false }, undefined, authCoordinator.generation, generation)
}

function publicIdentityRead(method: string, path: string): boolean {
  if (method !== 'get' && method !== 'head') return false
  // Matches gateway public/optional routes. A confirmed anonymous session
  // still cannot open private reads or writes through this exception.
  return ['/v1/videos', '/v1/videos/hot', '/v1/videos/search', '/v1/likes/count', '/v1/feed', '/v1/search', '/v1/search/hot', '/v1/search/suggest'].includes(path) ||
    /^\/v1\/videos\/(?:[a-fA-F0-9]{24}|[1-9]\d*)(?:\/comments)?$/.test(path) ||
    /^\/v1\/recommend\/similar\/(?:[a-fA-F0-9]{24}|[1-9]\d*)$/.test(path) ||
    /^\/v1\/topics\/(?:id\/)?[^/]+$/.test(path) ||
    /^\/v1\/users\/[1-9]\d*\/(?:followers|following)$/.test(path)
}

function captureAuth(config: InternalAxiosRequestConfig) {
  const previousGeneration = authCoordinator.generation
  sessionCoordination.synchronize()
  const method = (config.method ?? 'get').toLowerCase()
  const path = (config.url ?? '').split('?')[0]
  const identityControl = (method === 'get' && path === '/v1/auth/me') ||
    (method === 'post' && ['/v1/auth/login', '/v1/auth/register', '/logout'].includes(path))
  const actor = sessionCoordination.confirmedActor()
  if (!identityControl && !sessionCoordination.identityReady() &&
      !(sessionCoordination.anonymousReady() && publicIdentityRead(method, path))) throw generationError(config.url ?? '', previousGeneration)
  if (!identityControl && !actor && !publicIdentityRead(method, path)) throw generationError(config.url ?? '', previousGeneration)
  if (config._retry && (!config.metadata?.sessionScope || typeof config.metadata.authGeneration !== 'number' ||
      !Object.prototype.hasOwnProperty.call(config.metadata, 'expectedActor') ||
      (!identityControl && actor !== config.metadata.expectedActor))) throw generationError(config.url ?? '', previousGeneration)
  if (config._retry && staleRequest(config)) throw generationError(config.url ?? '', config.metadata?.authGeneration as number)
  if (!config._retry) {
    config.metadata!.authGeneration = authCoordinator.generation
    config.metadata!.sessionScope = sessionCoordination.capture()
    config.metadata!.expectedActor = identityControl ? undefined : actor
    config.metadata!.authActor = actor === 'anonymous' ? undefined : actor ?? refreshPayload().user_id
  }
  // Preserve the captured actor on a read replay. Shared refresh hints and
  // caller-supplied headers cannot replace the UI's confirmed identity.
  const expectedActor = config.metadata!.expectedActor as string | undefined
  if (!identityControl && expectedActor) config.headers.set('X-FeedVideo-Actor', expectedActor, true)
  else config.headers.delete('X-FeedVideo-Actor')
}

function rejectActorMismatch(err: AxiosError) {
  const config = err.config
  if (!config || err.response?.status !== 409 ||
      (err.response.data as { code?: unknown } | undefined)?.code !== 'AUTH_ACTOR_MISMATCH') return
  const scope = config.metadata?.sessionScope as SessionScope | undefined
  if (scope) sessionCoordination.rejectIdentity(scope, config.metadata?.expectedActor as string | undefined)
  return generationError(config.url ?? '', config.metadata?.authGeneration as number)
}

// ---------------------------------------------------------------------------
// Request timing interceptors (slow-network indicator — Task 10.2)
// ---------------------------------------------------------------------------

api.interceptors.request.use((config) => {
  if (!config.metadata) config.metadata = {}
  captureAuth(config)
  config.metadata.startTime = Date.now()
  return attachCSRFToken(config)
})

uploadApi.interceptors.request.use((config) => {
  if (!config.metadata) config.metadata = {}
  captureAuth(config)
  config.metadata.startTime = Date.now()
  return attachCSRFToken(config)
})

/**
 * Transitional safety net until Task 1.5 (AuthContext) subscribes to the
 * coordinator: legacy full-page redirect to the login screen. Kept behind
 * an object indirection so tests can spy on it (jsdom forbids spying on
 * window.location.replace directly).
 */
export const legacyRedirect = {
  toLogin(): void {
    // Safety net: if we're already on /login with ?session=expired, don't
    // redirect again — prevents infinite redirect loops when the server-side
    // cookie clearing hasn't taken effect yet.
    if (
      window.location.pathname === '/login' &&
      window.location.search.includes('session=expired')
    ) {
      return
    }
    // If the current URL already has a redirect param (e.g. we're on the login
    // page from a previous session-expired redirect), extract the ORIGINAL
    // redirect destination. URLSearchParams.get() decodes the value, so we
    // re-encode just the original path — not the extra query params that
    // include "session=expired&redirect=..." which would cause recursive growth.
    const params = new URLSearchParams(window.location.search)
    const existingRedirect = params.get('redirect')
    const redirect = existingRedirect
      ? encodeURIComponent(existingRedirect) // reuse original destination, re-encoded
      : encodeURIComponent(
          window.location.pathname + window.location.search + window.location.hash,
        )
    window.location.replace(`/login?session=expired&redirect=${redirect}`)
  },
}

/**
 * Only GET/HEAD are approved for automatic replay after refresh. A write
 * header or caller flag cannot establish a server idempotency contract.
 * Writes use their explicit caller recovery protocol; no write endpoint is
 * currently registered for automatic replay.
 */
function canAutoReplay(config: InternalAxiosRequestConfig): boolean {
  const method = (config.method ?? 'get').toLowerCase()
  return method === 'get' || method === 'head'
}

/**
 * Schedules (or joins) the single shared refresh attempt. Both `api` and
 * `uploadApi` interceptors call this, so concurrent 401s from either
 * instance dedupe into one POST /api/auth/refresh when generation matches.
 *
 * Returns a structured RefreshOutcome:
 * - `success`: refresh completed, caller may retry.
 * - `terminal`: non-recoverable auth failure — coordinator event already emitted.
 * - `external-generation-changed`: generation changed externally during refresh.
 *
 * On network/server errors (non-terminal), the promise REJECTS with the
 * original Axios error — the caller should NOT retry.
 */
function scheduleSharedRefresh(source: string, scope: SessionScope, actor?: string, expectedActor?: string): Promise<RefreshOutcome> {
  const capturedGeneration = authCoordinator.generation
  const ensureCurrent = () => {
    if (!sessionCoordination.current(scope) || capturedGeneration !== authCoordinator.generation ||
        (expectedActor !== undefined && expectedActor !== sessionCoordination.confirmedActor())) throw generationError(source, capturedGeneration)
  }
  refreshSlot.generation = capturedGeneration
  return refreshSlot.schedule(async () => {
    try {
      ensureCurrent()
      await sessionCoordination.refresh(scope, async () => {
        ensureCurrent()
        await axios.post('/api/auth/refresh', refreshPayload(), {
          withCredentials: true, timeout: 30000,
          headers: { 'X-CSRF-Token': getCSRFToken() },
        })
        ensureCurrent()
      }, async () => {
        // A peer receipt is only a hint; verify current server-owned identity.
        ensureCurrent()
        const response = await axios.get('/api/v1/auth/me', { withCredentials: true, timeout: 30000 })
        const verified = requireAuthMeResponse(response.data, { generation: capturedGeneration, source, timestamp: Date.now(), retryable: true })
        if (!actor || verified.uid !== actor) throw generationError(source, capturedGeneration)
        ensureCurrent()
      })
      ensureCurrent()
      return { kind: 'success' }
    } catch (refreshErr) {
      ensureCurrent()
      const status = (refreshErr as AxiosError | undefined)?.response?.status
      if (status === 401 || status === 403) {
        const sessionErr = new SessionExpiredError({ generation: capturedGeneration, source, timestamp: Date.now(), retryable: false }, '登录已失效，请重新登录')
        const emitted = emitTerminalAuthFailure(authCoordinator, refreshSlot, capturedGeneration, sessionErr, source)
        if (emitted) {
          sessionCoordination.invalidate()
          rememberRefreshUser(null)
          if (!authCoordinator.hasSubscribers()) legacyRedirect.toLogin()
          return { kind: 'terminal', sourceGeneration: capturedGeneration, nextGeneration: authCoordinator.generation, failure: sessionErr }
        }
        throw generationError(source, capturedGeneration)
      }
      throw refreshErr
    } finally {
      if (refreshSlot.generation === capturedGeneration) refreshSlot.clear()
    }
  })
}
api.interceptors.response.use(
  (r) => {
    if (r.config && staleRequest(r.config)) throw generationError(r.config.url ?? '', r.config.metadata?.authGeneration as number)
    if (r.config) checkSlowNetwork(r.config as InternalAxiosRequestConfig)
    if (r.config?.url === '/v1/auth/me') {
      if (sessionCoordination.capture().invalidated) throw new NoSessionError({ generation: authCoordinator.generation, source: '/v1/auth/me', timestamp: Date.now(), retryable: false, status: 401 })
      const identity = requireAuthMeResponse(r.data, { generation: authCoordinator.generation, source: '/v1/auth/me', timestamp: Date.now(), retryable: true })
      rememberRefreshUser(identity.uid)
    } else if (r.config?.url === '/v1/auth/login') {
      sessionCoordination.activate()
      authCoordinator.advanceGeneration()
      rememberRefreshUser(null)
    } else if (r.config?.url === '/logout') {
      rememberRefreshUser(null)
      const scope = r.config.metadata?.sessionScope as SessionScope | undefined
      if (scope) sessionCoordination.confirmAnonymous(scope)
    }
    return r
  },
  async (err: AxiosError) => {
    const config = err.config
    if (config && staleRequest(config)) throw generationError(config.url ?? '', config.metadata?.authGeneration as number)
    if (config) checkSlowNetwork(config)
    const actorMismatch = rejectActorMismatch(err)
    if (actorMismatch) throw actorMismatch
    // Requests that explicitly opt out (e.g. /v1/auth/me — 401 means
    // anonymous, not expired) bypass the refresh flow entirely.
    if (!config || config.skipAuthRefresh || sessionCoordination.capture().invalidated) {
      return Promise.reject(err)
    }
    if (err.response?.status !== 401 || config._retry) {
      return Promise.reject(err)
    }

    const source = `${config.method?.toUpperCase()} ${config.url}`

    let outcome: RefreshOutcome
    try {
      outcome = await scheduleSharedRefresh(source, config.metadata?.sessionScope as SessionScope, config.metadata?.authActor as string | undefined, config.metadata?.expectedActor as string | undefined)
      if (outcome.kind === 'success' && staleRequest(config)) throw generationError(source, config.metadata?.authGeneration as number)
    } catch (refreshErr) {
      return Promise.reject(refreshErr)
    }

    if (outcome.kind === 'terminal') {
      // Auth failure already emitted via coordinator event.
      // Reject with the original request error.
      return Promise.reject(err)
    }

    if (outcome.kind === 'external-generation-changed') {
      // Generation changed externally (e.g., logout on another tab).
      // Reject — the caller should re-initiate if still relevant.
      return Promise.reject(err)
    }

    // outcome.kind === 'success' — refresh succeeded, proceed with replay

    if (!canAutoReplay(config)) {
      // Session is valid again, but replaying a non-idempotent request is
      // unsafe. Flag the error so callers may safely re-submit themselves.
      ;(err as AxiosError & { authRefreshed?: boolean }).authRefreshed = true
      return Promise.reject(err)
    }

    config._retry = true
    return api(config)
  },
)

uploadApi.interceptors.response.use(
  (r) => {
    if (r.config && staleRequest(r.config)) throw generationError(r.config.url ?? '', r.config.metadata?.authGeneration as number)
    if (r.config) checkSlowNetwork(r.config as InternalAxiosRequestConfig)
    return r
  },
  async (err: AxiosError) => {
    const config = err.config
    if (config && staleRequest(config)) throw generationError(config.url ?? '', config.metadata?.authGeneration as number)
    if (config) checkSlowNetwork(config)
    const actorMismatch = rejectActorMismatch(err)
    if (actorMismatch) throw actorMismatch
    // Requests that explicitly opt out (e.g. /v1/auth/me — 401 means
    // anonymous, not expired) bypass the refresh flow entirely.
    if (!config || config.skipAuthRefresh || sessionCoordination.capture().invalidated) {
      return Promise.reject(err)
    }
    if (err.response?.status !== 401 || config._retry) {
      return Promise.reject(err)
    }

    const source = `${config.method?.toUpperCase()} ${config.url}`

    let outcome: RefreshOutcome
    try {
      outcome = await scheduleSharedRefresh(source, config.metadata?.sessionScope as SessionScope, config.metadata?.authActor as string | undefined, config.metadata?.expectedActor as string | undefined)
      if (outcome.kind === 'success' && staleRequest(config)) throw generationError(source, config.metadata?.authGeneration as number)
    } catch (refreshErr) {
      return Promise.reject(refreshErr)
    }

    if (outcome.kind === 'terminal' || outcome.kind === 'external-generation-changed') {
      // Auth failure already handled, or generation changed externally.
      return Promise.reject(err)
    }

    // Refresh succeeded, but multipart bodies may not be re-streamable —
    // NEVER auto-replay uploads. Flag the error so the UI layer can prompt
    // the user to re-initiate the upload ("登录已续期，请重新上传").
    ;(err as AxiosError & { authRefreshed?: boolean }).authRefreshed = true
    return Promise.reject(err)
  },
)

export default api

