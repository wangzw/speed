/** A single field-level validation failure listed in the envelope. */
export interface FieldError {
  /** The field the error attaches to (JSON path when nested). */
  readonly field: string
  /** Module-scoped machine code, e.g. billing.insufficient_credits. */
  readonly code: string
  /** Code interpolation parameters, when the message needs them. */
  readonly params?: Readonly<Record<string, unknown>>
}

/** Reserved code: the request never completed for transport reasons. */
export const ERROR_CODE_NETWORK = 'client.network' as const

/** Reserved code: the request exceeded ClientOptions.timeoutMs. */
export const ERROR_CODE_TIMEOUT = 'client.timeout' as const

/** Reserved code: the API JSON contract was violated in either direction. */
export const ERROR_CODE_PROTOCOL = 'client.protocol' as const

/** Reserved code: a 2xx carried an HTML document where the operation declares JSON. */
export const ERROR_CODE_HTML = 'client.html' as const

/** Reserved code for a bare non-2xx response (no valid envelope). */
export function httpErrorCode(status: number): string {
  return `client.http.${status}`
}

const PROBLEM_TYPE_CODE = /^urn:speed:([^:]+):(.+)$/

export function moduleCodeFromProblemType(type: string): string | undefined {
  const match = PROBLEM_TYPE_CODE.exec(type)
  if (match === null) {
    return undefined
  }
  const module = match[1]
  const code = match[2]
  if (module === undefined || code === undefined) {
    return undefined
  }
  return `${module}.${code.replace(/-/g, '_')}`
}

/** Everything the ApiError constructor needs. */
export interface ApiErrorInit {
  /** HTTP status of the response; 0 when no response arrived. */
  readonly status: number
  /** Module error code from the envelope, or a reserved client.* code. */
  readonly code: string
  /** Server trace id, for reconnecting user reports to server logs. */
  readonly traceId?: string
  /** Envelope message; overrides the synthesized English default. */
  readonly message?: string
  /** Envelope parameters, for hosts that interpolate their own text. */
  readonly params?: Readonly<Record<string, unknown>>
  /** Field-level validation failures, when the envelope carried them. */
  readonly details?: readonly FieldError[]
  /** The Retry-After delay a 429/503 response asked for, in milliseconds. */
  readonly retryAfterMs?: number
  /** HTTP attempts made for this request (1 = no retry happened). */
  readonly attempts: number
  /** The underlying error for network/timeout/protocol failures. */
  readonly cause?: unknown
}

function defaultMessage(code: string, status: number): string {
  if (code === ERROR_CODE_NETWORK) {
    return 'The request failed before a response arrived.'
  }
  if (code === ERROR_CODE_TIMEOUT) {
    return 'The request timed out before a response arrived.'
  }
  if (code === ERROR_CODE_PROTOCOL) {
    return 'The response did not satisfy the API JSON contract.'
  }
  if (code === ERROR_CODE_HTML) {
    return `The server answered HTTP ${status} with an HTML document where the API contract promises JSON.`
  }
  if (code.startsWith('client.http.')) {
    return `The server answered HTTP ${status} without a valid ApiError envelope.`
  }
  return `The API rejected the request (${code}).`
}

/**
 * The normalized error every failed request rejects with. `auth` is true
 * exactly for HTTP 401; `retryAfterMs` carries the wait a 429/503 response's
 * `Retry-After` header asked for.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly traceId: string | undefined
  readonly params: Readonly<Record<string, unknown>> | undefined
  readonly details: readonly FieldError[] | undefined
  readonly retryAfterMs: number | undefined
  readonly attempts: number
  readonly auth: boolean

  constructor(init: ApiErrorInit) {
    super(init.message ?? defaultMessage(init.code, init.status), {
      cause: init.cause,
    })
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.traceId = init.traceId
    this.params = init.params
    this.details = init.details
    this.retryAfterMs = init.retryAfterMs
    this.attempts = init.attempts
    this.auth = init.status === 401
  }
}

/**
 * Type guard for ApiError; also accepts an error-shaped object thrown by a
 * second copy of the library.
 */
export function isApiError(value: unknown): value is ApiError {
  if (value instanceof ApiError) {
    return true
  }
  if (typeof value !== 'object' || value === null) {
    return false
  }
  const candidate = value as Partial<ApiError>
  return (
    typeof candidate.status === 'number' &&
    typeof candidate.code === 'string' &&
    typeof candidate.message === 'string' &&
    typeof candidate.auth === 'boolean'
  )
}

/**
 * Whether `value` is a genuine transport failure: status 0 with
 * `client.network` or `client.timeout`. An HTTP response always carries a
 * status, so no server-answered error can report status 0.
 */
export function isTransportFailure(value: unknown): value is ApiError {
  if (!isApiError(value)) {
    return false
  }
  return (
    value.status === 0 &&
    (value.code === ERROR_CODE_NETWORK || value.code === ERROR_CODE_TIMEOUT)
  )
}
