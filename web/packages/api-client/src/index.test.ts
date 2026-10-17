/**
 * Pins the public entry of @speed/api-client: the runtime surface is
 * exactly the documented exports (a drift changes this list and fails
 * the test), and the type surface is compile-checked -- the
 * shape-drift guards below are @ts-expect-error lines that fail the
 * package typecheck the moment the constraint they pin stops erroring.
 */

import { describe, expect, it } from 'vitest'
import * as apiClientModule from './index'
import {
  createConsoleReporter,
  createMemoryAccessTokenStore,
} from './index'
import type {
  AccessTokenStore,
  ApiErrorInit,
  ClientOptions,
  EventStreamHandle,
  EventStreamOpener,
  EventStreamOptions,
  FieldError,
  HttpMethod,
  Reporter,
  RequestFn,
  RequestOptions,
  RetryPolicy,
  ServerSentEvent,
  SpeedClient,
} from './index'

/** The runtime exports, sorted for comparison. */
const RUNTIME_EXPORTS = [
  'ApiError',
  'DEFAULT_RETRY_POLICY',
  'ERROR_CODE_HTML',
  'ERROR_CODE_NETWORK',
  'ERROR_CODE_PROTOCOL',
  'ERROR_CODE_TIMEOUT',
  'createClient',
  'createConsoleReporter',
  'createMemoryAccessTokenStore',
  'httpErrorCode',
  'isApiError',
  'isTransportFailure',
  'retryAfterDelayMs',
  'retryDelayMs',
]

describe('public entry', () => {
  it('exports exactly the documented runtime surface', () => {
    expect(Object.keys(apiClientModule).sort()).toEqual(RUNTIME_EXPORTS)
  })
})

/* ------------------------------------------------------------------ */
/* Type-surface pins: every line below compiles and keeps the          */
/* documented shapes honest. Removing or renaming an exported field or */
/* loosening a type breaks the typecheck -- loudly, in CI.             */
/* ------------------------------------------------------------------ */

/** Valid, fully-typed values against each documented type. */
const accessTokenStore: AccessTokenStore = createMemoryAccessTokenStore()
const reporter: Reporter = createConsoleReporter()
const retryPolicy: RetryPolicy = {
  maxAttempts: 1,
  initialDelayMs: 0,
  maxDelayMs: 0,
}
const clientOptions: ClientOptions = {
  baseUrl: 'https://api.example.test',
  fetch: undefined,
  accessTokenStore,
  refreshAccessToken: async () => true,
  timeoutMs: 1000,
  retryPolicy,
  reporter,
}
const requestOptions: RequestOptions = {
  method: 'POST',
  headers: { 'x-request-id': 'req-1' },
  query: { page: 1, flag: true },
  body: { title: 'T' },
  signal: undefined,
}
const errorInit: ApiErrorInit = {
  status: 400,
  code: 'notes.text_required',
  traceId: 'trace-1',
  message: 'Text is required.',
  params: { max: 100 },
  details: [{ field: 'title', code: 'notes.text_required' }],
  attempts: 1,
  cause: undefined,
}
const fieldError: FieldError = {
  field: 'title',
  code: 'notes.text_required',
  params: undefined,
}
const httpMethod: HttpMethod = 'DELETE'
const requestFn: RequestFn = async <T>(
  path: string,
  options?: RequestOptions,
): Promise<T> => {
  void path
  void options
  return undefined as T
}
const serverSentEvent: ServerSentEvent = { event: 'message', data: '{}' }
const eventStreamHandle: EventStreamHandle = { close: () => {} }
const eventStreamOptions: EventStreamOptions = {
  onEvent: () => {},
  signal: undefined,
}
const eventStreamOpener: EventStreamOpener = (path, options) => {
  void path
  void options
  return eventStreamHandle
}
// A SpeedClient is a RequestFn (the streaming member is additive), so a
// host that only issues requests keeps its existing typing. Returning a
// SpeedClient where a RequestFn is expected must typecheck.
const speedClientAsRequestFn: (client: SpeedClient) => RequestFn = (client) =>
  client
void accessTokenStore
void reporter
void retryPolicy
void clientOptions
void requestOptions
void errorInit
void fieldError
void httpMethod
void requestFn
void serverSentEvent
void eventStreamHandle
void eventStreamOptions
void eventStreamOpener
void speedClientAsRequestFn

/** Shape-drift guards: each must keep erroring as long as the pinned
 * constraint holds. The @ts-expect-error comment turns a guard that
 * silently stops erroring into a typecheck failure. */
// @ts-expect-error -- guard: ClientOptions.baseUrl is required
const missingBaseUrl: ClientOptions = {}

// @ts-expect-error -- guard: AccessTokenStore requires both get and set
const missingSetMethod: AccessTokenStore = { get: () => null }

// @ts-expect-error -- guard: RequestOptions.method is the HttpMethod union
const outOfUnionMethod: RequestOptions = { method: 'TRACE' }

// @ts-expect-error -- guard: HttpMethod carries only the spec'd verbs
const outOfUnionHttpMethod: HttpMethod = 'TRACE'

// @ts-expect-error -- guard: RetryPolicy.maxAttempts counts attempts
const nonNumericAttempts: RetryPolicy = { maxAttempts: '3', initialDelayMs: 0, maxDelayMs: 0 }

// @ts-expect-error -- guard: ApiErrorInit.attempts is required
const missingAttempts: ApiErrorInit = { status: 400, code: 'notes.text_required' }

// @ts-expect-error -- guard: FieldError.field is required
const missingFieldName: FieldError = { code: 'notes.text_required' }

// @ts-expect-error -- guard: Reporter requires error and warn
const missingWarnMethod: Reporter = { error: () => {} }

// @ts-expect-error -- guard: EventStreamOptions requires an onEvent callback
const missingOnEvent: EventStreamOptions = {}
void missingBaseUrl
void missingSetMethod
void outOfUnionMethod
void outOfUnionHttpMethod
void nonNumericAttempts
void missingAttempts
void missingFieldName
void missingWarnMethod
void missingOnEvent
