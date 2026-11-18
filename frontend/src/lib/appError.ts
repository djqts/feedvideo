import { AuthErrorType, isAuthError } from '../api/auth/errors'

export type AppErrorKind = 'network' | 'timeout' | 'permission' | 'notfound' | 'server' | 'validation' | 'auth' | 'auth_recovered' | 'conflict' | 'rate_limit' | 'canceled'

export interface AppError {
  kind: AppErrorKind
  code?: string
  message: string
  requestId?: string
  retryable: boolean
  fieldErrors: Record<string, string>
}

const defaults: Record<AppErrorKind, string> = {
  network: '网络连接失败，请检查网络后重试', timeout: '请求超时，请重试',
  permission: '没有权限访问', notfound: '内容不存在', server: '服务器错误',
  validation: '请检查填写内容', auth: '登录已失效，请重新登录',
  auth_recovered: '登录已恢复，请重新提交', conflict: '状态已变化，请重新读取后修改内容',
  rate_limit: '请求过于频繁，请稍后重试', canceled: '',
}

function record(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' ? value as Record<string, unknown> : {}
}

function safeText(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string' || !value.trim() || value.length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(value)) return undefined
  return value.trim()
}

export function normalizeAppError(error: unknown): AppError {
  const e = record(error)
  const response = record(e.response)
  const data = record(response.data)
  const context = isAuthError(error) ? error.context : undefined
  const status = typeof response.status === 'number' ? response.status : context?.status
  let kind: AppErrorKind
  if (e.code === 'ERR_CANCELED' || e.name === 'AbortError' || (isAuthError(error) && error.type === AuthErrorType.AUTH_GENERATION_CHANGED)) kind = 'canceled'
  else if (e.authRefreshed === true || (isAuthError(error) && error.type === AuthErrorType.AUTH_RECOVERED_RETRY_REQUIRED)) kind = 'auth_recovered'
  else if (isAuthError(error) && [AuthErrorType.SESSION_EXPIRED, AuthErrorType.NO_SESSION, AuthErrorType.SECURITY_ERROR].includes(error.type)) kind = 'auth'
  else if (isAuthError(error) && error.type === AuthErrorType.ACCOUNT_DISABLED) kind = 'permission'
  else if (e.code === 'ECONNABORTED' || e.code === 'ETIMEDOUT') kind = 'timeout'
  else if (isAuthError(error) && error.type === AuthErrorType.SERVER_ERROR) kind = 'server'
  else if (e.code === 'ERR_NETWORK' || (isAuthError(error) && error.type === AuthErrorType.NETWORK_ERROR) || !e.response) kind = 'network'
  else if (status === 400 || status === 422) kind = 'validation'
  else if (status === 401) kind = 'auth'
  else if (status === 403) kind = 'permission'
  else if (status === 404) kind = 'notfound'
  else if (status === 409) kind = 'conflict'
  else if (status === 429) kind = 'rate_limit'
  else kind = 'server'
  const requestId = safeText(data.request_id ?? context?.requestId, 128)
  const fieldErrors: Record<string, string> = {}
  for (const [field, value] of Object.entries(record(data.field_errors)).slice(0, 32)) {
    const message = safeText(value, 500)
    if (/^[a-zA-Z][a-zA-Z0-9_]{0,63}$/.test(field) && message && field !== 'constructor' && field !== 'prototype') fieldErrors[field] = message
  }
  return {
    kind, code: typeof data.code === 'number' && Number.isSafeInteger(data.code) ? String(data.code)
      : safeText(data.code ?? context?.backendCode ?? e.code, 128), requestId,
    message: kind === 'canceled' || kind === 'auth_recovered' ? defaults[kind]
      : safeText(data.message, 1000) ?? safeText(data.error, 1000)
        ?? (!e.response ? safeText(e.message, 1000) : undefined) ?? defaults[kind],
    retryable: ['network', 'timeout', 'server', 'rate_limit', 'auth_recovered'].includes(kind), fieldErrors,
  }
}
