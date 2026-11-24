/**
 * @speed/api-client -- the hand-written HTTP runtime under the speed frontend.
 * createClient wires the injectable seams into one request function that rejects
 * an ApiError; the package ships no i18n resources, no storage and no React.
 */

export { createClient } from './client.js'
export type {
  ClientOptions,
  HttpMethod,
  RequestFn,
  RequestOptions,
  SpeedClient,
} from './client.js'
export type {
  EventStreamHandle,
  EventStreamOpener,
  EventStreamOptions,
  ServerSentEvent,
} from './event-stream.js'
export {
  createMemoryAccessTokenStore,
} from './access-token.js'
export type { AccessTokenStore } from './access-token.js'
export {
  ApiError,
  ERROR_CODE_HTML,
  ERROR_CODE_NETWORK,
  ERROR_CODE_PROTOCOL,
  ERROR_CODE_TIMEOUT,
  httpErrorCode,
  isApiError,
  isTransportFailure,
} from './errors.js'
export type { ApiErrorInit, FieldError } from './errors.js'
export {
  DEFAULT_RETRY_POLICY,
  retryAfterDelayMs,
  retryDelayMs,
} from './retry.js'
export type { RetryPolicy } from './retry.js'
export { createConsoleReporter } from './reporter.js'
export type { Reporter } from './reporter.js'
