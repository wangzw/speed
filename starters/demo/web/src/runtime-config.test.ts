/**
 * loadRuntimeConfig: the deployment's API origin read, and every way it falls
 * back to same-origin. The file is fetched at the deployment root, resolved
 * against the document base rather than the current URL, so the read never
 * needs an origin to resolve one and a deep link cannot shift it under its own
 * path; a missing file, a network failure, a non-2xx answer and an unreadable
 * or origin-less body each yield an empty configuration.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { loadRuntimeConfig } from './runtime-config.js'

type FetchResult = {
  ok: boolean
  status?: number
  json: () => Promise<unknown>
}

/** Installs a fetch whose answer each test scripts, and returns the mock. */
function stubFetch(answer: FetchResult | (() => Promise<FetchResult>)) {
  const impl =
    typeof answer === 'function' ? answer : async (): Promise<FetchResult> => answer
  const mock = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<FetchResult>
  >(impl)
  vi.stubGlobal('fetch', mock)
  return mock
}

/** The path the requested URL resolves to against the current document. */
function requestedPath(mock: ReturnType<typeof stubFetch>): string {
  return new URL(String(mock.mock.calls[0]?.[0]), document.baseURI).pathname
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('loadRuntimeConfig', () => {
  it('reads the file at the deployment root and returns the named origin', async () => {
    const fetchMock = stubFetch({
      ok: true,
      json: async () => ({ apiOrigin: 'https://api.example.com' }),
    })

    await expect(loadRuntimeConfig()).resolves.toEqual({
      apiOrigin: 'https://api.example.com',
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(requestedPath(fetchMock)).toBe('/runtime-config.json')
  })

  it('reads the file at the root even under a deep-link path', async () => {
    // A deep link such as /console/tenants must not shift the read under
    // its own directory: a bare relative fetch would resolve to
    // /console/runtime-config.json here and miss the file.
    window.history.replaceState({}, '', '/console/tenants')
    const fetchMock = stubFetch({ ok: true, json: async () => ({}) })

    await loadRuntimeConfig()
    expect(requestedPath(fetchMock)).toBe('/runtime-config.json')
  })

  it('falls back to no origin when the file is absent', async () => {
    stubFetch({ ok: false, status: 404, json: async () => ({}) })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
  })

  it('falls back to no origin when the read fails outright', async () => {
    stubFetch(async () => {
      throw new TypeError('network down')
    })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
  })

  it('falls back to no origin when the body is not readable', async () => {
    stubFetch({
      ok: true,
      json: async () => {
        throw new SyntaxError('not json')
      },
    })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
  })

  it('ignores a body that carries no usable origin', async () => {
    for (const body of [null, [], { apiOrigin: 42 }, { apiOrigin: '' }, {}]) {
      stubFetch({ ok: true, json: async () => body })
      await expect(loadRuntimeConfig()).resolves.toEqual({})
      vi.unstubAllGlobals()
    }
  })
})
