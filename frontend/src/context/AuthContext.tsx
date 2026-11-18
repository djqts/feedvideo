import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react'
import { toast } from 'sonner'
import api, { authCoordinator, legacyRedirect, sessionCoordination, invalidateSession } from '../api/client'
import type { AuthFailureEvent } from '../api/auth/events'
import { AuthGenerationChangedError, NoSessionError } from '../api/auth/errors'
import { requireAuthMeResponse } from '../api/auth/parse'

/** Discriminated union of all auth states. */
export type AuthStatus =
  | { kind: 'initializing' }
  | { kind: 'authenticated'; uid: string; role: string }
  | { kind: 'anonymous' }
  /** Bootstrap /me failed with network/5xx — can't tell if logged in. */
  | { kind: 'unknown' }
  | { kind: 'account-disabled' }

/** Whether the auth backend is reachable (transient network/5xx tracking). */
export type AuthAvailability = 'available' | 'temporarily-unavailable'

/**
 * Single atomic auth snapshot — status, availability and the coordinator
 * generation always change together, eliminating intermediate-state windows
 * between separate useState updates.
 */
export interface AuthSnapshot {
  status: AuthStatus
  availability: AuthAvailability
  /** Synced with the coordinator's monotonic failure generation. */
  generation: number
}

export interface AuthContextValue {
  /** The full atomic snapshot (status + availability + generation). */
  snapshot: AuthSnapshot
  status: AuthStatus
  availability: AuthAvailability
  // Backward-compat accessors (derived from snapshot.status):
  uid: string | null
  role: string
  loading: boolean
  logout: () => Promise<void>
  /** Re-run the bootstrap /me call (for `unknown` state recovery). */
  retry: () => void
}

const INITIAL_SNAPSHOT: AuthSnapshot = {
  status: { kind: 'initializing' },
  availability: 'available',
  generation: 0,
}

const AuthContext = createContext<AuthContextValue>({
  snapshot: INITIAL_SNAPSHOT,
  status: INITIAL_SNAPSHOT.status,
  availability: INITIAL_SNAPSHOT.availability,
  uid: null,
  role: 'user',
  loading: true,
  logout: async () => {},
  retry: () => {},
})

/** Clears all sessionStorage keys matching the publish-draft:* pattern. */
function clearUserDrafts(): void {
  for (let i = sessionStorage.length - 1; i >= 0; i--) {
    const key = sessionStorage.key(i)
    if (key && key.startsWith('publish-draft:')) {
      sessionStorage.removeItem(key)
    }
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [snapshot, setSnapshot] = useState<AuthSnapshot>(INITIAL_SNAPSHOT)
  const visibleSnapshot = useRef(snapshot)
  visibleSnapshot.current = snapshot
  // Guard against React 18 StrictMode double-invoking the mount effect.
  const bootstrapped = useRef(false)
  // Last coordinator generation handled — dedups replayed events.
  const lastGeneration = useRef(0)
  const meSequence = useRef(0)
  const localGeneration = useRef(0)
  const mounted = useRef(true)
  const pendingIdentity = useRef<{ scope: ReturnType<typeof sessionCoordination.capture>; uid: string | null; generation: number } | null>(null)

  // Confirm only after React has committed the matching visible identity.
  // Transport /me success alone must not open writes from the old UI.
  useLayoutEffect(() => {
    const pending = pendingIdentity.current
    if (!pending || snapshot.availability !== 'available' || snapshot.generation !== pending.generation) return
    const authenticated = snapshot.status.kind === 'authenticated' && snapshot.status.uid === pending.uid
    const anonymous = snapshot.status.kind === 'anonymous' && pending.uid === null
    if (!authenticated && !anonymous) return
    if (!sessionCoordination.current(pending.scope)) { sessionCoordination.synchronize(); return }
    if (anonymous) sessionCoordination.confirmAnonymous(pending.scope)
    else sessionCoordination.confirmIdentity(pending.scope, pending.uid!)
    pendingIdentity.current = null
  }, [snapshot])

  const fetchMe = useCallback(async (peer = false, confirmLogout = false) => {
    const sequence = ++meSequence.current
    const generation = localGeneration.current
    const scope = sessionCoordination.capture()
    const current = () => mounted.current && sequence === meSequence.current && generation === localGeneration.current && sessionCoordination.current(scope)
    try {
      // /me goes through the normal refresh flow (Task 1.6)
      const r = peer || scope.invalidated ? await api.get('/v1/auth/me', { skipAuthRefresh: true }) : await api.get('/v1/auth/me')
      if (!current()) return
      // Another tab may announce logout before its HTTP request clears cookies.
      // This verification must never promote the invalidated local session.
      if (confirmLogout) return
      const identity = requireAuthMeResponse(r.data, { generation, source: '/v1/auth/me', timestamp: Date.now(), retryable: true })
      const confirmedGeneration = authCoordinator.generation
      pendingIdentity.current = { scope, uid: identity.uid, generation: confirmedGeneration }
      setSnapshot((s) => ({
        ...s,
        generation: confirmedGeneration,
        status: {
          kind: 'authenticated',
          uid: identity.uid,
          role: identity.role,
        },
        availability: 'available',
      }))
    } catch (err) {
      if (!current()) return
      // AuthGenerationChangedError: generation advanced during refresh.
      // Don't touch state — another bootstrap cycle / event handler will fix it.
      if (err instanceof AuthGenerationChangedError) {
        return
      }
      const httpStatus = (err as { response?: { status?: number } })?.response
        ?.status
      const body = (err as { response?: { data?: { message?: string; error?: string; code?: unknown } } })
        ?.response?.data
      // RequireCurrentAccount answers 403 {"code":403,"message":"account disabled"}
      // for a banned account. Without this branch the UI would misreport the
      // definitive moderation answer as "auth service unavailable".
      const accountDisabled =
        httpStatus === 403 &&
        (body?.message === 'account disabled' || body?.error === 'account disabled')
      if (accountDisabled) {
        setSnapshot((s) => ({
          ...s,
          generation: authCoordinator.generation,
          status: { kind: 'account-disabled' },
          availability: 'available',
        }))
        return
      }
      if (httpStatus === 401 || err instanceof NoSessionError) {
        const confirmedGeneration = authCoordinator.generation
        // A locally invalidated /me 200 is deliberately hidden; it does not
        // prove cookies were cleared. Only a real anonymous answer can reopen
        // public browsing, after the anonymous UI snapshot commits.
        if (httpStatus === 401 || (err instanceof NoSessionError && !scope.invalidated)) pendingIdentity.current = { scope, uid: null, generation: confirmedGeneration }
        // Definitive answer from the server: no session.
        setSnapshot((s) => ({
          ...s,
          generation: confirmedGeneration,
          status: { kind: 'anonymous' },
          availability: 'available',
        }))
      } else {
        // Network error / 5xx — do NOT misclassify as logged-out.
        setSnapshot((s) => ({
          ...s,
          status: s.status.kind === 'authenticated' ? s.status : { kind: 'unknown' },
          availability: 'temporarily-unavailable',
        }))
      }
    }
  }, [])

  useEffect(() => {
    mounted.current = true
    const unsubscribe = sessionCoordination.subscribe(notice => {
      if (notice.type === 'invalidated' || notice.type === 'changed') {
        pendingIdentity.current = null
        const generation = authCoordinator.advanceGeneration()
        localGeneration.current++
        lastGeneration.current = generation
        meSequence.current++
        setSnapshot({ status: { kind: 'anonymous' }, generation, availability: 'available' })
      }
      void fetchMe(true, notice.type === 'invalidated')
    })
    return () => { mounted.current = false; unsubscribe() }
  }, [fetchMe])

  useEffect(() => {
    if (bootstrapped.current) return
    bootstrapped.current = true
    void fetchMe()
  }, [fetchMe])

  useEffect(() => {
    const unsubscribe = authCoordinator.onAuthFailure(
      (event: AuthFailureEvent) => {
        if (event.generation <= lastGeneration.current) return
        lastGeneration.current = event.generation
        localGeneration.current++
        meSequence.current++
        switch (event.type) {
          case 'session-expired':
            const wasAuthenticated = visibleSnapshot.current.status.kind === 'authenticated'
            setSnapshot((s) => ({
              ...s,
              status: { kind: 'anonymous' },
              generation: event.generation,
            }))
            if (wasAuthenticated) legacyRedirect.toLogin()
            else queueMicrotask(() => {
              // The refresh handler invalidates the current scope after emitting
              // this event. Capture its new scope before confirming anonymous.
              if (mounted.current && visibleSnapshot.current.status.kind !== 'authenticated') void fetchMe(true)
            })
            break
          case 'security-error':
            // Do NOT change identity, do NOT redirect — just warn (Task 1.7)
            setSnapshot((s) => ({
              ...s,
              availability: 'temporarily-unavailable',
              generation: event.generation,
            }))
            toast.error('检测到安全异常，请稍后重试')
            break
          case 'account-disabled':
            setSnapshot((s) => ({
              ...s,
              status: { kind: 'account-disabled' },
              generation: event.generation,
            }))
            break
          case 'no-session':
            setSnapshot((s) => ({
              ...s,
              status: { kind: 'anonymous' },
              generation: event.generation,
            }))
            break
        }
      },
    )
    return unsubscribe
  }, [])

  // User-initiated logout — transactional (Task 1.3)
  // ① Advance generation → ② setAnonymous atomically → ③ clear drafts → ④ fire API
  const logout = useCallback(async () => {
    // ① Advance generation first (invalidates all in-flight requests)
    const nextGen = authCoordinator.advanceGeneration()
    localGeneration.current++
    meSequence.current++
    lastGeneration.current = nextGen
    invalidateSession()

    // ② Atomically set anonymous with new generation
    setSnapshot({
      status: { kind: 'anonymous' },
      availability: 'available',
      generation: nextGen,
    })

    // ③ Clear user-level drafts (sessionStorage)
    clearUserDrafts()

    // ④ 匿名态无需调用退出接口：避免登录页出现“服务端会话可能尚未退出”误报
    if (snapshot.status.kind !== 'authenticated') {
      return
    }

    // Access cookies may already have expired; the UID only locates the
    // server-side refresh capability. Keep the same actor when explicitly retrying.
    const logoutPayload = { user_id: snapshot.status.uid }
    try {
      await api.post('/logout', logoutPayload, { skipAuthRefresh: true })
    } catch (err) {
      toast.error('服务端退出未确认，请稍后重试', {
        action: {
          label: '重试',
          onClick: () => {
            if (authCoordinator.generation !== nextGen) return
            return api.post('/logout', logoutPayload, { skipAuthRefresh: true }).catch(() => {})
          },
        },
        duration: 8000,
      })
      // Do NOT set back to authenticated
    }
  }, [snapshot.status])

  const retry = useCallback(() => {
    void fetchMe()
  }, [fetchMe])

  const value: AuthContextValue = {
    snapshot,
    status: snapshot.status,
    availability: snapshot.availability,
    uid: snapshot.status.kind === 'authenticated' ? snapshot.status.uid : null,
    role:
      snapshot.status.kind === 'authenticated' ? snapshot.status.role : 'user',
    loading: snapshot.status.kind === 'initializing',
    logout,
    retry,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export const useAuth = () => useContext(AuthContext)
