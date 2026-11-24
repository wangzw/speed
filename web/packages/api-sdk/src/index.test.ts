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

interface RecordedCall {
  path: string
  options?: RequestOptions
}
const calls: RecordedCall[] = []
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
    expect(calls).toMatchObject([
      { path: '/api/v1/auth/sessions', options: { method: 'GET' } },
    ])
  })

  it('stamps the spec path as the query key, with no tenant prefix', () => {
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
