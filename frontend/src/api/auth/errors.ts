/** Context carried by every AuthError — metadata about what triggered the failure. */
export interface AuthErrorContext {
  /** Monotonic counter from auth coordinator, prevents duplicate handling. */
  generation: number;
  /** Endpoint that received the 401 (e.g. "GET /v1/feed"). */
  source: string;
  /** Date.now() when the error was created. */
  timestamp: number;
  /** Full endpoint URL that triggered the error. */
  endpoint?: string;
  /** HTTP method of the failed request (e.g. GET, POST). */
  method?: string;
  /** HTTP status code from the failed request. */
  status?: number;
  /** Backend error code if available in the response. */
  backendCode?: string;
  /** Request ID for tracing / debugging. */
  requestId?: string;
  /** Whether this error is retryable. */
  retryable: boolean;
  /** Original error that caused this auth error. */
  cause?: unknown;
}

/** Classification of an auth-related failure. */
export enum AuthErrorType {
  /** Refresh token expired — user needs to re-login. */
  SESSION_EXPIRED = "SESSION_EXPIRED",
  /** No session cookie present at all. */
  NO_SESSION = "NO_SESSION",
  /** User account was disabled by an admin. */
  ACCOUNT_DISABLED = "ACCOUNT_DISABLED",
  /** Token tampered or unexpected security issue. */
  SECURITY_ERROR = "SECURITY_ERROR",
  /** Network timeout or connection refused (not auth, but classified for handling). */
  NETWORK_ERROR = "NETWORK_ERROR",
  /** 5xx from refresh endpoint (not auth, but classified). */
  SERVER_ERROR = "SERVER_ERROR",
  /** External generation change invalidated a pending request (e.g. logout during refresh). */
  AUTH_GENERATION_CHANGED = "AUTH_GENERATION_CHANGED",
  /** Refresh succeeded but original request cannot be auto-replayed (non-idempotent). */
  AUTH_RECOVERED_RETRY_REQUIRED = "AUTH_RECOVERED_RETRY_REQUIRED",
}

/** Base class for all auth-related errors. */
export class AuthError extends Error {
  readonly type: AuthErrorType;
  readonly context: AuthErrorContext;

  constructor(type: AuthErrorType, context: AuthErrorContext, message?: string) {
    super(message);
    this.name = "AuthError";
    this.type = type;
    this.context = context;
  }
}

// ---- Concrete error classes ----

export class SessionExpiredError extends AuthError {
  constructor(context: AuthErrorContext, message?: string) {
    super(AuthErrorType.SESSION_EXPIRED, context, message);
    this.name = "SessionExpiredError";
  }
}

export class NoSessionError extends AuthError {
  constructor(context: AuthErrorContext, message?: string) {
    super(AuthErrorType.NO_SESSION, context, message);
    this.name = "NoSessionError";
  }
}

export class AccountDisabledError extends AuthError {
  constructor(context: AuthErrorContext, message?: string) {
    super(AuthErrorType.ACCOUNT_DISABLED, context, message);
    this.name = "AccountDisabledError";
  }
}

export class SecurityError extends AuthError {
  constructor(context: AuthErrorContext, message?: string) {
    super(AuthErrorType.SECURITY_ERROR, context, message);
    this.name = "SecurityError";
  }
}

export class NetworkError extends AuthError {
  constructor(context: AuthErrorContext, message?: string) {
    super(AuthErrorType.NETWORK_ERROR, context, message);
    this.name = "NetworkError";
  }
}

export class ServerError extends AuthError {
  constructor(context: AuthErrorContext, message?: string) {
    super(AuthErrorType.SERVER_ERROR, context, message);
    this.name = "ServerError";
  }
}

/**
 * Emitted when an external generation change invalidates a pending request
 * (e.g., logout happened while refresh was in-flight).
 * This is a cancellation, not a failure — NOT terminal.
 */
export class AuthGenerationChangedError extends AuthError {
  /** The generation that replaced the source. */
  readonly currentGeneration: number;
  /** The generation this request was captured at. */
  readonly sourceGeneration: number;

  constructor(
    context: AuthErrorContext,
    message: string | undefined,
    currentGeneration: number,
    sourceGeneration: number,
  ) {
    super(AuthErrorType.AUTH_GENERATION_CHANGED, context, message);
    this.name = "AuthGenerationChangedError";
    this.currentGeneration = currentGeneration;
    this.sourceGeneration = sourceGeneration;
  }
}

/**
 * Refresh succeeded but the original request cannot be auto-replayed
 * (non-idempotent POST/PATCH without idempotency key).
 * Auth is restored — caller just needs to re-submit. NOT terminal.
 */
export class AuthRecoveredRetryRequired extends AuthError {
  /** The original endpoint that needs re-submission. */
  readonly originalEndpoint: string;

  constructor(
    context: AuthErrorContext,
    originalEndpoint: string,
    message?: string,
  ) {
    super(AuthErrorType.AUTH_RECOVERED_RETRY_REQUIRED, context, message);
    this.name = "AuthRecoveredRetryRequired";
    this.originalEndpoint = originalEndpoint;
  }
}

// ---- Type guards ----

/** Returns true if err is an instance of AuthError. */
export function isAuthError(err: unknown): err is AuthError {
  return err instanceof AuthError;
}

/**
 * Returns true for auth errors that cannot self-recover:
 * SESSION_EXPIRED, ACCOUNT_DISABLED, SECURITY_ERROR.
 *
 * NOT terminal:
 * - AuthGenerationChangedError (cancellation, not failure)
 * - AuthRecoveredRetryRequired (auth is restored, just need to re-submit)
 * - NO_SESSION, NETWORK_ERROR, SERVER_ERROR (recoverable)
 */
export function isTerminalAuthError(err: AuthError): boolean {
  return (
    err.type === AuthErrorType.SESSION_EXPIRED ||
    err.type === AuthErrorType.ACCOUNT_DISABLED ||
    err.type === AuthErrorType.SECURITY_ERROR
  );
}
