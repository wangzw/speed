/**
 * The real-client network rig for account-ui's component suites.
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

export interface RealCall {
  readonly method: string
  readonly path: string
  readonly query: string
  readonly authorization: string | null
  readonly body: unknown
}

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

const METHODS_PATH = '/api/v1/auth/methods'

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

/** The runtime seam is last-bind-wins, so each call binds anew. */
export function makeRealClientRig(respond: RealResponder) {
  const { session, store, calls } = makeRig(
    withDefaultMethods(respond),
    projectCall,
  )
  return { session, store, calls }
}

export function makeRealClientRigWithoutSession(respond: RealResponder) {
  const { store, calls } = makeRig(respond, projectCall, { refresh: false })
  return { store, calls }
}

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
