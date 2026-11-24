import { afterEach, describe, expect, it, vi } from 'vitest'
import { createStandinFetch, jsonResponse } from '../test-utils/fetch-standin'
import type { StandinCall, StandinFetch } from '../test-utils/fetch-standin'
import {
  createClient,
  createMemoryAccessTokenStore,
  isApiError,
} from './index'

interface Note {
  id: string
  title: string
}

export async function loadNotes(): Promise<Note[]> {
  const api = createClient({
    baseUrl: '/api/v1',
    accessTokenStore: createMemoryAccessTokenStore(),
    refreshAccessToken: async () => false,
    timeoutMs: 10_000,
  })

  try {
    return await api<Note[]>('/notes', { query: { page: 1 } })
  } catch (error) {
    if (isApiError(error)) {
      console.error('loading notes failed', {
        code: error.code,
        trace_id: error.traceId,
      })
    }
    throw error
  }
}

const NOTES: readonly Note[] = [
  { id: 'n-1', title: 'First note' },
  { id: 'n-2', title: 'Second note' },
]

function onlyCall(standin: StandinFetch): StandinCall {
  if (standin.calls.length !== 1) {
    throw new Error(
      `expected exactly one request, the stand-in saw ${standin.calls.length}`,
    )
  }
  const call = standin.calls[0]
  if (call === undefined) {
    throw new Error('unreachable: a one-element array has no first element')
  }
  return call
}

async function expectApiErrorRejection(promise: Promise<unknown>) {
  let settled: unknown
  try {
    await promise
  } catch (error) {
    settled = error
  }
  if (settled === undefined) {
    throw new Error('expected loadNotes to reject on a 401')
  }
  if (!isApiError(settled)) {
    throw new Error(`expected an ApiError rejection, saw ${String(settled)}`)
  }
  return settled
}

describe('README usage example', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('loads the notes through the scripted stand-in, with no noise', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const standin = createStandinFetch(() => jsonResponse(200, NOTES))
    vi.stubGlobal('fetch', standin.fetch)

    await expect(loadNotes()).resolves.toEqual([...NOTES])

    const call = onlyCall(standin)
    expect(call.url).toBe('/api/v1/notes?page=1')
    expect(call.method).toBe('GET')
    expect(call.headers.get('accept')).toBe('application/json')
    expect(call.headers.has('authorization')).toBe(false)
    expect(call.headers.has('x-tenant-id')).toBe(false)
    expect(consoleError).not.toHaveBeenCalled()
    expect(consoleWarn).not.toHaveBeenCalled()
  })

  it('routes a session-expired 401 through the isApiError branch', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    const consoleWarn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const standin = createStandinFetch(() =>
      jsonResponse(401, {
        code: 'authn.session_expired',
        traceId: 'trace-1',
        message: 'Your session expired.',
      }),
    )
    vi.stubGlobal('fetch', standin.fetch)

    const error = await expectApiErrorRejection(loadNotes())
    expect(error.status).toBe(401)
    expect(error.auth).toBe(true)
    expect(error.code).toBe('authn.session_expired')
    expect(error.traceId).toBe('trace-1')
    expect(error.attempts).toBe(1)

    const call = onlyCall(standin)
    expect(call.url).toBe('/api/v1/notes?page=1')
    expect(consoleWarn).not.toHaveBeenCalled()
    expect(consoleError).toHaveBeenCalledWith('loading notes failed', {
      code: 'authn.session_expired',
      trace_id: 'trace-1',
    })
  })
})
