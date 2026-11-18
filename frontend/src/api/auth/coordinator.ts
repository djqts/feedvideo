/**
 * Auth refresh coordination — a single-slot refresh deduplicator plus a
 * central coordinator that classifies refresh failures and broadcasts
 * terminal auth failure events to subscribers.
 */

import {
  AuthErrorType,
  isAuthError,
  isTerminalAuthError,
} from './errors'
import type { AuthError } from './errors'
import type {
  AuthEventHandler,
  AuthFailureEvent,
  AuthFailureType,
} from './events'

// ---------------------------------------------------------------------------
// RefreshOutcome — structured result of a refresh attempt
// ---------------------------------------------------------------------------

/**
 * Structured result of a single refresh attempt.
 *
 * - `success`: refresh completed, the slot is cleared, the caller may retry.
 * - `terminal`: non-recoverable auth failure. The coordinator generation has
 *   been advanced and the slot cleared. The caller should NOT retry.
 * - `external-generation-changed`: the generation changed between when the
 *   caller captured it and when the refresh attempt started (e.g., a logout
 *   happened on another tab). The caller should restart the refresh flow with
 *   the new generation.
 */
export type RefreshOutcome =
  | { kind: 'success' }
  | { kind: 'terminal'; sourceGeneration: number; nextGeneration: number; failure: AuthError }
  | { kind: 'external-generation-changed'; currentGeneration: number }

// ---------------------------------------------------------------------------
// RefreshSlot — generation-gated single-slot refresh deduplication
// ---------------------------------------------------------------------------

/**
 * Single-slot coordination mechanism that prevents concurrent refresh
 * attempts. Only one refresh Promise may be in flight at a time; callers
 * scheduling while one is in flight receive the existing Promise ONLY when
 * the generation matches (prevents stale in-flight promises from being
 * reused after an external generation change such as a logout).
 */
export interface RefreshSlot {
  /**
   * Monotonic generation counter synced with the auth coordinator.
   * Set by the caller before `schedule()` to indicate the expected generation.
   * When `schedule()` is called and an in-flight promise exists but the
   * generation has changed, the old promise is discarded and a new refresh
   * is started.
   */
  generation: number

  /** Returns the current in-flight refresh Promise, or null if none. */
  readonly promise: Promise<RefreshOutcome> | null

  /**
   * Schedules (or dedupes) a refresh function.
   *
   * - If no refresh is in-flight: stores `fn()` and returns its Promise.
   * - If a refresh is in-flight AND `generation` matches the stored
   *   generation: returns the existing Promise (dedup).
   * - If a refresh is in-flight BUT `generation` has changed: discards the
   *   old in-flight Promise, starts a new `fn()`, and returns the new Promise.
   */
  schedule(fn: () => Promise<RefreshOutcome>): Promise<RefreshOutcome>

  /** Clear the slot (when refresh succeeds or terminal failure occurs). */
  clear(): void
}

/** Creates an empty RefreshSlot with generation 0. */
export function createRefreshSlot(): RefreshSlot {
  let _generation = 0
  let _promise: Promise<RefreshOutcome> | null = null
  /** The generation at which the in-flight promise was scheduled. */
  let _scheduledGeneration = 0

  return {
    get generation(): number {
      return _generation
    },
    set generation(v: number) {
      _generation = v
    },
    get promise(): Promise<RefreshOutcome> | null {
      return _promise
    },
    schedule(fn: () => Promise<RefreshOutcome>): Promise<RefreshOutcome> {
      // If in-flight AND generation hasn't changed → dedup
      if (_promise !== null && _generation === _scheduledGeneration) {
        return _promise
      }
      // Otherwise (no in-flight, or generation changed) → start new
      _scheduledGeneration = _generation
      _promise = fn()
      return _promise
    },
    clear(): void {
      _promise = null
    },
  }
}

/**
 * Central coordinator tying RefreshSlot + auth events + error
 * classification together. Emits AuthFailureEvent to subscribers only for
 * terminal failures; transient network/server errors are swallowed.
 */
export interface AuthCoordinator {
  /** Monotonic counter, increments on each terminal failure. */
  readonly generation: number

  /** Register a handler for auth failure events. Returns unsubscribe fn. */
  onAuthFailure(handler: AuthEventHandler): () => void

  /** True when at least one handler is currently registered. */
  hasSubscribers(): boolean

  /** Called when refresh fails. Classifies the error, emits if terminal. */
  handleRefreshFailure(source: string, err: unknown): void

  /** Called when the /me endpoint returns account-disabled status. */
  handleAccountDisabled(source: string): void

  /** Called when the interceptor detects no session cookie. */
  handleNoSession(source: string): void

  /**
   * Advances the monotonic generation counter WITHOUT emitting events.
   * Used by logout to invalidate all in-flight requests atomically —
   * the logout event itself acts as the notification.
   */
  advanceGeneration(): number

  /** Clear all subscribers (for cleanup/testing). */
  destroy(): void
}

/** Maps terminal AuthErrorType values to their AuthFailureType. */
const TERMINAL_TYPE_MAP: Partial<Record<AuthErrorType, AuthFailureType>> = {
  [AuthErrorType.SESSION_EXPIRED]: 'session-expired',
  [AuthErrorType.NO_SESSION]: 'no-session',
  [AuthErrorType.ACCOUNT_DISABLED]: 'account-disabled',
  [AuthErrorType.SECURITY_ERROR]: 'security-error',
}

/** Creates an AuthCoordinator with generation starting at 0. */
export function createAuthCoordinator(): AuthCoordinator {
  let generation = 0
  const handlers = new Set<AuthEventHandler>()

  function emitFailure(type: AuthFailureType, source: string): void {
    generation += 1
    const event: AuthFailureEvent = {
      type,
      generation,
      source,
      timestamp: Date.now(),
    }
    for (const handler of handlers) {
      try {
        handler(event)
      } catch (e) {
        console.error('auth handler error:', e)
        // Continue to next handler — never let one broken handler break others
      }
    }
  }

  return {
    get generation(): number {
      return generation
    },
    onAuthFailure(handler: AuthEventHandler): () => void {
      handlers.add(handler)
      return () => {
        handlers.delete(handler)
      }
    },
    hasSubscribers(): boolean {
      return handlers.size > 0
    },
    handleRefreshFailure(source: string, err: unknown): void {
      if (!isAuthError(err) || !isTerminalAuthError(err)) {
        // Transient (network/server) or unknown errors — do not emit.
        return
      }
      const failureType = TERMINAL_TYPE_MAP[err.type]
      if (failureType === undefined) {
        return
      }
      emitFailure(failureType, source)
    },
    handleAccountDisabled(source: string): void {
      emitFailure('account-disabled', source)
    },
    handleNoSession(source: string): void {
      emitFailure('no-session', source)
    },
    advanceGeneration(): number {
      generation += 1
      return generation
    },
    destroy(): void {
      handlers.clear()
    },
  }
}

// ---------------------------------------------------------------------------
// emitTerminalAuthFailure — single entry point for terminal refresh failures
// ---------------------------------------------------------------------------

/**
 * Validates sourceGeneration, advances coordinator generation, clears the
 * slot, and emits the appropriate auth failure event to subscribers.
 *
 * This is the SINGLE entry point for terminal auth failures originating from
 * the refresh flow. It guarantees that:
 *
 * 1. `sourceGeneration` matches the coordinator's current generation
 *    (prevents duplicate/stale emissions).
 * 2. The coordinator generation is advanced BEFORE the event is emitted.
 * 3. The refresh slot is cleared so future refresh attempts can proceed.
 * 4. The event is emitted to all subscribers.
 *
 * Returns `true` if the failure was emitted, `false` if it was rejected
 * (stale generation or non-terminal error).
 */
export function emitTerminalAuthFailure(
  coordinator: AuthCoordinator,
  slot: RefreshSlot,
  sourceGeneration: number,
  failure: AuthError,
  source: string,
): boolean {
  // Guard 1: only terminal errors get emitted
  if (!isTerminalAuthError(failure)) {
    return false
  }

  // Guard 2: reject stale generations (another event already advanced)
  if (sourceGeneration !== coordinator.generation) {
    return false
  }

  // Clear the slot first — allows future refreshes
  slot.clear()

  // Delegate to handleRefreshFailure which validates the error type,
  // maps it to an AuthFailureType, and calls emitFailure (which advances
  // generation and broadcasts to all subscribers).
  coordinator.handleRefreshFailure(source, failure)

  return true
}
