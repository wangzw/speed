/**
 * real-client.ts -- the real-client network rig for @speed/account-ui's
 * component suites (an operation driven through the wiring a host builds
 * -- a real @speed/api-client createClient over a fetch stand-in
 * answering with genuine Response objects, the memory access-token
 * store, and optionally the session's own refreshAccessToken: () =>
 * session.refresh() -- bound into the api-sdk runtime seam; see
 * @speed/test-utils/real-client's header for the shared rig's contract).
 * Because the transport is real api-client machinery, a 401-refresh leg
 * a journey scripts is exercised by the client itself rather than
 * scripted around, and a suite that wants a raw 401 to surface passes
 * `{ refresh: false }` (no session layer) after planting the bearer token
 * the reads ride (store.set('access-1')).
 *
 * The projection below is this package's recorded call shape: the
 * observed request's JSON-decoded body, `null` when the request carried
 * none or carried an unparseable one -- the suites pin a write's exact
 * payload (the profile PATCH's partial update), so the decoded object is
 * what they assert on.
 */

import { createClient, createMemoryAccessTokenStore } from '@speed/api-client'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createAuthSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import {
  jsonResponse,
  makeRealClientRig as makeRig,
} from '@speed/test-utils/real-client'
import type { ObservedRequest } from '@speed/test-utils/real-client'
import { methodList } from '@speed/test-utils/session-harness'

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
   * request (the refresh leg travels credential-less by declaration). */
  readonly authorization: string | null
  /** The JSON-decoded request body when the request carried one (a
   * string body that parses as JSON), else null -- for the suites that
   * pin a write's exact payload (the profile PATCH's partial update). */
  readonly body: unknown
}

/** One scripted endpoint: answer with a genuine Response, immediately
 * or after a deferred promise (journeys hold an exchange open to assert
 * the pending state). */
export type RealResponder = (
  call: RealCall,
) => Response | Promise<Response>

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

/** The path every account surface reads on mount to learn the deployment's
 * human-verification gate. */
const METHODS_PATH = '/api/v1/auth/methods'

/**
 * Answer the methods read with the off gate when the case did not script
 * it. The sections always read discovery before a send, so an unscripted
 * path would otherwise settle as a failed read (and a React state update
 * outside the case's own assertions); a real server always answers the
 * document.
 */
function withDefaultMethods(respond: RealResponder): RealResponder {
  const fallback = (call: RealCall): Response | undefined =>
    call.method === 'GET' && call.path === METHODS_PATH
      ? jsonResponse(200, methodList([]))
      : undefined
  return (call) => {
    const answer = respond(call) as
      | Response
      | undefined
      | Promise<Response | undefined>
    if (answer instanceof Promise) {
      return answer.then(
        (settled) => (settled ?? fallback(call)) as Response,
      )
    }
    return (answer ?? fallback(call)) as Response
  }
}

/**
 * Binds a real client whose fetch stand-in answers from the script and
 * returns the shared store and the observed calls; with the default
 * session layer it also returns the session (refresh: true). Each call
 * binds anew: the runtime seam is last-bind-wins by contract.
 */
export function makeRealClientRig(respond: RealResponder) {
  const { session, store, calls } = makeRig(
    withDefaultMethods(respond),
    projectCall,
  )
  return { session, store, calls }
}

/**
 * The session-free variant: no auth session layer, so a scripted 401
 * surfaces directly. The store starts empty; a test plants the bearer
 * token the reads ride (store.set('access-1')), exactly the state a real
 * host's sign-in leaves behind.
 */
export function makeRealClientRigWithoutSession(respond: RealResponder) {
  const { store, calls } = makeRig(respond, projectCall, { refresh: false })
  return { store, calls }
}

/** The cookie-session rig: the client declares cookie credentials and
 * binds the session's own restore() as its 401 leg -- the composition a
 * production host builds for a browser session (see @speed/product-shell's
 * bootstrap). The default rig above binds the token leg (session.refresh(),
 * no cookie), which never runs restore() and so cannot exercise what a
 * silent 401 does to the session snapshot. The store starts empty, as a
 * cookie session leaves it. */
export function makeCookieSessionRig(respond: RealResponder): {
  readonly session: AuthSession
  readonly calls: RealCall[]
} {
  const store = createMemoryAccessTokenStore()
  const session = createAuthSession(store)
  const calls: RealCall[] = []
  const fetcher: typeof fetch = async (input, init) => {
    const url = new URL(String(input))
    const call = projectCall({
      method: init?.method ?? 'GET',
      path: url.pathname,
      query: url.search,
      authorization: new Headers(init?.headers).get('authorization'),
      rawBody: typeof init?.body === 'string' ? init.body : null,
    })
    calls.push(call)
    return withDefaultMethods(respond)(call)
  }
  const client: RequestFn = createClient({
    baseUrl: 'https://api.test',
    fetch: fetcher,
    accessTokenStore: store,
    credentials: 'same-origin',
    refreshAccessToken: (issue) => session.restore(issue),
  })
  bindRequestFn(client)
  return { session, calls }
}
