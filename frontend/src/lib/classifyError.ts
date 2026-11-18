import { normalizeAppError, type AppErrorKind } from './appError'

export type ErrorType = Exclude<AppErrorKind, 'permission'> | 'forbidden'

export interface ClassifiedError {
  type: ErrorType
  message: string
  retriable: boolean
  code?: string
  requestId?: string
  fieldErrors?: Record<string, string>
}

/** Adapt the shared wire error to the existing component API. */
export function classifyError(err: unknown): ClassifiedError {
  const error = normalizeAppError(err)
  return {
    type: error.kind === 'permission' ? 'forbidden' : error.kind,
    message: error.message,
    retriable: error.retryable,
    code: error.code,
    requestId: error.requestId,
    fieldErrors: error.fieldErrors,
  }
}
