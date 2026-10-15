/**
 * loadRuntimeConfig: the deployment's API origin read, and every way it falls
 * back to same-origin. The file is fetched by its relative path so the read
 * never needs an origin to resolve one; a missing file, a network failure, a
 * non-2xx answer and an unreadable or origin-less body each yield an empty
 * configuration.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { loadRuntimeConfig, RUNTIME_CONFIG_FILE } from './runtime-config.js'

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

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('loadRuntimeConfig', () => {
  it('reads the file by its relative path and returns the named origin', async () => {
    const fetchMock = stubFetch({
      ok: true,
      json: async () => ({ apiOrigin: 'https://api.example.com' }),
    })

    await expect(loadRuntimeConfig()).resolves.toEqual({
      apiOrigin: 'https://api.example.com',
    })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0]?.[0]).toBe(RUNTIME_CONFIG_FILE)
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
