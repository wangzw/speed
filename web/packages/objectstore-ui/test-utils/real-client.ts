/**
 * The real-client network rig for objectstore-ui's suites: a real
 * @speed/api-client createClient over a fetch stand-in answering with
 * genuine Response objects and the memory access-token store, bound into
 * the api-sdk runtime seam (bindRequestFn). Because the transport is the
 * real api-client machinery, a suite exercises the actual JSON path and
 * the actual binary path -- a Blob upload body sent verbatim, a
 * raw-byte response resolved as a Blob -- rather than scripting around
 * them.
 *
 * Unlike the shared rig (@speed/test-utils/real-client), this one records
 * the request body as-is (a string or a Blob), because the objectstore
 * suites must observe that the upload's byte leg carried the file's bytes
 * as a Blob and not as JSON text. The rig carries no session layer: the
 * store starts empty and a test plants whatever bearer token it wants.
 */

import {
  createClient,
  createMemoryAccessTokenStore,
} from '@speed/api-client'
import type { AccessTokenStore, RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'

/** One request as the fetch stand-in observed it. */
export interface RecordedRequest {
  readonly method: string
  /** The request path without the base URL or query string. */
  readonly path: string
  /** The raw query string, or '' when none was sent. */
  readonly query: string
  /** The authorization header value, or null for a credential-less request. */
  readonly authorization: string | null
  /** The request body: the JSON text, a Blob (the binary upload leg), or null. */
  readonly body: string | Blob | null
}

/** One scripted endpoint: answer with a genuine Response. */
export type Responder = (call: RecordedRequest) => Response | Promise<Response>

export interface ObjectStoreRig {
  /** The access-token store the client reads on every send. */
  readonly store: AccessTokenStore
  /** The bound client, for a test to drive an operation directly. */
  readonly api: RequestFn
  /** Every request observed, in order. */
  readonly calls: RecordedRequest[]
}

/** Binds a real client answering from the script and returns the store,
 * the client and the observed calls. Each call binds anew. */
export function makeObjectStoreRig(respond: Responder): ObjectStoreRig {
  const store = createMemoryAccessTokenStore()
  const calls: RecordedRequest[] = []
  const fetcher: typeof fetch = async (input, init) => {
    const url = new URL(String(input))
    const body = init?.body
    const recorded: RecordedRequest = {
      method: init?.method ?? 'GET',
      path: url.pathname,
      query: url.search,
      authorization: new Headers(init?.headers).get('authorization'),
      body:
        typeof body === 'string'
          ? body
          : body instanceof Blob
            ? body
            : null,
    }
    calls.push(recorded)
    return respond(recorded)
  }
  const client = createClient({
    baseUrl: 'https://objects.test',
    fetch: fetcher,
    accessTokenStore: store,
  })
  bindRequestFn(client)
  return { store, api: client, calls }
}

/** A JSON answer, like the server's object routes. */
export function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

/** A raw-byte answer, like the content route. The bytes are handed to the
 * Response as text (the suite's bytes are textual); a jsdom Blob must not
 * be passed here, since the jsdom environment's Response would stringify
 * it instead of reading its bytes. */
export function blobResponse(text: string, type: string): Response {
  return new Response(text, {
    status: 200,
    headers: { 'content-type': type },
  })
}

/** A 204 answer, like the upload and delete routes. */
export function noContentResponse(): Response {
  return new Response(null, { status: 204 })
}

/** An RFC 9457 problem answer, like the objectstore refusals. */
export function problemResponse(status: number, type: string): Response {
  return new Response(
    JSON.stringify({ type, status, title: 'refused', detail: 'refused' }),
    { status, headers: { 'content-type': 'application/problem+json' } },
  )
}
