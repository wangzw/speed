/**
 * request-rig.ts -- the scripted-request rig for the console's shipped
 * operations bundle.
 *
 * The shipped `consoleOperations` reads and writes through the generated
 * @speed/api-sdk surface, whose every call forwards to the request
 * function bound into the runtime seam (bindRequestFn, the same seam a
 * host's real client fills). This rig binds a scripted function there and
 * records the request leg of each generated call (the HTTP method and the
 * path -- query serialization is the transport's, asserted where it
 * matters). It is how the seam's own suite proves which endpoint each
 * member drives, without an HTTP transport.
 *
 * A scripted responder throws on an unpinned path, so a member wired to
 * the wrong endpoint fails the test rather than silently answering.
 */

import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'

/** One request leg the generated surface produced. */
export interface ObservedCall {
  /** The HTTP method of the generated call. */
  readonly method: string
  /** The request path without the base URL or query string. */
  readonly path: string
}

/** One scripted endpoint: the value the generated call resolves with
 * (the operation's typed body), keyed by the request leg. */
export type ConsoleResponder = (call: ObservedCall) => unknown

/**
 * Binds a scripted request function and returns the recorded calls. Each
 * call binds anew: the runtime seam is last-bind-wins by contract.
 */
export function bindConsoleRequest(
  respond: ConsoleResponder,
): ObservedCall[] {
  const calls: ObservedCall[] = []
  const requestFn: RequestFn = (async <T>(
    path: string,
    options?: RequestOptions,
  ): Promise<T> => {
    const method = options?.method ?? 'GET'
    calls.push({ method, path })
    return (await respond({ method, path })) as T
  }) as RequestFn
  bindRequestFn(requestFn)
  return calls
}
