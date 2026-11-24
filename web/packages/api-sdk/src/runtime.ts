/**
 * The single hand-written source file of @speed/api-sdk: the mutator adapting an
 * orval call to api-client's request function and forwarding it to the bound
 * function. Everything else in src/ is generated.
 */

import type {
  HttpMethod,
  RequestFn,
  RequestOptions,
} from '@speed/api-client'

/** The axios-flavoured call shape the generated mutators receive. */
interface OrvalCall {
  url: string
  method: HttpMethod
  headers?: Readonly<Record<string, string>>
  /** Query parameters, mirroring api-client's query option; array values become repeated parameters. */
  params?: Readonly<
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
  data?: unknown
  /** Raw-byte response; forwarded as the transport's `responseType: 'blob'`. */
  responseType?: 'blob'
  signal?: AbortSignal
}

let boundRequestFn: RequestFn | undefined

/**
 * Binds the host's request function at bootstrap, before any generated hook
 * fires. Rebinding replaces the previous function with no once-guard (last bind
 * wins); a call while unbound fails fast.
 */
export function bindRequestFn(requestFn: RequestFn): void {
  boundRequestFn = requestFn
}

/** Never declares `requireJsonBody`: a consumer that needs a document guards the body itself. */
function forward<T>(call: OrvalCall, omitAccessToken: boolean): Promise<T> {
  const requestFn = boundRequestFn
  if (requestFn === undefined) {
    throw new Error(
      '[speed-api-sdk] no request function bound: call bindRequestFn(createClient(...)) once at bootstrap before any generated hook runs.',
    )
  }
  // responseType is added only when the operation declared it, so an ordinary call keeps its exact shape.
  const options: RequestOptions = {
    method: call.method,
    headers: call.headers,
    query: call.params,
    body: call.data,
    signal: call.signal,
  }
  if (omitAccessToken) {
    options.omitAccessToken = true
  }
  if (call.responseType !== undefined) {
    options.responseType = call.responseType
  }
  return requestFn<T>(call.url, options)
}

/** The mutator every generated operation calls unless the orval config overrides it per operation. */
export function speedRequest<T>(call: OrvalCall): Promise<T> {
  return forward<T>(call, false)
}

/**
 * The credential-less mutator, selected for the token-issuing operation: it
 * must never present an access token and its refusal must stay terminal.
 */
export function speedRequestCredentialless<T>(call: OrvalCall): Promise<T> {
  return forward<T>(call, true)
}
