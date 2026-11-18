/**
 * Auth event types — used by the auth coordinator to notify subscribers
 * of terminal auth failures (session expired, no session, account disabled,
 * security errors). Consumers use the monotonic `generation` counter to
 * ignore stale events during React re-renders / effect re-runs.
 */

/** Union of all terminal auth failure types. */
export type AuthFailureType =
  | 'session-expired'
  | 'no-session'
  | 'account-disabled'
  | 'security-error'

const VALID_AUTH_FAILURE_TYPES = new Set<string>([
  'session-expired',
  'no-session',
  'account-disabled',
  'security-error',
])

/**
 * Type guard: returns `true` when `value` is a recognized AuthFailureType.
 */
export function isAuthFailureType(
  value: unknown,
): value is AuthFailureType {
  return typeof value === 'string' && VALID_AUTH_FAILURE_TYPES.has(value)
}

/**
 * A terminal auth failure event emitted by the auth coordinator.
 *
 * - `generation` is a monotonic counter; consumers compare it against the
 *   last-seen generation to skip stale events.
 * - `source` identifies the endpoint that triggered the failure.
 * - `timestamp` is `Date.now()` at the moment the event was created.
 */
export interface AuthFailureEvent {
  type: AuthFailureType
  generation: number
  source: string
  timestamp: number
}

/** Handler callback invoked for each AuthFailureEvent emitted by the bus. */
export type AuthEventHandler = (event: AuthFailureEvent) => void

/**
 * Contract that the auth coordinator implements.
 *
 * - `subscribe` registers a handler and returns an unsubscribe function.
 * - `emit` delivers an event to all currently registered handlers.
 */
export interface AuthEventBus {
  subscribe(handler: AuthEventHandler): () => void
  emit(event: AuthFailureEvent): void
}
