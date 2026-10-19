import { beforeEach, describe, expect, it } from 'vitest'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, createElement, type ReactNode } from 'react'
import { renderHook, waitFor } from '@testing-library/react'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from './runtime'
import {
  auditAdminRecordsList,
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginStart,
  useAuthnProfileGet,
  useAuthnSessionList,
} from './index'

/**
 * Exercises the generated surface (orval output, DO-NOT-EDIT) against a
 * bound fake request function: what is under test is that the generated
 * hooks and operation functions issue exactly the calls the spec
 * implies, with the query keys the generated code stamps -- never a
 * network, never a real client. The authn and audit operations stand in
 * for any generated group: every platform fragment's operations share
 * the identical generated shape, so this set pins the surface mechanics
 * the whole package relies on -- a query hook, a mutation hook, a
 * repeated (array) query parameter, and the generated query keys.
 */
interface RecordedCall {
  path: string
  options?: RequestOptions
}
const calls: RecordedCall[] = []
/** The payloads the endpoints would return, keyed by method + path. */
const responses: Record<string, unknown> = {
  'GET /api/v1/auth/sessions': { sessions: [] },
  'GET /api/v1/auth/me': { account_id: 'user-1' },
}
const fakeRequestFn: RequestFn = (async <T>(
  path: string,
  options?: RequestOptions,
): Promise<T> => {
  calls.push({ path, options })
  const method = options?.method ?? 'GET'
  return (responses[`${method} ${path}`] ?? undefined) as T
}) as RequestFn

/** The host's QueryClient, shared the way a real consumer shares one
 * QueryClientProvider across the app (the peer-family contract). */
const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false } },
})
const wrapper = ({ children }: { children?: ReactNode }) =>
  createElement(QueryClientProvider, { client: queryClient }, children)

beforeEach(() => {
  calls.length = 0
  bindRequestFn(fakeRequestFn)
})

describe('useAuthnSessionList', () => {
  it('issues a GET for the sessions path through the bound request function', async () => {
    const { result } = renderHook(() => useAuthnSessionList(), { wrapper })
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true)
    })
    expect(result.current.data).toEqual({ sessions: [] })
    // react-query passes its own AbortSignal into the query function, so
    // the recorded options carry a live signal -- assert the wire
    // slice, not structural equality over the whole options object.
    expect(calls).toMatchObject([
      { path: '/api/v1/auth/sessions', options: { method: 'GET' } },
    ])
  })

  it('stamps the spec path as the query key, with no tenant prefix', () => {
    // Query-key tenant namespacing is a consumer-shell discipline; the
    // generated key is the bare spec path.
    expect(getAuthnSessionListQueryKey()).toEqual(['/api/v1/auth/sessions'])
  })
})

describe('useAuthnProfileGet', () => {
  it('reads the caller profile through the bound request function', async () => {
    const { result } = renderHook(() => useAuthnProfileGet(), { wrapper })
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true)
    })
    expect(result.current.data).toEqual({ account_id: 'user-1' })
    expect(calls).toMatchObject([
      { path: '/api/v1/auth/me', options: { method: 'GET' } },
    ])
  })

  it('stamps the profile path as the query key', () => {
    expect(getAuthnProfileGetQueryKey()).toEqual(['/api/v1/auth/me'])
  })
})

describe('useAuthnLoginStart', () => {
  it('issues a POST carrying the start body through the bound request function', async () => {
    const { result } = renderHook(() => useAuthnLoginStart(), { wrapper })
    act(() => {
      result.current.mutate({ data: { method: 'password' } })
    })
    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true)
    })
    expect(calls).toMatchObject([
      {
        path: '/api/v1/auth/flows',
        options: {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: { method: 'password' },
        },
      },
    ])
  })
})

describe('auditAdminRecordsList', () => {
  it('passes a repeated (array) query parameter through the bound request function', async () => {
    // The `action` parameter is declared `style: form, explode: true`
    // in the spec, so orval types it as an array; the call must reach
    // the transport with the array intact for it to expand into
    // repeated query parameters.
    await auditAdminRecordsList({ action: ['login.create', 'login.failed'] })
    expect(calls).toMatchObject([
      {
        path: '/api/v1/system/audit/records',
        options: {
          method: 'GET',
          query: { action: ['login.create', 'login.failed'] },
        },
      },
    ])
  })
})
