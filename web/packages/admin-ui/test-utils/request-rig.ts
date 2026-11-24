/**
 * The scripted-request rig for the shipped operations bundle: it binds a
 * scripted request function into the api-sdk runtime seam and records each
 * generated call's method, path and query (and body with `recordBody`).
 */

import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'

/** One request leg the generated surface produced. */
export interface ObservedCall {
  /** The HTTP method of the generated call. */
  readonly method: string
  /** The request path without the base URL or query string. */
  readonly path: string
  /** The query parameters the generated call carried, if any. */
  readonly query?: RequestOptions['query']
  /** The request body the generated call carried, when the rig records it. */
  readonly body?: unknown
}

/** One scripted endpoint, keyed by the request leg it answers. */
export type AdminResponder = (call: ObservedCall) => unknown

/** How a rig call records its request leg. */
export interface RequestRigOptions {
  /** Record each call's request body beside its method, path and query. */
  readonly recordBody?: boolean
}

/** Binds a scripted request function and returns the recorded calls; the seam is last-bind-wins. */
export function bindAdminRequest(
  respond: AdminResponder,
  rig: RequestRigOptions = {},
): ObservedCall[] {
  const calls: ObservedCall[] = []
  const requestFn: RequestFn = (async <T>(
    path: string,
    options?: RequestOptions,
  ): Promise<T> => {
    const method = options?.method ?? 'GET'
    const call: ObservedCall = {
      method,
      path,
      query: options?.query,
      ...(rig.recordBody ? { body: options?.body } : {}),
    }
    calls.push(call)
    return (await respond(call)) as T
  }) as RequestFn
  bindRequestFn(requestFn)
  return calls
}
