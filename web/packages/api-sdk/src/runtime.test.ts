import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import {
  bindRequestFn,
  speedRequest,
  speedRequestCredentialless,
} from './runtime'

interface RecordedCall {
  path: string
  options?: RequestOptions
}
const calls: RecordedCall[] = []
const fakeRequestFn: RequestFn = (async <T>(
  path: string,
  options?: RequestOptions,
): Promise<T> => {
  calls.push({ path, options })
  return undefined as T
}) as RequestFn

beforeEach(() => {
  calls.length = 0
  bindRequestFn(fakeRequestFn)
})

describe('bindRequestFn / speedRequest', () => {
  it('forwards an orval-shaped GET call to the bound request function', async () => {
    const result = await speedRequest<{ notes: unknown[] }>({
      url: '/api/v1/notes',
      method: 'GET',
    })
    expect(result).toBeUndefined()
    expect(calls).toEqual([
      { path: '/api/v1/notes', options: { method: 'GET' } },
    ])
  })

  it('maps the axios-shaped POST call onto body and headers', async () => {
    const body = { text: 'hello' }
    const signal = new AbortController().signal
    await speedRequest<unknown>({
      url: '/api/v1/notes',
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      data: body,
      signal,
    })
    expect(calls).toEqual([
      {
        path: '/api/v1/notes',
        options: {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body,
          signal,
        },
      },
    ])
  })

  it('maps orval query params onto the query option', async () => {
    await speedRequest<unknown>({
      url: '/api/v1/notes',
      method: 'GET',
      params: { page: 1, tag: null },
    })
    expect(calls[0]?.options?.query).toEqual({ page: 1, tag: null })
  })

  it('carries a repeated (array) query parameter through to the transport unchanged', async () => {
    await speedRequest<unknown>({
      url: '/api/v1/system/audit/records',
      method: 'GET',
      params: { action: ['login.create', 'login.failed'] },
    })
    expect(calls[0]?.options?.query).toEqual({
      action: ['login.create', 'login.failed'],
    })
  })

  it('never sets the omitAccessToken key on an ordinary speedRequest call', async () => {
    await speedRequest<unknown>({
      url: '/api/v1/notes',
      method: 'GET',
    })
    expect(calls).toEqual([
      { path: '/api/v1/notes', options: { method: 'GET' } },
    ])
    expect(calls[0]?.options?.omitAccessToken).toBeUndefined()
  })

  it('forwards a raw-byte response flag as the transport blob responseType', async () => {
    await speedRequest<Blob>({
      url: '/api/v1/files/f-1/content',
      method: 'GET',
      responseType: 'blob',
    })
    expect(calls).toEqual([
      {
        path: '/api/v1/files/f-1/content',
        options: { method: 'GET', responseType: 'blob' },
      },
    ])
  })

  it('never sets responseType on an ordinary call', async () => {
    await speedRequest<unknown>({ url: '/api/v1/notes', method: 'GET' })
    expect(calls).toEqual([
      { path: '/api/v1/notes', options: { method: 'GET' } },
    ])
    expect(calls[0]?.options?.responseType).toBeUndefined()
  })

  it('declares the credential-less flag on speedRequestCredentialless calls', async () => {
    const body = { refresh_token: 'stale' }
    await speedRequestCredentialless<unknown>({
      url: '/api/v1/auth/token',
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      data: body,
    })
    expect(calls).toEqual([
      {
        path: '/api/v1/auth/token',
        options: {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body,
          omitAccessToken: true,
        },
      },
    ])
  })

  it('rebinding replaces the previous request function (last bind wins)', async () => {
    const second: RequestFn = (async <T>(path: string): Promise<T> => {
      calls.push({ path })
      return undefined as T
    }) as RequestFn
    bindRequestFn(second)
    await speedRequest<unknown>({ url: '/api/v1/notes', method: 'GET' })
    expect(calls).toEqual([{ path: '/api/v1/notes', options: undefined }])
  })

  it('throws a programmer error while no request function is bound', async () => {
    vi.resetModules()
    const fresh = await import('./runtime')
    expect(() =>
      fresh.speedRequest({ url: '/api/v1/notes', method: 'GET' }),
    ).toThrow(
      '[speed-api-sdk] no request function bound: call bindRequestFn(createClient(...)) once at bootstrap before any generated hook runs.',
    )
    expect(() =>
      fresh.speedRequestCredentialless({
        url: '/api/v1/auth/token',
        method: 'POST',
      }),
    ).toThrow(
      '[speed-api-sdk] no request function bound: call bindRequestFn(createClient(...)) once at bootstrap before any generated hook runs.',
    )
  })
})
