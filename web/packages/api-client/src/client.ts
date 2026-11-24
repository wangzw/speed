import {
  ApiError,
  ERROR_CODE_HTML,
  ERROR_CODE_NETWORK,
  ERROR_CODE_PROTOCOL,
  ERROR_CODE_TIMEOUT,
  httpErrorCode,
  moduleCodeFromProblemType,
  type FieldError,
} from './errors.js'
import type { AccessTokenStore } from './access-token.js'
import {
  DEFAULT_RETRY_POLICY,
  retryAfterDelayMs,
  retryDelayMs,
  type RetryPolicy,
} from './retry.js'
import { createConsoleReporter, type Reporter } from './reporter.js'
import { createEventStreamOpener, type EventStreamOpener } from './event-stream.js'
import { applySeamHeaders } from './seams.js'

/** HTTP methods the request function accepts. */
export type HttpMethod =
  | 'GET'
  | 'HEAD'
  | 'OPTIONS'
  | 'POST'
  | 'PUT'
  | 'PATCH'
  | 'DELETE'

/** Per-request options. */
export interface RequestOptions {
  /** HTTP method; GET when omitted. */
  method?: HttpMethod
  /**
   * Extra request headers. `accept` and `content-type` default to application/json
   * when the caller did not set them; the store's token overwrites a
   * caller-supplied `authorization` unless `omitAccessToken` is declared.
   */
  headers?: Readonly<Record<string, string>>
  /**
   * Declares this request store-token-less: the store is never read and no
   * token from it is attached, so the request travels with only the caller's
   * own headers.
   */
  omitAccessToken?: boolean
  /**
   * Query parameters appended to the path; null and undefined entries are
   * skipped and array values become repeated parameters.
   */
  query?: Readonly<
    Record<
      string,
      | string
      | number
      | boolean
      | ReadonlyArray<string | number | boolean>
      | null
      | undefined
    >
  >
  /**
   * The request body. A JSON value is serialized; a Blob, ArrayBuffer or
   * typed array travels verbatim as raw bytes.
   */
  body?: unknown
  /** Read a 2xx body as raw bytes (a Blob) instead of JSON. */
  responseType?: 'blob'
  /**
   * Declares that the 2xx body must carry a JSON document; an empty 2xx then
   * refuses as `client.protocol`, carrying the exchange's real status and
   * attempt count.
   */
  requireJsonBody?: boolean
  /** Caller cancellation: aborting rejects with the raw AbortError, no retry. */
  signal?: AbortSignal
}

/**
 * The request function createClient returns. A 2xx JSON body resolves as `T`,
 * an empty 2xx as undefined, and every failure rejects an ApiError. In TSX,
 * annotate calls as `api<T,>(path)`.
 */
export type RequestFn = <T>(
  path: string,
  options?: RequestOptions,
) => Promise<T>

/**
 * What createClient returns: the request function plus the server-sent event
 * opener built over the same seams. A `SpeedClient` is usable wherever a
 * `RequestFn` is.
 */
export type SpeedClient = RequestFn & {
  /** Opens a server-sent event stream (see event-stream.ts). */
  readonly openEventStream: EventStreamOpener
}

/** Everything createClient needs. */
export interface ClientOptions {
  /**
   * Scheme + host + optional prefix. Request paths must start with '/' and are
   * appended verbatim; trailing slashes are stripped.
   */
  baseUrl: string
  /**
   * The fetch implementation; defaults to `globalThis.fetch` captured at
   * construction. Construction throws when neither is available.
   */
  fetch?: typeof fetch
  /** The bearer-token store; absent, requests go out without Authorization. */
  accessTokenStore?: AccessTokenStore
  /**
   * Silent-401-refresh hook: true means a fresh token was stored and the request
   * may retry once; concurrent 401s share one refresh, and the argument is the
   * round-scoped request function.
   */
  refreshAccessToken?: (issue: RequestFn) => Promise<boolean>
  /**
   * Abort one HTTP exchange (send plus body read) after this many milliseconds;
   * absent, no timeout is armed. Time in the refresh round does not count.
   */
  timeoutMs?: number
  /**
   * The fetch `credentials` mode, passed straight to fetch; absent, the browser
   * default (`same-origin`) applies. `'same-origin'` or `'include'` marks the
   * client cookie-credentialed, making a cookie 401 eligible for the refresh hook.
   */
  credentials?: RequestCredentials
  /** Transient-retry budget and timing; defaults to DEFAULT_RETRY_POLICY. */
  retryPolicy?: RetryPolicy
  /** Structured diagnostics sink; defaults to createConsoleReporter(). */
  reporter?: Reporter
  /**
   * Returns the language requests announce as `accept-language`, or
   * null/empty for none; read per attempt. A caller-supplied `accept-language`
   * is never overwritten.
   */
  languageProvider?: () => string | null
  /**
   * Returns the tenant requests announce, or null/empty for none; read per
   * attempt. A caller-supplied header of the configured name is never
   * overwritten.
   */
  tenantProvider?: () => string | null
  /**
   * The header name the tenant travels under; absent or empty means the
   * `x-tenant` default. The request function and the event-stream opener share
   * it.
   */
  tenantHeader?: string
}

const RETRYABLE_STATUSES = new Set([429, 502, 503, 504])

function isIdempotent(method: HttpMethod): boolean {
  return method === 'GET' || method === 'HEAD' || method === 'OPTIONS'
}

/** A response that arrived before any body was read; let the body settle before disposing of the attempt. */
interface HttpOutcome {
  kind: 'http'
  status: number
  response: Response
  attachedToken: boolean
  readBody(): Promise<BodyReadResult>
  readBlob(): Promise<BlobReadResult>
  cancelBody(): Promise<void>
  pauseTimeout(): void
  resumeTimeout(): void
  dispose(): void
}

type BodyReadResult =
  | { kind: 'text'; text: string }
  | { kind: 'timeout'; cause: unknown }
  | { kind: 'aborted' }
  | { kind: 'failed'; cause: unknown }

type BlobReadResult =
  | { kind: 'blob'; blob: Blob }
  | { kind: 'timeout'; cause: unknown }
  | { kind: 'aborted' }
  | { kind: 'failed'; cause: unknown }

type WireBody = string | Blob | ArrayBuffer | ArrayBufferView

interface FailureOutcome {
  kind: 'network' | 'timeout'
  cause: unknown
}

type AttemptOutcome = HttpOutcome | FailureOutcome

interface Envelope {
  code: string
  traceId: string | undefined
  message: string | undefined
  params: Record<string, unknown> | undefined
  details: FieldError[] | undefined
}

function envelopeCode(candidate: Record<string, unknown>): string | undefined {
  if (typeof candidate.code === 'string') {
    return candidate.code
  }
  if (typeof candidate.type === 'string') {
    return moduleCodeFromProblemType(candidate.type)
  }
  return undefined
}

function parseEnvelope(text: string | null): Envelope | undefined {
  if (text === null || text.trim() === '') {
    return undefined
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(text)
  } catch {
    return undefined
  }
  if (typeof parsed !== 'object' || parsed === null) {
    return undefined
  }
  const candidate = parsed as Record<string, unknown>
  const code = envelopeCode(candidate)
  if (code === undefined || code.startsWith('client.')) {
    return undefined
  }
  const envelope: Envelope = {
    code,
    traceId: undefined,
    message: undefined,
    params: undefined,
    details: undefined,
  }
  if (typeof candidate.traceId === 'string') {
    envelope.traceId = candidate.traceId
  }
  if (typeof candidate.message === 'string') {
    envelope.message = candidate.message
  }
  if (
    typeof candidate.params === 'object' &&
    candidate.params !== null &&
    !Array.isArray(candidate.params)
  ) {
    envelope.params = candidate.params as Record<string, unknown>
  }
  if (Array.isArray(candidate.details)) {
    envelope.details = parseFieldErrors(candidate.details)
  }
  return envelope
}

function parseFieldErrors(entries: unknown[]): FieldError[] {
  const fieldErrors: FieldError[] = []
  for (const entry of entries) {
    if (typeof entry !== 'object' || entry === null) {
      continue
    }
    const candidate = entry as Record<string, unknown>
    if (
      typeof candidate.field !== 'string' ||
      typeof candidate.code !== 'string'
    ) {
      continue
    }
    const params =
      typeof candidate.params === 'object' &&
      candidate.params !== null &&
      !Array.isArray(candidate.params)
        ? (candidate.params as Record<string, unknown>)
        : undefined
    const fieldError: FieldError = {
      field: candidate.field,
      code: candidate.code,
      ...(params === undefined ? {} : { params }),
    }
    fieldErrors.push(fieldError)
  }
  return fieldErrors
}

function serializeBody(body: unknown): string {
  const serialized = JSON.stringify(body)
  if (serialized === undefined) {
    throw new TypeError(
      'The request body does not serialize to a JSON string (a top-level undefined, function, or symbol).',
    )
  }
  return serialized
}

function isBinaryBody(
  value: unknown,
): value is Blob | ArrayBuffer | ArrayBufferView {
  if (typeof Blob !== 'undefined' && value instanceof Blob) {
    return true
  }
  if (value instanceof ArrayBuffer) {
    return true
  }
  return ArrayBuffer.isView(value)
}

function buildUrl(
  baseUrl: string,
  path: string,
  query: RequestOptions['query'],
): string {
  const url = `${baseUrl}${path}`
  if (query === undefined) {
    return url
  }
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null) {
      continue
    }
    if (Array.isArray(value)) {
      for (const element of value) {
        if (element === undefined || element === null) {
          continue
        }
        params.append(key, String(element))
      }
      continue
    }
    params.append(key, String(value))
  }
  const encoded = params.toString()
  if (encoded === '') {
    return url
  }
  if (path.includes('?')) {
    throw programmerError(
      `request path must not carry a query string (${JSON.stringify(path)}): ` +
        'parameters appended through the query option would produce a ' +
        'malformed double-? URL; move those parameters into the query option.',
    )
  }
  return `${url}?${encoded}`
}

function programmerError(message: string): Error {
  return new Error(`[speed-api-client] ${message}`)
}

function retryableOutcome(outcome: AttemptOutcome): boolean {
  if (outcome.kind === 'http') {
    return RETRYABLE_STATUSES.has(outcome.status)
  }
  return true
}

function retryDelayFor(
  outcome: HttpOutcome,
  retryIndex: number,
  policy: RetryPolicy,
): number {
  if (outcome.status === 429 || outcome.status === 503) {
    const header = outcome.response.headers.get('retry-after')
    if (header !== null) {
      const parsed = retryAfterDelayMs(header)
      if (parsed !== null) {
        return Math.min(parsed, policy.maxDelayMs)
      }
    }
  }
  return retryDelayMs(retryIndex, policy)
}

async function readEnvelope(
  outcome: HttpOutcome,
): Promise<Envelope | undefined> {
  const read = await outcome.readBody()
  if (read.kind === 'text') {
    return parseEnvelope(read.text)
  }
  if (read.kind === 'aborted') {
    throw new DOMException('The operation was aborted.', 'AbortError')
  }
  return undefined
}

function retryAfterOf(outcome: HttpOutcome): number | undefined {
  if (outcome.status !== 429 && outcome.status !== 503) {
    return undefined
  }
  return (
    retryAfterDelayMs(outcome.response.headers.get('retry-after')) ?? undefined
  )
}

function envelopeError(
  outcome: HttpOutcome,
  envelope: Envelope | undefined,
  attempts: number,
): ApiError {
  if (envelope !== undefined) {
    return new ApiError({
      status: outcome.status,
      code: envelope.code,
      traceId: envelope.traceId,
      message: envelope.message,
      params: envelope.params,
      details: envelope.details,
      retryAfterMs: retryAfterOf(outcome),
      attempts,
    })
  }
  return new ApiError({
    status: outcome.status,
    code: httpErrorCode(outcome.status),
    retryAfterMs: retryAfterOf(outcome),
    attempts,
  })
}

function failureError(outcome: FailureOutcome, attempts: number): ApiError {
  return new ApiError({
    status: 0,
    code: outcome.kind === 'timeout' ? ERROR_CODE_TIMEOUT : ERROR_CODE_NETWORK,
    attempts,
    cause: outcome.cause,
  })
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  if (signal?.aborted === true) {
    return Promise.reject(
      new DOMException('The operation was aborted.', 'AbortError'),
    )
  }
  return new Promise<void>((resolve, reject) => {
    const onAbort = (): void => {
      clearTimeout(timer)
      signal?.removeEventListener('abort', onAbort)
      reject(new DOMException('The operation was aborted.', 'AbortError'))
    }
    const timer = setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

/** Rejects with the raw AbortError when `signal` aborts; the awaited promise itself is never cancelled. */
function raceWithAbort<T>(
  promise: Promise<T>,
  signal?: AbortSignal,
): Promise<T> {
  if (signal?.aborted === true) {
    return Promise.reject(
      new DOMException('The operation was aborted.', 'AbortError'),
    )
  }
  return new Promise<T>((resolve, reject) => {
    const onAbort = (): void => {
      signal?.removeEventListener('abort', onAbort)
      reject(new DOMException('The operation was aborted.', 'AbortError'))
    }
    promise.then(
      (value) => {
        signal?.removeEventListener('abort', onAbort)
        resolve(value)
      },
      (cause: unknown) => {
        signal?.removeEventListener('abort', onAbort)
        reject(cause)
      },
    )
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

function throwIfAborted(signal: AbortSignal | undefined): void {
  if (signal?.aborted === true) {
    throw new DOMException('The operation was aborted.', 'AbortError')
  }
}

/**
 * Creates the client. Configuration mistakes throw here; every request
 * afterwards rejects ApiError or the caller's own AbortError, never a
 * configuration error.
 */
export function createClient(options: ClientOptions): SpeedClient {
  if (typeof options.baseUrl !== 'string' || options.baseUrl === '') {
    throw programmerError(
      'createClient requires a non-empty baseUrl (host + optional prefix; see the README).',
    )
  }
  // CodeQL's js/polynomial-redos alert on this line is a false positive:
  // /\/+$/ is a single anchored character-class repetition and runs in O(n).
  const baseUrl = options.baseUrl.replace(/\/+$/, '')
  const fetchFn =
    options.fetch ??
    (typeof globalThis.fetch === 'function' ? globalThis.fetch : undefined)
  if (fetchFn === undefined) {
    throw programmerError(
      'no fetch implementation available: pass ClientOptions.fetch (this environment has no global fetch).',
    )
  }
  const tokenStore = options.accessTokenStore
  const retryPolicy = options.retryPolicy ?? DEFAULT_RETRY_POLICY
  if (
    !Number.isInteger(retryPolicy.maxAttempts) ||
    retryPolicy.maxAttempts < 1
  ) {
    throw programmerError(
      'retryPolicy.maxAttempts must be an integer >= 1 (1 disables transient retries).',
    )
  }
  if (
    !Number.isFinite(retryPolicy.initialDelayMs) ||
    retryPolicy.initialDelayMs < 0 ||
    !Number.isFinite(retryPolicy.maxDelayMs) ||
    retryPolicy.maxDelayMs < 0
  ) {
    throw programmerError(
      'retryPolicy delays must be finite, non-negative milliseconds.',
    )
  }
  const timeoutMs = options.timeoutMs
  if (timeoutMs !== undefined && (!Number.isFinite(timeoutMs) || timeoutMs <= 0)) {
    throw programmerError(
      'timeoutMs must be a positive number of milliseconds.',
    )
  }
  const reporter = options.reporter ?? createConsoleReporter()
  const languageProvider = options.languageProvider
  const tenantProvider = options.tenantProvider
  const tenantHeader = options.tenantHeader
  const credentials = options.credentials
  const cookieCredentialed =
    credentials === 'same-origin' || credentials === 'include'

  const attemptOnce = async (
    method: HttpMethod,
    url: string,
    requestOptions: RequestOptions,
    body: WireBody | undefined,
    signal: AbortSignal | undefined,
  ): Promise<AttemptOutcome> => {
    const headers = new Headers(requestOptions.headers ?? {})
    if (!headers.has('accept')) {
      headers.set('accept', 'application/json')
    }
    if (typeof body === 'string' && !headers.has('content-type')) {
      headers.set('content-type', 'application/json')
    }
    const attachedToken = applySeamHeaders(
      headers,
      {
        accessTokenStore: tokenStore,
        languageProvider,
        tenantProvider,
        tenantHeader,
      },
      { omitAccessToken: requestOptions.omitAccessToken === true },
    )

    const controller = new AbortController()
    let timedOut = false
    let disposed = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let timeoutDeadline = 0
    let pausedRemaining: number | undefined
    let notifyAborted: (() => void) | undefined
    // Settles when this attempt is aborted while its body is still being read.
    const abortTrigger = new Promise<void>((resolve) => {
      notifyAborted = resolve
    })
    const fireTimeout = (): void => {
      timedOut = true
      controller.abort()
      notifyAborted?.()
    }
    const armTimeout = (delay: number): void => {
      timeoutDeadline = Date.now() + delay
      timer = setTimeout(fireTimeout, delay)
    }
    if (timeoutMs !== undefined) {
      armTimeout(timeoutMs)
    }
    const pauseTimeout = (): void => {
      if (timer === undefined) {
        return
      }
      clearTimeout(timer)
      timer = undefined
      pausedRemaining = Math.max(0, timeoutDeadline - Date.now())
    }
    const resumeTimeout = (): void => {
      if (pausedRemaining === undefined) {
        return
      }
      const delay = pausedRemaining
      pausedRemaining = undefined
      if (delay <= 0) {
        fireTimeout()
        return
      }
      armTimeout(delay)
    }
    const forwardAbort = (): void => {
      controller.abort()
      notifyAborted?.()
    }
    if (signal !== undefined) {
      signal.addEventListener('abort', forwardAbort, { once: true })
    }
    const dispose = (): void => {
      if (disposed) {
        return
      }
      disposed = true
      if (timer !== undefined) {
        clearTimeout(timer)
      }
      if (signal !== undefined) {
        signal.removeEventListener('abort', forwardAbort)
      }
    }
    const cancelBody = async (response: Response): Promise<void> => {
      try {
        await response.body?.cancel()
      } catch {
      }
    }

    try {
      const response = await fetchFn(url, {
        method,
        headers,
        body: body as BodyInit,
        signal: controller.signal,
        ...(credentials === undefined ? {} : { credentials }),
      })
      const outcome: HttpOutcome = {
        kind: 'http',
        status: response.status,
        response,
        attachedToken,
        readBody: async (): Promise<BodyReadResult> => {
          const reading = response.text()
          const result = await Promise.race<BodyReadResult>([
            reading.then(
              (text): BodyReadResult => ({ kind: 'text', text }),
              (cause: unknown): BodyReadResult => {
                if (timedOut) {
                  return {
                    kind: 'timeout',
                    cause: new DOMException(
                      'The operation was aborted.',
                      'AbortError',
                    ),
                  }
                }
                if (signal?.aborted === true) {
                  return { kind: 'aborted' }
                }
                return { kind: 'failed', cause }
              },
            ),
            abortTrigger.then((): BodyReadResult =>
              timedOut
                ? {
                    kind: 'timeout',
                    cause: new DOMException(
                      'The operation was aborted.',
                      'AbortError',
                    ),
                  }
                : { kind: 'aborted' },
            ),
          ])
          // A read abandoned mid-stream releases nothing by hand: response.text()
          // holds the body's lock, so the controller abort is the release.
          return result
        },
        readBlob: async (): Promise<BlobReadResult> => {
          const reading = response.blob()
          const result = await Promise.race<BlobReadResult>([
            reading.then(
              (blob): BlobReadResult => ({ kind: 'blob', blob }),
              (cause: unknown): BlobReadResult => {
                if (timedOut) {
                  return {
                    kind: 'timeout',
                    cause: new DOMException(
                      'The operation was aborted.',
                      'AbortError',
                    ),
                  }
                }
                if (signal?.aborted === true) {
                  return { kind: 'aborted' }
                }
                return { kind: 'failed', cause }
              },
            ),
            abortTrigger.then((): BlobReadResult =>
              timedOut
                ? {
                    kind: 'timeout',
                    cause: new DOMException(
                      'The operation was aborted.',
                      'AbortError',
                    ),
                  }
                : { kind: 'aborted' },
            ),
          ])
          return result
        },
        cancelBody: () => cancelBody(response),
        pauseTimeout,
        resumeTimeout,
        dispose,
      }
      return outcome
    } catch (error) {
      dispose()
      if (timedOut) {
        return { kind: 'timeout', cause: error }
      }
      if (signal?.aborted === true) {
        throw error
      }
      return { kind: 'network', cause: error }
    }
  }

  // Single-flight refresh: at most one refresh in flight; a completed refresh
  // is not memoized, so the next 401 starts a fresh attempt.
  const refreshHook = options.refreshAccessToken
  let refreshInFlight: Promise<boolean> | null = null
  // True only while the refresh hook's own synchronous invocation is on the
  // stack; a request born then is the round's own and is refused the refresh
  // path.
  let inRefreshHookCall = false
  const refreshOnce =
    refreshHook === undefined
      ? undefined
      : (): Promise<boolean> => {
          if (refreshInFlight === null) {
            refreshInFlight = (async (): Promise<boolean> => {
              try {
                inRefreshHookCall = true
                let hookOutcome: Promise<boolean>
                try {
                  hookOutcome = (async (): Promise<boolean> => {
                    try {
                      return (await refreshHook(issueInRound)) === true
                    } catch {
                      return false
                    }
                  })()
                } finally {
                  inRefreshHookCall = false
                }
                // The flight races no signal; each joining request races it
                // against its own signal.
                return await hookOutcome
              } finally {
                refreshInFlight = null
              }
            })()
          }
          return refreshInFlight
        }

  const runRequest = async <T>(
    path: string,
    requestOptions: RequestOptions = {},
    roundOwned = false,
  ): Promise<T> => {
    // Captured at entry for the 401 guard: a request born inside the refresh
    // hook's invocation is the round's own.
    const bornInsideRefresh = inRefreshHookCall
    if (typeof path !== 'string' || !path.startsWith('/')) {
      throw programmerError(
        `request path must be an absolute path starting with "/" (baseUrl carries host and prefix); got ${JSON.stringify(path)}.`,
      )
    }
    const method = (requestOptions.method ?? 'GET').toUpperCase() as HttpMethod
    const idempotent = isIdempotent(method)
    const url = buildUrl(baseUrl, path, requestOptions.query)
    const signal = requestOptions.signal
    throwIfAborted(signal)

    let body: WireBody | undefined
    if (requestOptions.body !== undefined) {
      if (isBinaryBody(requestOptions.body)) {
        body = requestOptions.body
      } else {
        try {
          body = serializeBody(requestOptions.body)
        } catch (cause) {
          throw new ApiError({
            status: 0,
            code: ERROR_CODE_PROTOCOL,
            attempts: 0,
            message: 'The request body could not be serialized as JSON.',
            cause,
          })
        }
      }
    }

    let attempts = 0
    // maxAttempts bounds transient retries; the refresh round never counts.
    let transientRetries = 0
    let refreshed = false
    for (;;) {
      attempts += 1
      const outcome = await attemptOnce(
        method,
        url,
        requestOptions,
        body,
        signal,
      )
      if (outcome.kind === 'http') {
        try {
          if (signal?.aborted === true) {
            await outcome.cancelBody()
            throwIfAborted(signal)
          }

          if (isSuccessStatus(outcome.status)) {
            if (requestOptions.responseType === 'blob') {
              const read = await outcome.readBlob()
              if (read.kind === 'aborted') {
                throwIfAborted(signal)
                throw new DOMException('The operation was aborted.', 'AbortError')
              }
              if (read.kind !== 'blob') {
                const kind = read.kind === 'timeout' ? 'timeout' : 'network'
                if (
                  idempotent &&
                  transientRetries < retryPolicy.maxAttempts - 1
                ) {
                  transientRetries += 1
                  await sleep(
                    retryDelayMs(transientRetries - 1, retryPolicy),
                    signal,
                  )
                  throwIfAborted(signal)
                  continue
                }
                throw failureError({ kind, cause: read.cause }, attempts)
              }
              throwIfAborted(signal)
              return read.blob as T
            }
            const read = await outcome.readBody()
            if (read.kind === 'aborted') {
              throwIfAborted(signal)
              throw new DOMException('The operation was aborted.', 'AbortError')
            }
            if (read.kind !== 'text') {
              const kind = read.kind === 'timeout' ? 'timeout' : 'network'
              if (idempotent && transientRetries < retryPolicy.maxAttempts - 1) {
                transientRetries += 1
                await sleep(retryDelayMs(transientRetries - 1, retryPolicy), signal)
                throwIfAborted(signal)
                continue
              }
              throw failureError({ kind, cause: read.cause }, attempts)
            }
            const body = read.text
            throwIfAborted(signal)
            if (isHtmlDocument(outcome.response.headers.get('content-type'))) {
              // An HTML document where JSON was declared is what a
              // front-end-serving deployment answers for an unclaimed path.
              throw new ApiError({
                status: outcome.status,
                code: ERROR_CODE_HTML,
                attempts,
              })
            }
            if (body.trim() === '') {
              if (requestOptions.requireJsonBody === true) {
                throw new ApiError({
                  status: outcome.status,
                  code: ERROR_CODE_PROTOCOL,
                  attempts,
                  message:
                    'The endpoint answered an empty 2xx body; expected a JSON document.',
                })
              }
              return undefined as T
            }
            let data: unknown
            try {
              data = JSON.parse(body)
            } catch {
              throw new ApiError({
                status: outcome.status,
                code: ERROR_CODE_PROTOCOL,
                attempts,
                cause: new SyntaxError('2xx body is not valid JSON'),
              })
            }
            if (typeof data !== 'object' || data === null) {
              throw new ApiError({
                status: outcome.status,
                code: ERROR_CODE_PROTOCOL,
                attempts,
                cause: new SyntaxError('2xx body is not a JSON object or array'),
              })
            }
            return data as T
          }

          if (outcome.status === 401) {
            // The hook is for a request that presented a credential: a bearer
            // token, or the ambient cookie when cookie sessions are declared.
            const presentedCredential =
              outcome.attachedToken ||
              (cookieCredentialed && requestOptions.omitAccessToken !== true)
            if (
              !refreshed &&
              refreshOnce !== undefined &&
              presentedCredential &&
              !bornInsideRefresh &&
              !roundOwned
            ) {
              refreshed = true
              outcome.pauseTimeout()
              const refreshedOk = await raceWithAbort(refreshOnce(), signal)
              throwIfAborted(signal)
              if (refreshedOk) {
                await outcome.cancelBody()
                continue
              }
              outcome.resumeTimeout()
              const envelope = await readEnvelope(outcome)
              reporter.warn('access token refresh failed', {
                status: outcome.status,
                ...(envelope === undefined
                  ? {}
                  : envelope.traceId === undefined
                    ? { code: envelope.code }
                    : {
                        code: envelope.code,
                        trace_id: envelope.traceId,
                      }),
              })
              throw envelopeError(outcome, envelope, attempts)
            }
            throw envelopeError(outcome, await readEnvelope(outcome), attempts)
          }

          if (
            idempotent &&
            transientRetries < retryPolicy.maxAttempts - 1 &&
            retryableOutcome(outcome)
          ) {
            await outcome.cancelBody()
            const delay = retryDelayFor(outcome, transientRetries, retryPolicy)
            transientRetries += 1
            await sleep(delay, signal)
            throwIfAborted(signal)
            continue
          }

          throw envelopeError(outcome, await readEnvelope(outcome), attempts)
        } finally {
          outcome.dispose()
        }
      }

      throwIfAborted(signal)
      if (idempotent && transientRetries < retryPolicy.maxAttempts - 1) {
        const delay = retryDelayMs(transientRetries, retryPolicy)
        transientRetries += 1
        await sleep(delay, signal)
        throwIfAborted(signal)
        continue
      }
      throw failureError(outcome, attempts)
    }
  }

  const request: RequestFn = <T>(
    path: string,
    requestOptions?: RequestOptions,
  ): Promise<T> => runRequest<T>(path, requestOptions)

  // The hook's own request function: requests through it belong to the
  // refresh round.
  const issueInRound: RequestFn = <T>(
    path: string,
    requestOptions?: RequestOptions,
  ): Promise<T> => runRequest<T>(path, requestOptions, true)

  const openEventStream = createEventStreamOpener({
    baseUrl,
    fetch: fetchFn,
    accessTokenStore: tokenStore,
    languageProvider,
    tenantProvider,
    tenantHeader,
    refresh: refreshOnce,
    credentials,
  })

  return Object.assign(request, { openEventStream })
}

function isSuccessStatus(status: number): boolean {
  return status >= 200 && status <= 299
}

function isHtmlDocument(contentType: string | null): boolean {
  const mediaType = (contentType ?? '').split(';', 1)[0]?.trim().toLowerCase()
  return mediaType === 'text/html' || mediaType === 'application/xhtml+xml'
}
