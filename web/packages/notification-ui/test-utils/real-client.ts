/**
 * real-client.ts -- the real-client network rig for @speed/notification-ui's
 * component suites (an operation driven through the wiring a host builds
 * -- a real @speed/api-client createClient over a fetch stand-in
 * answering with genuine Response objects, the memory access-token store,
 * and optionally the session's own refreshAccessToken: () => session.refresh()
 * -- bound into the api-sdk runtime seam; see @speed/test-utils/real-client's
 * header for the shared rig's contract). Because the transport is real
 * api-client machinery, a 401-refresh leg a journey scripts is exercised
 * by the client itself rather than scripted around, and a suite that
 * wants a raw 401 to surface passes `{ refresh: false }` (no session
 * layer) after planting the bearer token the reads ride
 * (store.set('access-1')).
 *
 * The projection below is this package's recorded call shape: the request
 * method, the path without the base URL, and the JSON-decoded request
 * body (null when none) -- the preference suites pin which channel a
 * toggle PUTs.
 */

import { makeRealClientRig as makeRig } from '@speed/test-utils/real-client'
import type { ObservedRequest } from '@speed/test-utils/real-client'

export {
  errorResponse,
  jsonResponse,
  signInWithPassword,
} from '@speed/test-utils/real-client'

/** A request as the fetch stand-in observed it. */
export interface RealCall {
  readonly method: string
  /** The request path without the base URL. */
  readonly path: string
  /** The raw query string of the request URL, '' when the request sent
   * none. */
  readonly query: string
  /** The authorization header value, or null for a credential-less
   * request. */
  readonly authorization: string | null
  /** The JSON-decoded request body when the request carried one, else
   * null. */
  readonly body: unknown
}

/** One scripted endpoint: answer with a genuine Response, immediately or
 * after a deferred promise. */
export type RealResponder = (call: RealCall) => Response | Promise<Response>

function projectCall(request: ObservedRequest): RealCall {
  let body: unknown = null
  if (request.rawBody !== null && request.rawBody !== '') {
    try {
      body = JSON.parse(request.rawBody)
    } catch {
      body = null
    }
  }
  return {
    method: request.method,
    path: request.path,
    query: request.query,
    authorization: request.authorization,
    body,
  }
}

/**
 * Binds a real client whose fetch stand-in answers from the script and
 * returns the shared store and the observed calls; with the default
 * session layer it also returns the session (refresh: true). Each call
 * binds anew: the runtime seam is last-bind-wins by contract.
 */
export function makeRealClientRig(respond: RealResponder) {
  return makeRig(respond, projectCall)
}

/**
 * The session-free variant: no auth session layer, so a scripted 401
 * surfaces directly. The store starts empty; a test plants the bearer
 * token the reads ride (store.set('access-1')).
 */
export function makeRealClientRigWithoutSession(respond: RealResponder) {
  return makeRig(respond, projectCall, { refresh: false })
}
