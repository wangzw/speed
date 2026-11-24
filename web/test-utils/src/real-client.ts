/**
 * The shared real-client network rig for the workspace's journey tests: a real
 * @speed/api-client client over a fetch stand-in, optionally over the session's
 * refresh seam (`refresh: false` leaves it out), with each package projecting
 * the observed request into its own recorded call shape, plus the shared
 * answer helpers.
 */

import {
  createClient,
  createMemoryAccessTokenStore,
} from '@speed/api-client'
import type { AccessTokenStore, RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createAuthSession } from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'

export interface ObservedRequest {
  readonly method: string
  readonly path: string
  readonly query: string
  readonly authorization: string | null
  readonly rawBody: string | null
}

export type RealResponder<Call> = (call: Call) => Response | Promise<Response>

export interface RealClientRigOptions {
  readonly refresh?: boolean
}

export interface BoundRealClientRig<Call, Session> {
  readonly store: AccessTokenStore
  readonly session: Session
  readonly api: RequestFn
  readonly calls: Call[]
}

export function makeRealClientRig<Call>(
  respond: RealResponder<Call>,
  project: (request: ObservedRequest) => Call,
): BoundRealClientRig<Call, AuthSession>
export function makeRealClientRig<Call>(
  respond: RealResponder<Call>,
  project: (request: ObservedRequest) => Call,
  options: { readonly refresh: false },
): BoundRealClientRig<Call, null>
export function makeRealClientRig<Call>(
  respond: RealResponder<Call>,
  project: (request: ObservedRequest) => Call,
  options: RealClientRigOptions = {},
): BoundRealClientRig<Call, AuthSession | null> {
  const { refresh = true } = options
  const store = createMemoryAccessTokenStore()
  const session = refresh ? createAuthSession(store) : null
  const calls: Call[] = []
  const fetcher: typeof fetch = async (input, init) => {
    const url = new URL(String(input))
    const call = project({
      method: init?.method ?? 'GET',
      path: url.pathname,
      query: url.search,
      authorization: new Headers(init?.headers).get('authorization'),
      rawBody: typeof init?.body === 'string' ? init.body : null,
    })
    calls.push(call)
    return respond(call)
  }
  const client = createClient({
    baseUrl: 'https://api.test',
    fetch: fetcher,
    accessTokenStore: store,
    ...(session === null ? {} : { refreshAccessToken: () => session.refresh() }),
  })
  bindRequestFn(client)
  return { store, session, api: client, calls }
}

export function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

export function errorResponse(
  status: number,
  code: string,
  traceId = 'trace-1',
): Response {
  return jsonResponse(status, { code, traceId, message: code })
}

export async function signInWithPassword(rig: {
  readonly session: AuthSession
}): Promise<void> {
  const first = await rig.session.startFlow({ method: 'password' })
  if (!('next' in first)) {
    return
  }
  await rig.session.answerFlow({
    seq: first.seq,
    step: 'answer',
    input: {
      identifier: 'owner@example.test',
      password: 'correct-horse-battery-staple',
    },
  })
}
