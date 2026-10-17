/**
 * The ApiError envelope and the reserved client.* code vocabulary.
 *
 * Every speed API answers a non-2xx response with an RFC 9457 problem
 * details object -- the shape pkg/http writes for a module's refusals
 * and for the entry point's own route and body refusals:
 *
 *   { type: string, title?: string, status: number, detail?: string }
 *
 * The machine-readable kind is the problem `type`, `urn:speed:<module>:<code>`
 * (`urn:speed:objectstore:not-found`, `urn:speed:settings:invalid-value`).
 * The client derives the `<module>.<code>` vocabulary the frontend's
 * error-text resolvers whitelist from it -- see
 * {@link moduleCodeFromProblemType} -- normalizing each dash in the code
 * part to an underscore (`objectstore.not_found`, `settings.invalid_value`).
 * A body may instead carry a plain `code` string directly (the
 * `{code, params}` envelope an earlier backend wrote); a `code` that is a
 * string and not in the reserved `client.` namespace then wins over the
 * problem `type`. Either a valid `code` or a `urn:speed:` `type` is enough
 * on its own; a body with neither -- the entry point's own refusal, whose
 * problem carries `about:blank` or no type at all, a vendor problem URI --
 * is not a module error and keeps the reserved `client.http.<status>`
 * fallback. `traceId`, `params`, `message` and `details` are optional,
 * parsed whenever present: `traceId` surfaces on the ApiError and in the
 * reporter's attributes for correlating user reports to server logs, and
 * `message` is an English log-triage fallback that must never be rendered
 * to users (frontends resolve the code through the i18n catalog instead).
 * Responses that do not carry a valid envelope are still errors, but they
 * cannot be mapped onto module error codes -- they get a synthesized code
 * in the reserved `client.` namespace instead, so callers can tell "the
 * API answered a real error" from "the API layer itself broke":
 *
 *   client.network   -- the request never completed (DNS, refused,
 *                       TLS, CORS, mid-body read failure...).
 *   client.timeout   -- the client's own timeout fired and aborted it.
 *   client.http.<n>  -- a non-2xx response without a parseable envelope
 *                       (non-JSON body, or JSON without a `code`
 *                       string).
 *   client.protocol  -- the API JSON contract was violated on either
 *                       side of the wire: a 2xx whose body is not a
 *                       JSON object/array (an empty 2xx too, when the
 *                       request declared `requireJsonBody`), or a
 *                       request body that cannot be JSON-serialized
 *                       (a circular structure, a top-level function
 *                       or symbol, a toJSON() that yields nothing) --
 *                       the latter rejects before anything is sent.
 *   client.html      -- a 2xx whose content type is an HTML document.
 *                       An endpoint that declares a JSON answer was
 *                       served a document instead: an entry point that
 *                       hands an unmatched path to its front-end
 *                       fallback does this, where the API-only
 *                       deployment would answer 404. The code is the
 *                       decidable form of "this path has no API route
 *                       behind it"; it is not a claim about which.
 *
 * Codes from a valid envelope always win over the client.* vocabulary:
 * a 401 carrying `authn.session_expired` surfaces as that code, with
 * `auth: true` marking it as a session/credential problem hosts treat
 * specially (silent refresh, forced sign-out).
 *
 * One parse-time boundary makes "reserved" a mechanism rather than a
 * convention: an envelope whose resolved code -- the `code` it carries, or
 * the code derived from its `urn:speed:` problem `type` -- starts with
 * `client.` is refused (client.ts's parseEnvelope) and the failure is
 * synthesized as `client.http.<status>` instead. Server codes are module-scoped
 * (`authn.*`, `notes.*`, ...), and `client` is no module's domain, so a
 * `client.*` code inside an envelope is a misbehaving backend or an
 * intermediary borrowing the reserved vocabulary -- accepted, it could
 * make a login surface render "request timed out" (its whitelisted
 * `client.timeout` text) for what is actually a server-answered session
 * error. The exported {@link isTransportFailure} predicate is the
 * honest discriminator consumers branch on: an ApiError whose request
 * never reached a usable response carries status 0 -- and an HTTP
 * response always carries a status, so no server answer can forge one.
 */

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

/** Reserved code: the API JSON contract was violated -- see the header
 * for the full list (a 2xx body that is not a JSON object/array, an
 * unsendable request body, ...). */
export const ERROR_CODE_PROTOCOL = 'client.protocol' as const

/** Reserved code: a 2xx carried an HTML document where the operation
 * declares JSON -- the front-end entry document, typically, which a
 * deployment that serves the front end from the API's own endpoint
 * answers for a path no API route claims. */
export const ERROR_CODE_HTML = 'client.html' as const

/** Reserved code for a bare non-2xx response (no valid envelope). */
export function httpErrorCode(status: number): string {
  return `client.http.${status}`
}

/** The RFC 9457 problem `type` this platform writes a module refusal
 * under: `urn:speed:<module>:<code>`. */
const PROBLEM_TYPE_CODE = /^urn:speed:([^:]+):(.+)$/

/**
 * The error code a problem-details `type` denotes, or undefined when the
 * type is not this platform's. A `urn:speed:<module>:<code>` type
 * (`urn:speed:objectstore:not-found`, `urn:speed:settings:invalid-value`)
 * maps to the `<module>.<code>` vocabulary the frontend's error-text
 * resolvers whitelist and the backend's catalogs key on, with each dash in
 * the code part normalized to an underscore (`objectstore.not_found`,
 * `settings.invalid_value`). A type outside the `urn:speed:` namespace --
 * `about:blank`, a vendor problem URI -- denotes no code, so its caller
 * keeps the bare-HTTP `client.http.<status>` fallback. The reserved
 * `client.` namespace is the caller's check, not this function's: a
 * `urn:speed:client:*` type derives a `client.*` code and is refused there
 * exactly like a literal `client.*` `code`.
 */
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
  /** HTTP attempts made for this request (1 = no retry happened). */
  readonly attempts: number
  /** The underlying error for network/timeout/protocol failures. */
  readonly cause?: unknown
}

/** The English diagnostic default for a code with no envelope message. */
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
 * The normalized error every failed request rejects with. Thrown by the
 * request function for envelope errors, bare HTTP errors and transport
 * failures alike, so callers handle exactly one error type -- inspect
 * `code` for a module code (map it through i18n) and the reserved
 * `client.` codes for API-layer failures. `auth` is true exactly for
 * HTTP 401, whatever the envelope said; hosts that configured a
 * refreshAccessToken hook see it only when refresh failed or the
 * retried request was refused again.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly traceId: string | undefined
  readonly params: Readonly<Record<string, unknown>> | undefined
  readonly details: readonly FieldError[] | undefined
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
    this.attempts = init.attempts
    this.auth = init.status === 401
  }
}

/**
 * Type guard for ApiError. `instanceof` alone would miss errors thrown
 * by a second copy of the library (dev-time aliasing); the structural
 * check additionally accepts any error-shaped object carrying the four
 * fields hosts actually branch on, and is what a `catch (error)` site
 * should use before reading `.code` or `.traceId`.
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
 * Whether `value` is a genuine transport-layer failure: a request that
 * never reached a usable response, diagnosed by this client itself with
 * its own `client.network`/`client.timeout` code. `status === 0` is the
 * mechanism that makes the answer trustworthy -- an HTTP response
 * always carries a status (>= 200 for anything real fetch produces),
 * and an envelope can only arrive through a response, so *no*
 * server-answered error can carry status 0. A `client.network` or
 * `client.timeout` code attached to a real status is therefore not this
 * client's synthesis -- client.ts refuses such envelopes at parse time
 * -- but a backend or intermediary borrowing the reserved vocabulary,
 * or a second copy of the library without that refusal, could produce
 * one: answering false keeps such a forgery from rendering as "the
 * request never completed" in consumer surfaces. Protocol refusals
 * answer false
 * whatever their status: `client.protocol` is a contract violation
 * (unsendable request body, malformed 2xx), never a transport failure.
 * Structural acceptance mirrors {@link isApiError}, for errors thrown
 * by a second copy of the library.
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
