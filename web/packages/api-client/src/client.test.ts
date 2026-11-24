import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import {
  createClient,
  createMemoryAccessTokenStore,
  DEFAULT_RETRY_POLICY,
  ERROR_CODE_HTML,
  ERROR_CODE_NETWORK,
  ERROR_CODE_PROTOCOL,
  ERROR_CODE_TIMEOUT,
  isApiError,
  isTransportFailure,
  type ApiError,
  type RequestFn,
  type RetryPolicy,
} from './index'
import { createMemoryReporter } from '../test-utils/memory-reporter'
import {
  blobResponse,
  createStandinFetch,
  hang,
  jsonResponse,
  scriptedStandin,
  textResponse,
  type StandinCall,
  type StandinFetch,
} from '../test-utils/fetch-standin'

const BASE_URL = 'https://api.example.test'

const zeroDelay = (maxAttempts: number): RetryPolicy => ({
  maxAttempts,
  initialDelayMs: 0,
  maxDelayMs: 0,
})

function recorded(standin: StandinFetch, index = 0): StandinCall {
  const call = standin.calls[index]
  if (call === undefined) {
    throw new Error(
      `expected the stand-in to have recorded call #${index + 1}; ` +
        `it recorded ${standin.calls.length}`,
    )
  }
  return call
}

async function expectApiError(promise: Promise<unknown>): Promise<ApiError> {
  let error: unknown
  try {
    await promise
  } catch (caught) {
    error = caught
  }
  if (error === undefined) {
    throw new Error('expected the request to reject, but it resolved')
  }
  if (!isApiError(error)) {
    throw new Error(`expected an ApiError, got ${String(error)}`)
  }
  return error
}

async function expectRawAbort(promise: Promise<unknown>): Promise<void> {
  let error: unknown
  try {
    await promise
  } catch (caught) {
    error = caught
  }
  expect(error).toBeInstanceOf(DOMException)
  if (error instanceof DOMException) {
    expect(error.name).toBe('AbortError')
  }
  expect(isApiError(error)).toBe(false)
}

async function expectSettled(
  isSettled: () => boolean,
  budgetMs = 30_000,
): Promise<void> {
  for (let i = 0; i < 64 && !isSettled(); i += 1) {
    await Promise.resolve()
  }
  const deadline = Date.now() + budgetMs
  while (!isSettled() && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 2))
  }
  expect(isSettled()).toBe(true)
}

function settlementOf(promise: Promise<unknown>): { settled: boolean } {
  const state = { settled: false }
  void promise.then(
    () => {
      state.settled = true
    },
    () => {
      state.settled = true
    },
  )
  return state
}

async function expectSettledNow(isSettled: () => boolean): Promise<void> {
  for (let i = 0; i < 200 && !isSettled(); i += 1) {
    await Promise.resolve()
  }
  expect(isSettled()).toBe(true)
}

function gatedBody(): {
  stream: ReadableStream<Uint8Array>
  release: (text: string) => void
  fail: (cause: unknown) => void
  readonly cancelled: boolean
  readonly pulling: boolean
} {
  let release: (text: string) => void = () => {}
  let fail: (cause: unknown) => void = () => {}
  let cancelled = false
  let pulling = false
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      release = (text: string) => {
        controller.enqueue(new TextEncoder().encode(text))
        controller.close()
      }
      fail = (cause: unknown) => {
        controller.error(cause)
      }
    },
    pull() {
      pulling = true
    },
    cancel() {
      cancelled = true
    },
  })
  return {
    stream,
    release,
    fail,
    get cancelled() {
      return cancelled
    },
    get pulling() {
      return pulling
    },
  }
}

async function waitForReadStart(body: {
  readonly pulling: boolean
}): Promise<void> {
  for (let i = 0; i < 200 && !body.pulling; i += 1) {
    await Promise.resolve()
  }
  expect(body.pulling).toBe(true)
}

const SESSION_EXPIRED = {
  code: 'authn.session_expired',
  traceId: 'trace-1',
}

describe('construction validation', () => {
  const dummyFetch = async (): Promise<Response> => new Response()

  it('rejects an empty baseUrl', () => {
    expect(() => createClient({ baseUrl: '' })).toThrow('[speed-api-client]')
  })

  it('throws at construction when no fetch exists anywhere', () => {
    vi.stubGlobal('fetch', undefined)
    expect(() => createClient({ baseUrl: BASE_URL })).toThrow(
      /no fetch implementation/,
    )
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('rejects malformed retry policies', () => {
    const badPolicies: RetryPolicy[] = [
      { maxAttempts: 0, initialDelayMs: 200, maxDelayMs: 4000 },
      { maxAttempts: 1.5, initialDelayMs: 200, maxDelayMs: 4000 },
      { maxAttempts: 3, initialDelayMs: -1, maxDelayMs: 4000 },
      { maxAttempts: 3, initialDelayMs: 200, maxDelayMs: Number.NaN },
    ]
    for (const retryPolicy of badPolicies) {
      expect(
        () =>
          createClient({
            baseUrl: BASE_URL,
            fetch: dummyFetch,
            retryPolicy,
          }),
        `policy ${JSON.stringify(retryPolicy)}`,
      ).toThrow('[speed-api-client]')
    }
  })

  it('rejects a non-positive timeout', () => {
    for (const timeoutMs of [0, -10, Number.NaN]) {
      expect(
        () =>
          createClient({ baseUrl: BASE_URL, fetch: dummyFetch, timeoutMs }),
        `timeoutMs ${timeoutMs}`,
      ).toThrow('[speed-api-client]')
    }
  })

  it('rejects request paths that do not start with "/"', async () => {
    const api = createClient({ baseUrl: BASE_URL, fetch: dummyFetch })
    await expect(api('notes')).rejects.toThrow(/absolute path/)
  })
})

describe('request shape', () => {
  it('attaches the bearer token and no tenant header without a provider', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('token-1')
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
    })
    await api<{ ok: boolean }>('/notes')
    const call = recorded(standin)
    expect(call.url).toBe(`${BASE_URL}/notes`)
    expect(call.method).toBe('GET')
    expect(call.headers.get('authorization')).toBe('Bearer token-1')
    expect(call.headers.get('accept')).toBe('application/json')
    expect(call.headers.has('content-type')).toBe(false)
    expect([...call.headers.keys()].sort()).toEqual([
      'accept',
      'authorization',
    ])
    for (const name of [
      'x-tenant-id',
      'x-tenant',
      'tenant-id',
      'x-tenant-context',
    ]) {
      expect(call.headers.has(name)).toBe(false)
    }
  })

  it('sends no Authorization header without a token', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await api<{ ok: boolean }>('/public/config')
    expect(recorded(standin).headers.has('authorization')).toBe(false)
  })

  it('honours omitAccessToken: the request goes out credential-less even when the store holds a token', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('store-token')
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
    })
    await api<{ ok: boolean }>('/api/v1/auth/token', {
      method: 'POST',
      omitAccessToken: true,
    })
    const call = recorded(standin)
    expect(call.headers.get('authorization')).toBeNull()
    expect(store.get()).toBe('store-token')
  })

  it('lets the store win over a caller-supplied authorization header', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('store-token')
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
    })
    await api<{ ok: boolean }>('/notes', {
      headers: { authorization: 'Bearer caller-token' },
    })
    expect(recorded(standin).headers.get('authorization')).toBe(
      'Bearer store-token',
    )
  })

  it('honours a caller-supplied accept: application/json is only the default when absent', async () => {
    const standin = scriptedStandin(
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await api<{ ok: boolean }>('/notes', {
      headers: { 'x-request-id': 'req-1', accept: 'text/plain' },
    })
    const call = recorded(standin)
    expect(call.headers.get('x-request-id')).toBe('req-1')
    expect(call.headers.get('accept')).toBe('text/plain')
    await api<{ ok: boolean }>('/other')
    expect(recorded(standin, 1).headers.get('accept')).toBe('application/json')
  })

  it('announces the language provider value and leaves the header absent for null/empty', async () => {
    const standin = scriptedStandin(
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
    )
    let language: string | null = 'zh-CN'
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      languageProvider: () => language,
    })
    await api<{ ok: boolean }>('/notes')
    expect(recorded(standin, 0).headers.get('accept-language')).toBe('zh-CN')

    language = null
    await api<{ ok: boolean }>('/notes')
    expect(recorded(standin, 1).headers.has('accept-language')).toBe(false)

    language = ''
    await api<{ ok: boolean }>('/notes')
    expect(recorded(standin, 2).headers.has('accept-language')).toBe(false)

    const bare = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await bare<{ ok: boolean }>('/notes')
    expect(recorded(standin, 3).headers.has('accept-language')).toBe(false)
  })

  it('never overwrites a caller-supplied accept-language', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      languageProvider: () => 'zh-CN',
    })
    await api<{ ok: boolean }>('/notes', {
      headers: { 'accept-language': 'de-DE' },
    })
    expect(recorded(standin).headers.get('accept-language')).toBe('de-DE')
  })

  it('reads the language per attempt, so a retry carries the switched language', async () => {
    let language = 'zh-CN'
    const standin = scriptedStandin(
      textResponse(503, 'Service Unavailable'),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(2),
      languageProvider: () => language,
    })
    const pending = api<{ ok: boolean }>('/notes')
    language = 'en-US'
    await expect(pending).resolves.toEqual({ ok: true })
    expect(recorded(standin, 0).headers.get('accept-language')).toBe('zh-CN')
    expect(recorded(standin, 1).headers.get('accept-language')).toBe('en-US')
  })

  it('announces the tenant provider value and leaves the header absent for null/empty', async () => {
    const standin = scriptedStandin(
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
      jsonResponse(200, { ok: true }),
    )
    let tenant: string | null = 'acme'
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => tenant,
    })
    await api<{ ok: boolean }>('/objects')
    expect(recorded(standin, 0).headers.get('x-tenant')).toBe('acme')

    tenant = null
    await api<{ ok: boolean }>('/objects')
    expect(recorded(standin, 1).headers.has('x-tenant')).toBe(false)

    tenant = ''
    await api<{ ok: boolean }>('/objects')
    expect(recorded(standin, 2).headers.has('x-tenant')).toBe(false)

    const bare = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await bare<{ ok: boolean }>('/objects')
    expect(recorded(standin, 3).headers.has('x-tenant')).toBe(false)
  })

  it('never overwrites a caller-supplied x-tenant', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => 'acme',
    })
    await api<{ ok: boolean }>('/objects', {
      headers: { 'X-Tenant': 'globex' },
    })
    expect(recorded(standin).headers.get('x-tenant')).toBe('globex')
  })

  it('announces the tenant under the configured header name', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => 'acme',
      tenantHeader: 'x-workspace',
    })
    await api<{ ok: boolean }>('/objects')
    const call = recorded(standin)
    expect(call.headers.get('x-workspace')).toBe('acme')
    expect(call.headers.has('x-tenant')).toBe(false)
  })

  it('falls back to the default name when the configured header is empty', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => 'acme',
      tenantHeader: '',
    })
    await api<{ ok: boolean }>('/objects')
    expect(recorded(standin).headers.get('x-tenant')).toBe('acme')
  })

  it('never overwrites a caller-supplied header of the configured tenant name', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      tenantProvider: () => 'acme',
      tenantHeader: 'x-workspace',
    })
    await api<{ ok: boolean }>('/objects', {
      headers: { 'X-Workspace': 'globex' },
    })
    const call = recorded(standin)
    expect(call.headers.get('x-workspace')).toBe('globex')
    expect(call.headers.has('x-tenant')).toBe(false)
  })

  it('reads the tenant per attempt, so a retry carries the switched tenant', async () => {
    let tenant: string | null = 'acme'
    const standin = scriptedStandin(
      textResponse(503, 'Service Unavailable'),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(2),
      tenantProvider: () => tenant,
    })
    const pending = api<{ ok: boolean }>('/objects')
    tenant = 'globex'
    await expect(pending).resolves.toEqual({ ok: true })
    expect(recorded(standin, 0).headers.get('x-tenant')).toBe('acme')
    expect(recorded(standin, 1).headers.get('x-tenant')).toBe('globex')
  })

  it('strips trailing slashes from baseUrl and appends the path verbatim', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: 'https://api.example.test///',
      fetch: standin.fetch,
    })
    await api<{ ok: boolean }>('/v1/notes/')
    expect(recorded(standin).url).toBe('https://api.example.test/v1/notes/')
  })

  it('encodes query parameters and skips null and undefined entries', async () => {
    const standin = scriptedStandin(jsonResponse(200, []))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await api('/notes', {
      query: {
        page: 2,
        q: 'a b',
        flag: true,
        title: 'x&y',
        skip: null,
        nothing: undefined,
      },
    })
    expect(recorded(standin).url).toBe(
      `${BASE_URL}/notes?page=2&q=a+b&flag=true&title=x%26y`,
    )
  })

  it('serializes JSON bodies with the JSON content type', async () => {
    const standin = scriptedStandin(jsonResponse(201, { id: 'n-1' }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const created = await api<{ id: string }>('/notes', {
      method: 'POST',
      body: { title: 'T', n: 1 },
    })
    expect(created).toEqual({ id: 'n-1' })
    const call = recorded(standin)
    expect(call.method).toBe('POST')
    expect(call.headers.get('content-type')).toBe('application/json')
    expect(call.bodyJson).toEqual({ title: 'T', n: 1 })
  })

  it('rejects a body that cannot be JSON-serialized as a coded client.protocol ApiError', async () => {
    const circular: Record<string, unknown> = {}
    circular.self = circular
    const standin = scriptedStandin(jsonResponse(201, { id: 'n-1' }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(
      api<{ id: string }>('/notes', { method: 'POST', body: circular }),
    )
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
    expect(error.status).toBe(0)
    expect(error.attempts).toBe(0)
    expect(error.cause).toBeInstanceOf(TypeError)
    expect(standin.calls).toHaveLength(0)
  })

  it('refuses a body JSON.stringify cannot serialize, instead of sending the request silently bodyless', async () => {
    for (const body of [
      (): Record<string, string> => ({ title: 'T' }),
      Symbol('no-json'),
    ]) {
      const standin = scriptedStandin(jsonResponse(201, { id: 'n-1' }))
      const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
      const error = await expectApiError(
        api<{ id: string }>('/notes', { method: 'POST', body }),
      )
      expect(error.code, `body ${String(body)}`).toBe(ERROR_CODE_PROTOCOL)
      expect(error.status).toBe(0)
      expect(error.attempts).toBe(0)
      expect(error.cause).toBeInstanceOf(TypeError)
      expect(standin.calls).toHaveLength(0)
    }
  })

  it('sends a binary (Blob) body verbatim, never JSON-serialized', async () => {
    const standin = scriptedStandin(jsonResponse(204))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const bytes = new Blob(['raw-bytes'], { type: 'application/octet-stream' })
    await api('/objects/o-1/content', {
      method: 'PUT',
      headers: { 'content-type': 'application/octet-stream' },
      body: bytes,
    })
    const call = recorded(standin)
    expect(call.method).toBe('PUT')
    expect(call.bodyBlob).toBe(bytes)
    expect(call.bodyText).toBeUndefined()
    expect(call.headers.get('content-type')).toBe('application/octet-stream')
  })

  it('sends an ArrayBuffer body verbatim, never JSON-serialized', async () => {
    const standin = scriptedStandin(jsonResponse(204))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const buffer = new Uint8Array([1, 2, 3]).buffer
    await api('/objects/o-1/content', { method: 'PUT', body: buffer })
    const call = recorded(standin)
    expect(call.bodyText).toBeUndefined()
    expect(call.bodyBlob).toBeNull()
  })

  it('resolves a raw-byte response as a Blob when responseType is blob', async () => {
    const standin = scriptedStandin(
      blobResponse(200, 'the-bytes', 'application/octet-stream'),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const result = await api<Blob>('/objects/o-1/content', {
      responseType: 'blob',
    })
    expect(result).toBeInstanceOf(Blob)
    expect(await result.text()).toBe('the-bytes')
  })

  it('still parses a JSON error envelope for a blob-declaring request', async () => {
    const standin = scriptedStandin(jsonResponse(409, { code: 'objectstore.conflict' }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(
      api('/objects/o-1/content', { responseType: 'blob' }),
    )
    expect(error.status).toBe(409)
    expect(error.code).toBe('objectstore.conflict')
  })

  it('encodes an array query value as repeated parameters, never comma-joined', async () => {
    const standin = scriptedStandin(jsonResponse(200, []))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await api('/notes', {
      query: { tag: ['a', 'b c', 3], keep: 'x' },
    })
    expect(recorded(standin).url).toBe(
      `${BASE_URL}/notes?tag=a&tag=b+c&tag=3&keep=x`,
    )
  })

  it('omitAccessToken skips only the store token: a caller-supplied authorization header survives untouched', async () => {
    for (const probe of [
      { storeToken: null, callerHeader: 'Bearer CALLER' },
      { storeToken: 'store-token', callerHeader: 'Bearer CALLER' },
    ]) {
      const store = createMemoryAccessTokenStore()
      store.set(probe.storeToken)
      const standin = scriptedStandin(jsonResponse(200, { ok: true }))
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        accessTokenStore: store,
      })
      await api('/notes', {
        omitAccessToken: true,
        headers: { authorization: probe.callerHeader },
      })
      expect(recorded(standin).headers.get('authorization')).toBe(
        'Bearer CALLER',
      )
    }
  })

  it('refuses a query option on a path that already carries its own query string', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const promise = api<{ ok: boolean }>('/notes?draft=1', {
      query: { tag: 'a' },
    })
    await expect(promise).rejects.toThrow(
      'request path must not carry a query string',
    )
    expect(standin.calls).toHaveLength(0)
  })

  it('sends a query-bearing path verbatim when no query option is given', async () => {
    const standin = scriptedStandin(jsonResponse(200, { ok: true }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await api<{ ok: boolean }>('/notes?draft=1')
    expect(recorded(standin).url).toBe(`${BASE_URL}/notes?draft=1`)
  })
})

describe('successful responses', () => {
  it('resolves a parsed JSON object', async () => {
    const standin = scriptedStandin(jsonResponse(200, { notes: ['one'] }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await expect(api<{ notes: string[] }>('/notes')).resolves.toEqual({
      notes: ['one'],
    })
  })

  it('resolves a JSON array body', async () => {
    const standin = scriptedStandin(jsonResponse(200, [1, 2]))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await expect(api<number[]>('/ids')).resolves.toEqual([1, 2])
  })

  it('resolves undefined for an empty 2xx body', async () => {
    const standin = scriptedStandin(jsonResponse(204))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await expect(api<undefined>('/notes/n-1')).resolves.toBeUndefined()
  })

  it('treats a whitespace-only 2xx body as an empty success', async () => {
    const standin = scriptedStandin(textResponse(200, ' \n '))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    await expect(api<undefined>('/notes/n-1')).resolves.toBeUndefined()
  })

  it('refuses a 204 as a coded client.protocol error when the request declares requireJsonBody', async () => {
    const standin = scriptedStandin(jsonResponse(204))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(
      api<unknown>('/notes/n-1', { requireJsonBody: true }),
    )
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
    expect(error.status).toBe(204)
    expect(error.attempts).toBe(1)
  })

  it('refuses a whitespace-only 2xx body when the request declares requireJsonBody', async () => {
    const standin = scriptedStandin(textResponse(200, ' \n '))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(
      api<unknown>('/notes/n-1', { requireJsonBody: true }),
    )
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
    expect(error.status).toBe(200)
    expect(error.attempts).toBe(1)
  })

  it('reports the requireJsonBody refusal with the real attempts and status of the exchange', async () => {
    const standin = scriptedStandin(
      textResponse(503, 'Service Unavailable'),
      textResponse(503, 'Service Unavailable'),
      new Response(null, { status: 200 }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(3),
    })
    const error = await expectApiError(
      api<unknown>('/config/public', { requireJsonBody: true }),
    )
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
    expect(error.status).toBe(200)
    expect(error.attempts).toBe(3)
    expect(standin.calls).toHaveLength(3)
  })

  it('retries a 2xx whose body fails to arrive, then succeeds', async () => {
    let first = true
    const standin = createStandinFetch(() => {
      if (first) {
        first = false
        const locked = new Response('{"ok":true}', {
          status: 200,
          headers: { 'content-type': 'application/json' },
        })
        void locked.body?.getReader()
        return locked
      }
      return jsonResponse(200, { ok: true })
    })
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(3),
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
  })

  it('surfaces a body-read failure as a network ApiError when out of budget', async () => {
    const locked = new Response('{"ok":true}', {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
    void locked.body?.getReader()
    const standin = scriptedStandin(locked)
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(1),
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe(ERROR_CODE_NETWORK)
    expect(error.status).toBe(0)
    expect(error.attempts).toBe(1)
    expect(error.cause).toBeInstanceOf(TypeError)
  })
})

describe('401 and the refresh hook', () => {
  it('surfaces an envelope 401 as an auth ApiError when no hook is set', async () => {
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.status).toBe(401)
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(error.traceId).toBe('trace-1')
    expect(error.attempts).toBe(1)
    expect(standin.calls).toHaveLength(1)
  })

  it('engages the hook for a cookie-credentialed 401 with no bearer token', async () => {
    let hookCalls = 0
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      credentials: 'same-origin',
      refreshAccessToken: async () => {
        hookCalls += 1
        return true
      },
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(hookCalls).toBe(1)
    expect(standin.calls).toHaveLength(2)
    expect(recorded(standin, 0).headers.get('authorization')).toBeNull()
    expect(recorded(standin, 1).headers.get('authorization')).toBeNull()
  })

  it('does not engage the hook for a cookie-credentialed request declared omitAccessToken', async () => {
    let hookCalls = 0
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      credentials: 'same-origin',
      refreshAccessToken: async () => {
        hookCalls += 1
        return true
      },
    })
    const error = await expectApiError(
      api<{ ok: boolean }>('/api/v1/auth/token', { omitAccessToken: true }),
    )
    expect(error.status).toBe(401)
    expect(hookCalls).toBe(0)
    expect(standin.calls).toHaveLength(1)
  })

  it('leaves the hook bearer-only without a cookie credential mode', async () => {
    let hookCalls = 0
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      refreshAccessToken: async () => {
        hookCalls += 1
        return true
      },
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.status).toBe(401)
    expect(hookCalls).toBe(0)
    expect(standin.calls).toHaveLength(1)
  })

  it('passes the credentials mode through to fetch, and omits it when unset', async () => {
    const seen: Array<RequestCredentials | undefined> = []
    const fetchStub: typeof fetch = async (_input, init) => {
      seen.push(init?.credentials)
      return jsonResponse(200, { ok: true })
    }
    const withCookies = createClient({
      baseUrl: BASE_URL,
      fetch: fetchStub,
      credentials: 'include',
    })
    await withCookies<{ ok: boolean }>('/notes')
    expect(seen[0]).toBe('include')

    const withoutMode = createClient({ baseUrl: BASE_URL, fetch: fetchStub })
    await withoutMode<{ ok: boolean }>('/notes')
    expect(seen[1]).toBeUndefined()
  })

  it('refreshes once, retries the original request, and picks up the new token', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const memory = createMemoryReporter()
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        store.set('fresh-token')
        return true
      },
      reporter: memory.reporter,
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(2)
    expect(recorded(standin, 0).headers.get('authorization')).toBe(
      'Bearer stale-token',
    )
    expect(recorded(standin, 1).headers.get('authorization')).toBe(
      'Bearer fresh-token',
    )
    expect(memory.warns).toHaveLength(0)
  })

  it('retries a non-idempotent method once after a successful refresh', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(201, { id: 'n-1' }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        store.set('fresh-token')
        return true
      },
    })
    await expect(
      api<{ id: string }>('/notes', { method: 'POST', body: { title: 'T' } }),
    ).resolves.toEqual({ id: 'n-1' })
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(2)
    expect(recorded(standin, 1).method).toBe('POST')
  })

  it('does not refresh twice when the retried request is refused again', async () => {
    let refreshCalls = 0
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-2' }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        return true
      },
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(error.attempts).toBe(2)
    expect(error.traceId).toBe('trace-2')
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(2)
  })

  it('surfaces the 401 and reports the refresh failure with the envelope attrs', async () => {
    const memory = createMemoryReporter()
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => false,
      reporter: memory.reporter,
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(error.traceId).toBe('trace-1')
    expect(error.attempts).toBe(1)
    expect(standin.calls).toHaveLength(1)
    expect(memory.warns).toEqual([
      {
        message: 'access token refresh failed',
        attrs: {
          status: 401,
          code: 'authn.session_expired',
          trace_id: 'trace-1',
        },
      },
    ])
  })

  it('reports a bare-401 refresh failure without envelope attrs', async () => {
    const memory = createMemoryReporter()
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    const standin = scriptedStandin(jsonResponse(401, { error: 'no envelope' }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => false,
      reporter: memory.reporter,
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(error.code).toBe('client.http.401')
    expect(error.traceId).toBeUndefined()
    expect(memory.warns).toEqual([
      { message: 'access token refresh failed', attrs: { status: 401 } },
    ])
  })

  it('reports a traceId-less refresh-failure envelope with its real code', async () => {
    const memory = createMemoryReporter()
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    const standin = scriptedStandin(
      jsonResponse(401, { code: 'authn.session_expired' }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => false,
      reporter: memory.reporter,
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(error.traceId).toBeUndefined()
    expect(memory.warns).toEqual([
      {
        message: 'access token refresh failed',
        attrs: { status: 401, code: 'authn.session_expired' },
      },
    ])
  })

  it('treats a throwing refresh hook as a failed refresh', async () => {
    const memory = createMemoryReporter()
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        throw new Error('refresh endpoint unreachable')
      },
      reporter: memory.reporter,
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(memory.warns).toHaveLength(1)
    expect(standin.calls).toHaveLength(1)
  })

  it('leaves a credential-less 401 untouched: refresh cannot supply missing authentication', async () => {
    const memory = createMemoryReporter()
    let refreshCalls = 0
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: createMemoryAccessTokenStore(),
      refreshAccessToken: async () => {
        refreshCalls += 1
        return true
      },
      reporter: memory.reporter,
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(error.attempts).toBe(1)
    expect(refreshCalls).toBe(0)
    expect(memory.warns).toHaveLength(0)
    expect(standin.calls).toHaveLength(1)
    expect(recorded(standin).headers.get('authorization')).toBeNull()
  })

  it('does not recurse when the refresh request itself is refused', async () => {
    const memory = createMemoryReporter()
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-2' }),
    )
    const api: RequestFn = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        try {
          await api('/api/v1/auth/token', {
            method: 'POST',
            body: { refresh_token: 'stale' },
            omitAccessToken: true,
          })
          return true
        } catch {
          return false
        }
      },
      reporter: memory.reporter,
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(memory.warns).toEqual([
      {
        message: 'access token refresh failed',
        attrs: {
          status: 401,
          code: 'authn.session_expired',
          trace_id: 'trace-1',
        },
      },
    ])
    expect(error.traceId).toBe('trace-1')
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(2)
    expect(recorded(standin, 1).headers.get('authorization')).toBeNull()
    expect(store.get()).toBe('stale-token')
  })

  it('does not self-deadlock when the refresh operation forgets omitAccessToken on its own request', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const memory = createMemoryReporter()
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-refresh' }),
    )
    const api: RequestFn = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        try {
          await api('/api/v1/auth/token', {
            method: 'POST',
            body: { refresh_token: 'stale' },
          })
          return true
        } catch {
          return false
        }
      },
      reporter: memory.reporter,
    })
    let settled = false
    let caught: unknown
    const observed = api<{ ok: boolean }>('/notes').then(
      () => {},
      (error: unknown) => {
        settled = true
        caught = error
      },
    )
    await expectSettled(() => settled)
    expect(isApiError(caught)).toBe(true)
    if (isApiError(caught)) {
      expect(caught.auth).toBe(true)
      expect(caught.code).toBe('authn.session_expired')
      expect(caught.traceId).toBe('trace-1')
    }
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(2)
    expect(memory.warns).toEqual([
      {
        message: 'access token refresh failed',
        attrs: {
          status: 401,
          code: 'authn.session_expired',
          trace_id: 'trace-1',
        },
      },
    ])
    await observed
  })

  it('ends a restore-shaped recovery hook within a bounded time when its profile read is refused', async () => {
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(200, { authenticated: true }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-me' }),
    )
    const memory = createMemoryReporter()
    let roundSettled = false
    let roundOutcome: boolean | undefined
    const restore = async (issueSession?: RequestFn): Promise<boolean> => {
      const call = issueSession ?? api
      const view = await call<{ authenticated: boolean }>('/api/v1/auth/session')
      if (!view.authenticated) {
        return false
      }
      try {
        await call('/api/v1/auth/me')
        return true
      } catch (error) {
        if (isApiError(error) && error.status === 401) {
          return false
        }
        throw error
      }
    }
    const api: RequestFn = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      credentials: 'same-origin',
      refreshAccessToken: (issueSession?: RequestFn) => {
        const round = restore(issueSession)
        round.then(
          (outcome) => {
            roundSettled = true
            roundOutcome = outcome
          },
          () => {
            roundSettled = true
          },
        )
        return round
      },
      reporter: memory.reporter,
    })
    const refused = expectApiError(api<{ ok: boolean }>('/notes'))
    await expectSettled(() => roundSettled)
    expect(roundOutcome).toBe(false)
    const error = await refused
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(standin.calls).toHaveLength(3)
    expect(recorded(standin, 1).url).toBe(`${BASE_URL}/api/v1/auth/session`)
    expect(recorded(standin, 2).url).toBe(`${BASE_URL}/api/v1/auth/me`)
  })

  it('keeps a declared credential-less 401 terminal even while the store holds a token', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        return true
      },
    })
    const error = await expectApiError(
      api<{ ok: boolean }>('/api/v1/auth/token', {
        method: 'POST',
        body: { refresh_token: 'stale' },
        omitAccessToken: true,
      }),
    )
    expect(error.auth).toBe(true)
    expect(error.attempts).toBe(1)
    expect(refreshCalls).toBe(0)
    expect(standin.calls).toHaveLength(1)
    expect(recorded(standin).headers.get('authorization')).toBeNull()
    expect(store.get()).toBe('stale-token')
  })

  it('coalesces concurrent 401s onto one in-flight refresh', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-2' }),
      jsonResponse(200, { notes: ['one'] }),
      jsonResponse(200, { notes: ['two'] }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        store.set('fresh-token')
        return true
      },
    })
    const [first, second] = await Promise.all([
      api<{ notes: string[] }>('/notes'),
      api<{ notes: string[] }>('/notes'),
    ])
    expect(first).toEqual({ notes: ['one'] })
    expect(second).toEqual({ notes: ['two'] })
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(4)
    expect(recorded(standin, 2).headers.get('authorization')).toBe(
      'Bearer fresh-token',
    )
    expect(recorded(standin, 3).headers.get('authorization')).toBe(
      'Bearer fresh-token',
    )
  })

  it('does not let the refresh round consume the transient-retry budget', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      textResponse(503, 'Service Unavailable'),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        refreshCalls += 1
        store.set('fresh-token')
        return true
      },
      retryPolicy: zeroDelay(2),
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(3)
  })

  it('releases the refused 401 body when the refresh succeeds and the request retries', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    const body = gatedBody()
    const standin = scriptedStandin(
      new Response(body.stream, { status: 401 }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: async () => {
        store.set('fresh-token')
        return true
      },
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
    expect(body.cancelled).toBe(true)
  })

  it('keeps the real 401 envelope when a failed refresh outlives timeoutMs', async () => {
    vi.useFakeTimers()
    try {
      const memory = createMemoryReporter()
      const store = createMemoryAccessTokenStore()
      store.set('stale-token')
      let refreshCalls = 0
      let releaseRefresh: (ok: boolean) => void = () => {}
      const refreshGate = new Promise<boolean>((resolve) => {
        releaseRefresh = resolve
      })
      const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        accessTokenStore: store,
        refreshAccessToken: () => {
          refreshCalls += 1
          return refreshGate
        },
        timeoutMs: 50,
        reporter: memory.reporter,
      })
      const pending = api<{ ok: boolean }>('/notes')
      for (let i = 0; i < 32 && refreshCalls === 0; i += 1) {
        await Promise.resolve()
      }
      expect(refreshCalls).toBe(1)
      await vi.advanceTimersByTimeAsync(5000)
      releaseRefresh(false)
      const error = await expectApiError(pending)
      expect(error.auth).toBe(true)
      expect(error.code).toBe('authn.session_expired')
      expect(error.traceId).toBe('trace-1')
      expect(error.attempts).toBe(1)
      expect(standin.calls).toHaveLength(1)
      expect(memory.warns).toEqual([
        {
          message: 'access token refresh failed',
          attrs: {
            status: 401,
            code: 'authn.session_expired',
            trace_id: 'trace-1',
          },
        },
      ])
    } finally {
      await restoreRealTimers()
    }
  })

  it('keeps the attempt timeout live for a stalled 401 body after a failed refresh', async () => {
    vi.useFakeTimers()
    try {
      const memory = createMemoryReporter()
      const store = createMemoryAccessTokenStore()
      store.set('stale-token')
      let refreshCalls = 0
      let releaseRefresh: (ok: boolean) => void = () => {}
      const refreshGate = new Promise<boolean>((resolve) => {
        releaseRefresh = resolve
      })
      const body = gatedBody()
      const standin = scriptedStandin(
        new Response(body.stream, { status: 401 }),
      )
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        accessTokenStore: store,
        refreshAccessToken: () => {
          refreshCalls += 1
          return refreshGate
        },
        timeoutMs: 50,
        reporter: memory.reporter,
      })
      const pending = api<{ ok: boolean }>('/notes')
      const settlement = settlementOf(pending)
      for (let i = 0; i < 32 && refreshCalls === 0; i += 1) {
        await Promise.resolve()
      }
      expect(refreshCalls).toBe(1)
      releaseRefresh(false)
      await waitForReadStart(body)
      const rejection = expectApiError(pending)
      await vi.advanceTimersByTimeAsync(50)
      await expectSettledNow(() => settlement.settled)
      const error = await rejection
      expect(error.auth).toBe(true)
      expect(error.code).toBe('client.http.401')
      expect(error.traceId).toBeUndefined()
      expect(standin.calls).toHaveLength(1)
      expect(memory.warns).toEqual([
        { message: 'access token refresh failed', attrs: { status: 401 } },
      ])
    } finally {
      await restoreRealTimers()
    }
  })
})

describe('transient retries (idempotent methods only)', () => {
  it('retries a 503 and succeeds', async () => {
    const standin = scriptedStandin(
      textResponse(503, 'Service Unavailable'),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(3),
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
  })

  it('retries a network failure and succeeds', async () => {
    const standin = scriptedStandin(
      new TypeError('fetch failed'),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(3),
    })
    await expect(api<{ ok: boolean }>('/notes')).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
  })

  it('exhausts the budget on repeated bare 503s with the attempt count', async () => {
    const standin = scriptedStandin(
      textResponse(503, 'down'),
      textResponse(503, 'down'),
      textResponse(503, 'down'),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(3),
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('client.http.503')
    expect(error.status).toBe(503)
    expect(error.attempts).toBe(3)
    expect(standin.calls).toHaveLength(3)
  })

  it('surfaces network exhaustion with the original failure as cause', async () => {
    const standin = scriptedStandin(
      new TypeError('fetch failed: socket hang up'),
      new TypeError('fetch failed: socket hang up'),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(2),
    })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe(ERROR_CODE_NETWORK)
    expect(error.status).toBe(0)
    expect(error.attempts).toBe(2)
    expect(error.cause).toBeInstanceOf(TypeError)
  })

  for (const method of ['POST', 'PUT', 'PATCH', 'DELETE'] as const) {
    it(`never transient-retries ${method}`, async () => {
      const standin = scriptedStandin(
        textResponse(503, 'Service Unavailable'),
        jsonResponse(200, { ok: true }),
      )
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        retryPolicy: zeroDelay(3),
      })
      const error = await expectApiError(
        api<{ ok: boolean }>('/things', { method, body: {} }),
      )
      expect(error.code).toBe('client.http.503')
      expect(error.attempts).toBe(1)
      expect(standin.calls).toHaveLength(1)
    })
  }

  it('honours Retry-After on 429 before retrying', async () => {
    vi.useFakeTimers()
    try {
      const standin = scriptedStandin(
        textResponse(429, 'rate limited', { 'retry-after': '1' }),
        jsonResponse(200, { ok: true }),
      )
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        retryPolicy: DEFAULT_RETRY_POLICY,
      })
      const pending = api<{ ok: boolean }>('/limited')
      await vi.advanceTimersByTimeAsync(999)
      expect(standin.calls).toHaveLength(1)
      await vi.advanceTimersByTimeAsync(1)
      await expect(pending).resolves.toEqual({ ok: true })
      expect(standin.calls).toHaveLength(2)
    } finally {
      await restoreRealTimers()
    }
  })

  it('caps Retry-After at the policy maximum delay', async () => {
    vi.useFakeTimers()
    try {
      const standin = scriptedStandin(
        textResponse(429, 'rate limited', { 'retry-after': '120' }),
        jsonResponse(200, { ok: true }),
      )
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        retryPolicy: DEFAULT_RETRY_POLICY,
      })
      const pending = api<{ ok: boolean }>('/limited')
      await vi.advanceTimersByTimeAsync(3999)
      expect(standin.calls).toHaveLength(1)
      await vi.advanceTimersByTimeAsync(1)
      await expect(pending).resolves.toEqual({ ok: true })
      expect(standin.calls).toHaveLength(2)
    } finally {
      await restoreRealTimers()
    }
  })

  it('carries a 429 Retry-After wait onto the ApiError', async () => {
    const standin = scriptedStandin(
      jsonResponse(
        429,
        { type: 'urn:speed:authn:throttled' },
        { 'retry-after': '3' },
      ),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(1),
    })
    const error = await expectApiError(
      api<{ ok: boolean }>('/auth/register', { method: 'POST', body: {} }),
    )
    expect(error.code).toBe('authn.throttled')
    expect(error.retryAfterMs).toBe(3000)
  })

  it('leaves retryAfterMs undefined when the refusal states no wait', async () => {
    const standin = scriptedStandin(textResponse(429, 'rate limited'))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(1),
    })
    const error = await expectApiError(
      api<{ ok: boolean }>('/auth/register', { method: 'POST', body: {} }),
    )
    expect(error.code).toBe('client.http.429')
    expect(error.retryAfterMs).toBeUndefined()
  })

  it('releases the discarded response body before retrying a retryable status', async () => {
    const body = gatedBody()
    const standin = scriptedStandin(
      new Response(body.stream, { status: 429 }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(2),
    })
    await expect(api<{ ok: boolean }>('/limited')).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
    expect(body.cancelled).toBe(true)
  })
})

describe('timeouts', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(async () => {
    await restoreRealTimers()
  })

  it('retries a timed-out request when budget remains', async () => {
    const standin = scriptedStandin(hang, jsonResponse(200, { ok: true }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      timeoutMs: 50,
      retryPolicy: zeroDelay(2),
    })
    const pending = api<{ ok: boolean }>('/slow')
    await vi.advanceTimersByTimeAsync(50)
    await vi.advanceTimersByTimeAsync(1)
    await expect(pending).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
  })

  it('rejects client.timeout with the attempt count once the budget is out', async () => {
    const standin = scriptedStandin(hang)
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      timeoutMs: 50,
      retryPolicy: zeroDelay(1),
    })
    const pending = api<{ ok: boolean }>('/slow')
    const rejection = expectApiError(pending)
    await vi.advanceTimersByTimeAsync(50)
    const error = await rejection
    expect(error.code).toBe(ERROR_CODE_TIMEOUT)
    expect(error.status).toBe(0)
    expect(error.attempts).toBe(1)
    expect(error.cause).toBeInstanceOf(DOMException)
  })

  it('times out a 2xx whose body stalls after the headers arrived', async () => {
    const body = gatedBody()
    const standin = scriptedStandin(new Response(body.stream, { status: 200 }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      timeoutMs: 50,
      retryPolicy: zeroDelay(1),
    })
    const pending = api<{ ok: boolean }>('/slow-body')
    const settlement = settlementOf(pending)
    const rejection = expectApiError(pending)
    await vi.advanceTimersByTimeAsync(0)
    await waitForReadStart(body)
    await vi.advanceTimersByTimeAsync(50)
    await expectSettledNow(() => settlement.settled)
    const error = await rejection
    expect(error.code).toBe(ERROR_CODE_TIMEOUT)
    expect(error.status).toBe(0)
    expect(error.attempts).toBe(1)
    expect(error.cause).toBeInstanceOf(DOMException)
    expect(standin.calls).toHaveLength(1)
    expect(recorded(standin).signal?.aborted).toBe(true)
  })

  it('retries a 2xx whose body stalls mid-read, exactly like any other timeout', async () => {
    const body = gatedBody()
    let first = true
    const standin = createStandinFetch(() => {
      if (first) {
        first = false
        return new Response(body.stream, { status: 200 })
      }
      return jsonResponse(200, { ok: true })
    })
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      timeoutMs: 50,
      retryPolicy: zeroDelay(2),
    })
    const pending = api<{ ok: boolean }>('/slow-body')
    const settlement = settlementOf(pending)
    await vi.advanceTimersByTimeAsync(0)
    await waitForReadStart(body)
    await vi.advanceTimersByTimeAsync(50)
    await vi.advanceTimersByTimeAsync(1)
    await expectSettledNow(() => settlement.settled)
    await expect(pending).resolves.toEqual({ ok: true })
    expect(standin.calls).toHaveLength(2)
    expect(recorded(standin, 0).signal?.aborted).toBe(true)
    expect(recorded(standin, 1).signal?.aborted).toBe(false)
  })
})

describe('caller cancellation', () => {
  it('rejects an already-aborted signal raw, before any request', async () => {
    const standin = scriptedStandin()
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const controller = new AbortController()
    controller.abort()
    let caught: unknown
    try {
      await api<{ ok: boolean }>('/notes', { signal: controller.signal })
    } catch (error) {
      caught = error
    }
    expect(caught).toBeInstanceOf(DOMException)
    if (caught instanceof DOMException) {
      expect(caught.name).toBe('AbortError')
    }
    expect(isApiError(caught)).toBe(false)
    expect(standin.calls).toHaveLength(0)
  })

  it('passes a mid-flight abort through raw, without retrying', async () => {
    const standin = scriptedStandin(hang)
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: DEFAULT_RETRY_POLICY,
    })
    const controller = new AbortController()
    const pending = api<{ ok: boolean }>('/slow', { signal: controller.signal })
    controller.abort()
    let caught: unknown
    try {
      await pending
    } catch (error) {
      caught = error
    }
    expect(caught).toBeInstanceOf(DOMException)
    if (caught instanceof DOMException) {
      expect(caught.name).toBe('AbortError')
    }
    expect(isApiError(caught)).toBe(false)
    const call = recorded(standin)
    expect(call.signal?.aborted).toBe(true)
    expect(standin.calls).toHaveLength(1)
  })

  it('cancels a request aborted during the backoff, before any retry fires', async () => {
    vi.useFakeTimers()
    try {
      const standin = scriptedStandin(
        textResponse(503, 'Service Unavailable', { 'retry-after': '2' }),
      )
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        retryPolicy: DEFAULT_RETRY_POLICY,
      })
      const controller = new AbortController()
      const rejection = expectRawAbort(
        api<{ ok: boolean }>('/notes', { signal: controller.signal }),
      )
      await vi.advanceTimersByTimeAsync(0)
      controller.abort()
      await vi.advanceTimersByTimeAsync(2000)
      await rejection
      expect(standin.calls).toHaveLength(1)
    } finally {
      await restoreRealTimers()
    }
  })

  it('cancels a request aborted while the refresh is in flight, before the retry fires', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    let releaseRefresh: () => void = () => {}
    const refreshGate = new Promise<boolean>((resolve) => {
      releaseRefresh = () => resolve(true)
    })
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(200, { ok: true }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: () => {
        refreshCalls += 1
        return refreshGate
      },
    })
    const controller = new AbortController()
    const rejection = expectRawAbort(
      api<{ ok: boolean }>('/notes', { signal: controller.signal }),
    )
    for (let i = 0; i < 32 && refreshCalls === 0; i += 1) {
      await Promise.resolve()
    }
    expect(refreshCalls).toBe(1)
    controller.abort()
    releaseRefresh()
    await rejection
    expect(standin.calls).toHaveLength(1)
  })

  it('rejects raw when an abort lands while a never-settling refresh is in flight', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(jsonResponse(401, { ...SESSION_EXPIRED }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: () => {
        refreshCalls += 1
        return new Promise<boolean>(() => {})
      },
      timeoutMs: 50,
    })
    const controller = new AbortController()
    let reason: unknown
    const observed = api<{ ok: boolean }>('/notes', {
      signal: controller.signal,
    }).then(
      () => {},
      (caught: unknown) => {
        reason = caught
      },
    )
    for (let i = 0; i < 32 && refreshCalls === 0; i += 1) {
      await Promise.resolve()
    }
    expect(refreshCalls).toBe(1)
    controller.abort()
    await expectSettled(() => reason !== undefined)
    expect(reason).toBeInstanceOf(DOMException)
    if (reason instanceof DOMException) {
      expect(reason.name).toBe('AbortError')
    }
    expect(isApiError(reason)).toBe(false)
    expect(standin.calls).toHaveLength(1)
    await observed
  })

  it('keeps the round alive when the starting request aborts: a later 401 joins it, never a second hook invocation', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-2' }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: () => {
        refreshCalls += 1
        return new Promise<boolean>(() => {})
      },
    })
    const firstController = new AbortController()
    let firstReason: unknown
    const first = api<{ ok: boolean }>('/notes', {
      signal: firstController.signal,
    }).then(
      () => {},
      (caught: unknown) => {
        firstReason = caught
      },
    )
    for (let i = 0; i < 32 && refreshCalls === 0; i += 1) {
      await Promise.resolve()
    }
    expect(refreshCalls).toBe(1)
    firstController.abort()
    await expectSettled(() => firstReason !== undefined)
    expect(firstReason).toBeInstanceOf(DOMException)
    if (firstReason instanceof DOMException) {
      expect(firstReason.name).toBe('AbortError')
    }
    expect(isApiError(firstReason)).toBe(false)
    const secondController = new AbortController()
    let secondReason: unknown
    const second = api<{ ok: boolean }>('/notes', {
      signal: secondController.signal,
    }).then(
      () => {},
      (caught: unknown) => {
        secondReason = caught
      },
    )
    for (let i = 0; i < 64 && standin.calls.length < 2; i += 1) {
      await Promise.resolve()
    }
    for (let i = 0; i < 200; i += 1) {
      await Promise.resolve()
    }
    expect(refreshCalls).toBe(1)
    secondController.abort()
    await expectSettled(() => secondReason !== undefined)
    expect(secondReason).toBeInstanceOf(DOMException)
    if (secondReason instanceof DOMException) {
      expect(secondReason.name).toBe('AbortError')
    }
    expect(isApiError(secondReason)).toBe(false)
    expect(standin.calls).toHaveLength(2)
    await first
    await second
  })

  it('lets requests already joined onto the flight succeed when the initiator aborts and the refresh then succeeds', async () => {
    const store = createMemoryAccessTokenStore()
    store.set('stale-token')
    let refreshCalls = 0
    let releaseRefresh: (ok: boolean) => void = () => {}
    const refreshGate = new Promise<boolean>((resolve) => {
      releaseRefresh = resolve
    })
    const standin = scriptedStandin(
      jsonResponse(401, { ...SESSION_EXPIRED }),
      jsonResponse(401, { ...SESSION_EXPIRED, traceId: 'trace-2' }),
      jsonResponse(200, { notes: ['two'] }),
    )
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      accessTokenStore: store,
      refreshAccessToken: () => {
        refreshCalls += 1
        return refreshGate
      },
    })
    const initiatorController = new AbortController()
    const initiator = expectRawAbort(
      api<{ ok: boolean }>('/notes', {
        signal: initiatorController.signal,
      }),
    )
    for (let i = 0; i < 32 && refreshCalls === 0; i += 1) {
      await Promise.resolve()
    }
    expect(refreshCalls).toBe(1)
    const joiner = api<{ notes: string[] }>('/notes')
    for (let i = 0; i < 64 && standin.calls.length < 2; i += 1) {
      await Promise.resolve()
    }
    for (let i = 0; i < 64; i += 1) {
      await Promise.resolve()
    }
    initiatorController.abort()
    store.set('fresh-token')
    releaseRefresh(true)
    await initiator
    await expect(joiner).resolves.toEqual({ notes: ['two'] })
    expect(recorded(standin, 2).headers.get('authorization')).toBe(
      'Bearer fresh-token',
    )
    expect(refreshCalls).toBe(1)
    expect(standin.calls).toHaveLength(3)
  })

  it('never delivers a 2xx for a caller that aborts mid-read -- even when the body never settles', async () => {
    const body = gatedBody()
    const standin = scriptedStandin(new Response(body.stream, { status: 200 }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const controller = new AbortController()
    const pending = api<{ ok: boolean }>('/notes', {
      signal: controller.signal,
    })
    const rejection = expectRawAbort(pending)
    const settlement = settlementOf(pending)
    await waitForReadStart(body)
    for (let i = 0; i < 2000; i += 1) {
      await Promise.resolve()
    }
    controller.abort()
    await expectSettledNow(() => settlement.settled)
    await rejection
    expect(standin.calls).toHaveLength(1)
    expect(recorded(standin).signal?.aborted).toBe(true)
  })

  it('surfaces the raw abort, never a transient retry, when a cancelled caller abandons the body read', async () => {
    const body = gatedBody()
    const standin = scriptedStandin(new Response(body.stream, { status: 200 }))
    const api = createClient({
      baseUrl: BASE_URL,
      fetch: standin.fetch,
      retryPolicy: zeroDelay(3),
    })
    const controller = new AbortController()
    const pending = api<{ ok: boolean }>('/notes', {
      signal: controller.signal,
    })
    const rejection = expectRawAbort(pending)
    const settlement = settlementOf(pending)
    await waitForReadStart(body)
    for (let i = 0; i < 2000; i += 1) {
      await Promise.resolve()
    }
    controller.abort()
    await expectSettledNow(() => settlement.settled)
    await rejection
    expect(standin.calls).toHaveLength(1)
    expect(recorded(standin).signal?.aborted).toBe(true)
  })

  it('rejects a request aborted during the backoff promptly, without waiting out the delay', async () => {
    vi.useFakeTimers()
    try {
      const standin = scriptedStandin(
        textResponse(503, 'Service Unavailable', { 'retry-after': '2' }),
      )
      const api = createClient({
        baseUrl: BASE_URL,
        fetch: standin.fetch,
        retryPolicy: DEFAULT_RETRY_POLICY,
      })
      const controller = new AbortController()
      let settled = false
      let reason: unknown
      const pending = api<{ ok: boolean }>('/notes', {
        signal: controller.signal,
      }).then(
        () => {
          settled = true
        },
        (caught: unknown) => {
          settled = true
          reason = caught
        },
      )
      await vi.advanceTimersByTimeAsync(0)
      controller.abort()
      for (let i = 0; i < 32; i += 1) {
        await Promise.resolve()
      }
      expect(settled).toBe(true)
      expect(reason).toBeInstanceOf(DOMException)
      if (reason instanceof DOMException) {
        expect(reason.name).toBe('AbortError')
      }
      expect(isApiError(reason)).toBe(false)
      expect(standin.calls).toHaveLength(1)
      await pending
    } finally {
      await restoreRealTimers()
    }
  })
})

describe('error normalization', () => {
  it('parses a full envelope: code, traceId, message, params, details', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, {
        code: 'notes.validation_failed',
        traceId: 'trace-9',
        message: 'One field failed validation.',
        params: { max: 100 },
        details: [
          { field: 'title', code: 'notes.text_required' },
          {
            field: 'credits',
            code: 'billing.insufficient_credits',
            params: { need: 5 },
          },
        ],
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.status).toBe(400)
    expect(error.code).toBe('notes.validation_failed')
    expect(error.traceId).toBe('trace-9')
    expect(error.message).toBe('One field failed validation.')
    expect(error.params).toEqual({ max: 100 })
    expect(error.details).toEqual([
      { field: 'title', code: 'notes.text_required' },
      {
        field: 'credits',
        code: 'billing.insufficient_credits',
        params: { need: 5 },
      },
    ])
    expect(error.auth).toBe(false)
    expect(error.attempts).toBe(1)
  })

  it('parses a legacy {code, params} envelope: code and params, no traceId', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, {
        code: 'authn.password_too_short',
        params: { min_length: 12 },
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.status).toBe(400)
    expect(error.code).toBe('authn.password_too_short')
    expect(error.params).toEqual({ min_length: 12 })
    expect(error.traceId).toBeUndefined()
    expect(error.details).toBeUndefined()
  })

  it('derives the module code from an RFC 9457 problem type', async () => {
    const standin = scriptedStandin(
      jsonResponse(404, {
        type: 'urn:speed:objectstore:not-found',
        title: 'Not Found',
        status: 404,
        detail: 'The object is not available.',
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/objects/missing'))
    expect(error.status).toBe(404)
    expect(error.code).toBe('objectstore.not_found')
  })

  it('normalizes dashes in the derived problem-type code to underscores', async () => {
    const standin = scriptedStandin(
      jsonResponse(422, {
        type: 'urn:speed:settings:invalid-value',
        status: 422,
        detail: 'Out of range.',
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/settings'))
    expect(error.code).toBe('settings.invalid_value')
  })

  it('prefers a literal code over a problem type when a body carries both', async () => {
    const standin = scriptedStandin(
      jsonResponse(409, {
        code: 'notes.conflict',
        type: 'urn:speed:objectstore:conflict',
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('notes.conflict')
  })

  it('treats a problem type outside the urn:speed namespace as envelope-less', async () => {
    const standin = scriptedStandin(
      jsonResponse(500, {
        type: 'https://example.com/problems/out-of-credit',
        title: 'You do not have enough credit.',
        status: 500,
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('client.http.500')
  })

  it('refuses a urn:speed:client:* type into the reserved fallback', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, { type: 'urn:speed:client:timeout', status: 400 }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('client.http.400')
  })

  it('keeps a traceId-bearing envelope correlated when params are absent', async () => {
    const standin = scriptedStandin(
      jsonResponse(500, {
        code: 'config.internal_error',
        traceId: 'trace-50',
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.status).toBe(500)
    expect(error.code).toBe('config.internal_error')
    expect(error.traceId).toBe('trace-50')
    expect(error.params).toBeUndefined()
  })

  it('drops structurally invalid entries from envelope details', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, {
        code: 'notes.validation_failed',
        traceId: 'trace-9',
        details: [
          { field: 'title', code: 'notes.text_required' },
          {
            field: 'price',
            code: 'billing.invalid_amount',
            params: { min: 0 },
          },
          { field: 'orphan' },
          { code: 'notes.unknown' },
          { field: 'broken', code: 42 },
          'not an object',
          { field: 'p', code: 'notes.ok', params: 'junk' },
        ],
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.details).toEqual([
      { field: 'title', code: 'notes.text_required' },
      { field: 'price', code: 'billing.invalid_amount', params: { min: 0 } },
      { field: 'p', code: 'notes.ok' },
    ])
  })

  it('synthesizes an English diagnostic when the envelope has no message', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, {
        code: 'notes.text_required',
        traceId: 'trace-1',
      }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('notes.text_required')
    expect(error.message).toBe('The API rejected the request (notes.text_required).')
  })

  it('maps a bare non-JSON error body to client.http.<status>', async () => {
    const standin = scriptedStandin(
      textResponse(500, '<html><body>Internal Server Error</body></html>'),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('client.http.500')
    expect(error.status).toBe(500)
    expect(error.traceId).toBeUndefined()
    expect(error.message).toBe(
      'The server answered HTTP 500 without a valid ApiError envelope.',
    )
  })

  it('maps JSON without the required envelope fields to client.http.<status>', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, { error: 'no envelope here' }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('client.http.400')
    expect(error.message).toBe(
      'The server answered HTTP 400 without a valid ApiError envelope.',
    )
  })

  it('rejects a non-JSON 2xx body as client.protocol', async () => {
    const standin = scriptedStandin(textResponse(200, 'definitely not json'))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
    expect(error.status).toBe(200)
    expect(error.attempts).toBe(1)
    expect(error.cause).toBeInstanceOf(SyntaxError)
  })

  it('refuses a 2xx HTML document with the client.html code', async () => {
    for (const contentType of [
      'text/html; charset=utf-8',
      'TEXT/HTML',
      'application/xhtml+xml',
    ]) {
      const standin = scriptedStandin(
        textResponse(200, '<!doctype html><html></html>', {
          'content-type': contentType,
        }),
      )
      const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
      const error = await expectApiError(api<{ ok: boolean }>('/notes'))
      expect(error.code, `content type ${contentType}`).toBe(ERROR_CODE_HTML)
      expect(error.status).toBe(200)
      expect(error.attempts).toBe(1)
    }
  })

  it('keeps a non-JSON 2xx without an HTML content type on client.protocol', async () => {
    const standin = scriptedStandin(
      textResponse(200, 'plain text', { 'content-type': 'text/plain' }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
  })

  it('rejects a 2xx JSON primitive as client.protocol', async () => {
    for (const body of [42, 'just a string', true]) {
      const standin = scriptedStandin(jsonResponse(200, body))
      const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
      const error = await expectApiError(api<{ ok: boolean }>('/notes'))
      expect(error.code, `body ${JSON.stringify(body)}`).toBe(
        ERROR_CODE_PROTOCOL,
      )
    }
  })

  it('names what the 2xx-primitive refusal actually refuses', async () => {
    const standin = scriptedStandin(jsonResponse(200, 'just a string'))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe(ERROR_CODE_PROTOCOL)
    expect(error.status).toBe(200)
    expect(error.cause).toBeInstanceOf(SyntaxError)
    expect((error.cause as SyntaxError).message).toBe(
      '2xx body is not a JSON object or array',
    )
  })

  it('refuses an envelope whose code borrows the reserved client.* namespace', async () => {
    const standin = scriptedStandin(
      jsonResponse(400, { code: 'client.timeout', traceId: 'forged-1' }),
    )
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.code).toBe('client.http.400')
    expect(error.status).toBe(400)
    expect(error.traceId).toBeUndefined()
    expect(error.message).toBe(
      'The server answered HTTP 400 without a valid ApiError envelope.',
    )
  })

  it('never presents a forged client.* envelope as a transport failure', async () => {
    const standin = scriptedStandin(jsonResponse(401, { code: 'client.network' }))
    const api = createClient({ baseUrl: BASE_URL, fetch: standin.fetch })
    const error = await expectApiError(api<{ ok: boolean }>('/notes'))
    expect(error.status).toBe(401)
    expect(error.auth).toBe(true)
    expect(error.code).toBe('client.http.401')
    expect(isTransportFailure(error)).toBe(false)
  })
})

