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

function stubFetch(answer: FetchResult | (() => Promise<FetchResult>)) {
  const impl =
    typeof answer === 'function' ? answer : async (): Promise<FetchResult> => answer
  const mock = vi.fn<
    (input: RequestInfo | URL, init?: RequestInit) => Promise<FetchResult>
  >(impl)
  vi.stubGlobal('fetch', mock)
  return mock
}

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

  it('reads the shell layout and the theme preset beside the origin', async () => {
    stubFetch({
      ok: true,
      json: async () => ({
        apiOrigin: 'https://api.example.com',
        shellLayout: 'sidebar',
        themePreset: 'glass',
      }),
    })

    await expect(loadRuntimeConfig()).resolves.toEqual({
      apiOrigin: 'https://api.example.com',
      shellLayout: 'sidebar',
      themePreset: 'glass',
    })
  })

  it('keeps a valid field when another field is malformed', async () => {
    stubFetch({
      ok: true,
      json: async () => ({
        apiOrigin: 42,
        shellLayout: 'sidebar',
        themePreset: 'glass',
      }),
    })
    await expect(loadRuntimeConfig()).resolves.toEqual({
      shellLayout: 'sidebar',
      themePreset: 'glass',
    })

    stubFetch({
      ok: true,
      json: async () => ({
        apiOrigin: 'https://api.example.com',
        shellLayout: 'two-columns',
        themePreset: 'glass',
      }),
    })
    await expect(loadRuntimeConfig()).resolves.toEqual({
      apiOrigin: 'https://api.example.com',
      themePreset: 'glass',
    })
  })

  it('ignores an unknown shell layout and an unknown theme preset', async () => {
    stubFetch({ ok: true, json: async () => ({ shellLayout: 'two-columns' }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
    vi.unstubAllGlobals()

    stubFetch({ ok: true, json: async () => ({ shellLayout: 1 }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
    vi.unstubAllGlobals()

    stubFetch({ ok: true, json: async () => ({ themePreset: 'sepia' }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
    vi.unstubAllGlobals()

    stubFetch({ ok: true, json: async () => ({ themePreset: '' }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
  })

  it('reads each field on its own when only one is configured', async () => {
    stubFetch({ ok: true, json: async () => ({ shellLayout: 'sidebar' }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({ shellLayout: 'sidebar' })
    vi.unstubAllGlobals()

    stubFetch({ ok: true, json: async () => ({ themePreset: 'compact' }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({ themePreset: 'compact' })
    vi.unstubAllGlobals()

    stubFetch({ ok: true, json: async () => ({ apiOrigin: '' }) })
    await expect(loadRuntimeConfig()).resolves.toEqual({})
  })

  it('reads the file at the root even under a deep-link path', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
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

  it('names no origin, so the deployment keeps the same-origin fallback', async () => {
    const body = JSON.parse(
      readFileSync(join(appDir, 'public', RUNTIME_CONFIG_FILE), 'utf8'),
    ) as unknown
    stubFetch({ ok: true, json: async () => body })
    const config = await loadRuntimeConfig()
    expect(config.apiOrigin).toBeUndefined()
    expect(config.shellLayout).toBe('sidebar')
  })

  it('sits in the directory the build copies into the served output', async () => {
    const config = await resolveConfig({ root: appDir }, 'build')
    expect(config.publicDir).toBe(join(appDir, 'public'))
    expect(join(config.root, config.build.outDir)).toBe(
      join(appDir, '..', 'server', 'dist'),
    )
  })
})
