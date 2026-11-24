import { describe, expect, it } from 'vitest'
import { createClient } from './client.js'
import type { ServerSentEvent } from './event-stream.js'
import { createMemoryAccessTokenStore } from './access-token.js'
import {
  createStandinFetch,
  jsonResponse,
  sequence,
  type StandinCall,
} from '../test-utils/fetch-standin.js'

const BASE_URL = 'https://api.example.test'
const encoder = new TextEncoder()

function sseResponse(chunks: readonly string[], status = 200): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) {
        controller.enqueue(encoder.encode(chunk))
      }
      controller.close()
    },
  })
  return new Response(stream, {
    status,
    headers: { 'content-type': 'text/event-stream' },
  })
}

function openSseResponse(): {
  readonly response: Response
  push(chunk: string): void
  end(): void
} {
  let controller!: ReadableStreamDefaultController<Uint8Array>
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      controller = c
    },
  })
  return {
    response: new Response(stream, {
      status: 200,
      headers: { 'content-type': 'text/event-stream' },
    }),
    push: (chunk) => controller.enqueue(encoder.encode(chunk)),
    end: () => controller.close(),
  }
}

async function tick(): Promise<void> {
  await new Promise((resolve) => setTimeout(resolve, 0))
}

describe('openEventStream', () => {
  it('announces the seam headers and parses frames into events', async () => {
    const tokenStore = createMemoryAccessTokenStore()
    tokenStore.set('tok-1')
    const standin = createStandinFetch(
      sequence(
        sseResponse([
          'event: message\ndata: {"message_id":"m1"}\n\n',
          ': a comment line is ignored\n',
          'event: message\n',
          'data: {"message_id":"m2"}\n',
          'data: second-line\n\n',
        ]),
      ),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: tokenStore,
      tenantProvider: () => 'acme',
      languageProvider: () => 'zh-CN',
    })
    const events: ServerSentEvent[] = []
    let opened = 0
    let closed = 0
    api.openEventStream('/api/v1/notifications/stream', {
      onOpen: () => {
        opened += 1
      },
      onEvent: (event) => events.push(event),
      onClose: () => {
        closed += 1
      },
    })
    await tick()
    await tick()
    expect(opened).toBe(1)
    expect(closed).toBe(1)
    expect(events).toEqual([
      { event: 'message', data: '{"message_id":"m1"}' },
      { event: 'message', data: '{"message_id":"m2"}\nsecond-line' },
    ])
    const call: StandinCall = standin.calls[0]!
    expect(call.url).toBe(`${BASE_URL}/api/v1/notifications/stream`)
    expect(call.method).toBe('GET')
    expect(call.headers.get('accept')).toBe('text/event-stream')
    expect(call.headers.get('authorization')).toBe('Bearer tok-1')
    expect(call.headers.get('x-tenant')).toBe('acme')
    expect(call.headers.get('accept-language')).toBe('zh-CN')
  })

  it('announces the tenant under the configured header name', async () => {
    const standin = createStandinFetch(
      sequence(sseResponse(['event: message\ndata: {"message_id":"m1"}\n\n'])),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => 'acme',
      tenantHeader: 'x-workspace',
    })
    api.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
    })
    await tick()
    await tick()
    const call: StandinCall = standin.calls[0]!
    expect(call.headers.get('x-workspace')).toBe('acme')
    expect(call.headers.has('x-tenant')).toBe(false)
  })

  it('falls back to the default tenant header name when it is empty', async () => {
    const standin = createStandinFetch(
      sequence(sseResponse(['event: message\ndata: {"message_id":"m1"}\n\n'])),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => 'acme',
      tenantHeader: '',
    })
    api.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
    })
    await tick()
    await tick()
    expect(standin.calls[0]!.headers.get('x-tenant')).toBe('acme')
  })

  it('forwards the client credentials mode to the stream fetch, and omits it when unset', async () => {
    const seen: Array<RequestCredentials | undefined> = []
    const fetchStub: typeof fetch = async (_input, init) => {
      seen.push(init?.credentials)
      return sseResponse(['event: message\ndata: {}\n\n'])
    }
    const withCookies = createClient({
      baseUrl: BASE_URL,
      fetch: fetchStub,
      credentials: 'include',
    })
    withCookies.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
    })
    await tick()
    await tick()
    expect(seen[0]).toBe('include')

    const withoutMode = createClient({ baseUrl: BASE_URL, fetch: fetchStub })
    withoutMode.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
    })
    await tick()
    await tick()
    expect(seen[1]).toBeUndefined()
  })

  it('refreshes and retries once on a bearer 401, then streams', async () => {
    const tokenStore = createMemoryAccessTokenStore()
    tokenStore.set('stale')
    let refreshCalled = 0
    const standin = createStandinFetch(
      sequence(
        jsonResponse(401, { code: 'authn.token.expired' }),
        sseResponse(['event: message\ndata: {"message_id":"m1"}\n\n']),
      ),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: tokenStore,
      refreshAccessToken: async () => {
        refreshCalled += 1
        tokenStore.set('fresh')
        return true
      },
    })
    const events: ServerSentEvent[] = []
    api.openEventStream('/api/v1/notifications/stream', {
      onEvent: (event) => events.push(event),
    })
    await tick()
    await tick()
    await tick()
    expect(refreshCalled).toBe(1)
    expect(standin.calls).toHaveLength(2)
    expect(standin.calls[1]!.headers.get('authorization')).toBe('Bearer fresh')
    expect(events).toEqual([{ event: 'message', data: '{"message_id":"m1"}' }])
  })

  it('reports a non-2xx answer through onError with the envelope code', async () => {
    const tokenStore = createMemoryAccessTokenStore()
    tokenStore.set('tok-1')
    const standin = createStandinFetch(
      sequence(jsonResponse(403, { code: 'rbac.permission_denied' })),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: tokenStore,
    })
    let error: unknown
    let closed = 0
    api.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
      onError: (e) => {
        error = e
      },
      onClose: () => {
        closed += 1
      },
    })
    await tick()
    await tick()
    expect(closed).toBe(1)
    expect(error).toMatchObject({ status: 403, code: 'rbac.permission_denied' })
  })

  it('derives the code from an RFC 9457 problem type, like the request path', async () => {
    const standin = createStandinFetch(
      sequence(
        jsonResponse(404, {
          type: 'urn:speed:objectstore:not-found',
          status: 404,
          detail: 'The object is not available.',
        }),
      ),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    let error: unknown
    api.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
      onError: (e) => {
        error = e
      },
    })
    await tick()
    await tick()
    expect(error).toMatchObject({
      status: 404,
      code: 'objectstore.not_found',
    })
  })

  it('fires no callbacks when the caller closes the stream', async () => {
    const open = openSseResponse()
    const standin = createStandinFetch(sequence(open.response))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    let opened = 0
    let errored = 0
    let closed = 0
    const handle = api.openEventStream('/api/v1/notifications/stream', {
      onOpen: () => {
        opened += 1
      },
      onEvent: () => undefined,
      onError: () => {
        errored += 1
      },
      onClose: () => {
        closed += 1
      },
    })
    await tick()
    await tick()
    expect(opened).toBe(1)
    handle.close()
    await tick()
    await tick()
    expect(errored).toBe(0)
    expect(closed).toBe(0)
  })

  it('reports a dropped connection (read failure) through onError', async () => {
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('event: message\ndata: {}\n\n'))
        controller.error(new Error('connection reset'))
      },
    })
    const standin = createStandinFetch(
      sequence(
        new Response(stream, {
          status: 200,
          headers: { 'content-type': 'text/event-stream' },
        }),
      ),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    let error: unknown
    let closed = 0
    api.openEventStream('/api/v1/notifications/stream', {
      onEvent: () => undefined,
      onError: (e) => {
        error = e
      },
      onClose: () => {
        closed += 1
      },
    })
    await tick()
    await tick()
    await tick()
    expect(closed).toBe(1)
    expect(error).toMatchObject({ status: 0, code: 'client.network' })
  })
})
