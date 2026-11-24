/** The real-client network rig for auth-ui's journey tests: a real
 * `@speed/api-client` over a fetch stand-in, so the 401-refresh leg runs in
 * the client itself. */

import type { AccessTokenStore } from '@speed/api-client'
import type { AuthSession } from '@speed/auth-core'
import {
  makeRealClientRig as makeRig,
} from '@speed/test-utils/real-client'
import type { ObservedRequest } from '@speed/test-utils/real-client'

export {
  errorResponse,
  jsonResponse,
} from '@speed/test-utils/real-client'

/** A request as the fetch stand-in observed it. */
export interface RealCall {
  readonly method: string
  /** The request path without the base URL or query string. */
  readonly path: string
  /** The authorization header value, or null for a credential-less request. */
  readonly authorization: string | null
  /** The serialized JSON request body, or null when the request carried
   * none. */
  readonly rawBody: string | null
}

/** One scripted endpoint: a genuine Response, immediately or deferred. */
export type RealResponder = (
  call: RealCall,
) => Response | Promise<Response>

export interface RealClientRig {
  /** The session the journey drives; attached to the hooks with
   * attachSession before rendering. */
  readonly session: AuthSession
  /** The access-token store the client and the session share. */
  readonly store: AccessTokenStore
  /** Every request observed, in order. */
  readonly calls: RealCall[]
}

function projectCall(request: ObservedRequest): RealCall {
  return {
    method: request.method,
    path: request.path,
    authorization: request.authorization,
    rawBody: request.rawBody,
  }
}

/**
 * Binds a real client whose fetch stand-in answers from the script and returns
 * the session over the same store. Each call binds anew: the runtime seam is
 * last-bind-wins.
 */
export function makeRealClientRig(respond: RealResponder): RealClientRig {
  const { session, store, calls } = makeRig(respond, projectCall)
  return { session, store, calls }
}
