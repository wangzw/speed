/**
 * loadRuntimeConfig: the deployment's API origin read, and every way it falls
 * back to same-origin. The file is fetched at the deployment root, resolved
 * against the document base rather than the current URL, so the read never
 * needs an origin to resolve one and a deep link cannot shift it under its own
 * path; a missing file, a network failure, a non-2xx answer and an unreadable
 * or origin-less body each yield an empty configuration.
 *
 * The deployment carries the file itself: the committed asset under public/
 * (RUNTIME_CONFIG_FILE) is one the build copies into the served output, so the
 * read answers 200 rather than 404. Its own suite pins that the same-origin
 * shape and the copy happen -- driving the real loader over the asset's body,
 * and resolving the vite config to the directory the server serves.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { resolveConfig } from 'vite'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RUNTIME_CONFIG_FILE, loadRuntimeConfig } from './runtime-config.js'

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

describe('the committed runtime-config asset', () => {
  const appDir = join(dirname(fileURLToPath(import.meta.url)), '..')

  it('resolves to the same-origin fallback, never overriding the deployment base', async () => {
    // The asset is what the deployment serves at the root; the build copies it
    // out of public/ (see the next test). Driving the real loader over its own
    // body proves the served config names no origin, so the app reaches its own
    // origin exactly as it does with the file absent -- the file's only effect
    // is the read answering 200 instead of 404.
    const body = JSON.parse(
      readFileSync(join(appDir, 'public', RUNTIME_CONFIG_FILE), 'utf8'),
    ) as unknown
    stubFetch({ ok: true, json: async () => body })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
  })

  it('sits in the directory the build copies into the served output', async () => {
    // vite copies publicDir into build.outDir, and the demo program embeds and
    // serves that directory (starters/demo/server) at the deployment root. Tying
    // the asset's directory to the copied-from dir and the copied-to dir -- both
    // from the resolved config, not restated here -- is what makes the deployed
    // read land on the file rather than 404.
    const config = await resolveConfig({ root: appDir }, 'build')
    // publicDir resolves absolute; build.outDir stays as written, relative to
    // the root, so the served directory is the two joined.
    expect(config.publicDir).toBe(join(appDir, 'public'))
    expect(join(config.root, config.build.outDir)).toBe(
      join(appDir, '..', 'server', 'dist'),
    )
  })
})
