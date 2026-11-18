import { NoSessionError, ServerError } from './errors'
import type { AuthErrorContext } from './errors'

export type ParseResult<T> =
  | { ok: true; value: T }
  | { ok: false; reason: 'anonymous' | 'protocol-error'; detail?: string }

export function parseAuthUserID(value: unknown): string | undefined {
  if (typeof value === 'number') return Number.isSafeInteger(value) && value > 0 ? String(value) : undefined
  if (typeof value !== 'string' || !/^[1-9]\d{0,18}$/.test(value) || BigInt(value) > 9223372036854775807n) return undefined
  return value
}

function validRole(role: unknown): role is string {
  return typeof role === 'string' && /^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(role)
}

/**
 * Validate /api/v1/auth/me response at runtime.
 *
 * - {user: null} or 401  → {ok: false, reason: 'anonymous'}
 * - missing uid/role, wrong types → {ok: false, reason: 'protocol-error'}
 * - valid {uid: string, role: string} → {ok: true, value: {uid, role}}
 *
 * Also handles the flat response case where the backend returns
 * {uid, role} directly rather than {user: {uid, role}}.
 */
export function parseAuthMeResponse(
  data: unknown,
): ParseResult<{ uid: string; role: string }> {
  // 1. null/undefined or not an object
  if (data === null || data === undefined || typeof data !== 'object') {
    return { ok: false, reason: 'protocol-error', detail: 'Response is not an object' }
  }

  // Arrays are typeof 'object' — treat as protocol-error
  if (Array.isArray(data)) {
    return { ok: false, reason: 'protocol-error', detail: 'Response is an array, expected object' }
  }

  const obj = data as Record<string, unknown>

  // 2. Check nested user property
  if ('user' in obj) {
    const user = obj.user

    // user is null → anonymous
    if (user === null) {
      return { ok: false, reason: 'anonymous' }
    }

    // user exists but is not an object (or is null — already handled)
    if (typeof user !== 'object' || user === null) {
      return { ok: false, reason: 'protocol-error', detail: 'user field is not an object' }
    }

    const userObj = user as Record<string, unknown>

    const uid = parseAuthUserID(userObj.uid)
    if (!uid) {
      return { ok: false, reason: 'protocol-error', detail: 'uid is missing, empty, or wrong type' }
    }

    // Validate role: must be a string
    if (!validRole(userObj.role)) {
      return { ok: false, reason: 'protocol-error', detail: 'role is missing or wrong type' }
    }

    return { ok: true, value: { uid, role: userObj.role } }
  }

  // 3. Flat fallback: check data.uid directly
  const uid = parseAuthUserID(obj.uid)
  if (uid && validRole(obj.role)) {
    return { ok: true, value: { uid, role: obj.role } }
  }

  return { ok: false, reason: 'protocol-error', detail: 'Missing uid or role in response' }
}

export function requireAuthMeResponse(data: unknown, context: AuthErrorContext): { uid: string; role: string } {
  const result = parseAuthMeResponse(data)
  if (result.ok) return result.value
  if (result.reason === 'anonymous') throw new NoSessionError({ ...context, status: 401, retryable: false }, '未登录')
  throw new ServerError({ ...context, backendCode: 'AUTH_IDENTITY_INVALID', retryable: true }, '登录信息暂不可用，请稍后重试')
}
