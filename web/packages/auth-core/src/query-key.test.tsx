// @vitest-environment jsdom

import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { createElement, useState } from 'react'
import type { ReactNode } from 'react'
import { QueryClient } from '@tanstack/react-query'
import { TenantProvider } from './hooks'
import {
  evictTenantQueries,
  tenantQueryKey,
  useTenantQueryKey,
} from './query-key'

afterEach(() => {
  cleanup()
})

describe('tenantQueryKey', () => {
  it('puts the tenant id as the key outermost segment, then the bare tail', () => {
    expect(tenantQueryKey('tenant-1')).toEqual(['tenant-1'])
    expect(tenantQueryKey('tenant-1', 'notes')).toEqual(['tenant-1', 'notes'])
    expect(tenantQueryKey('tenant-1', 'notes', 3)).toEqual([
      'tenant-1',
      'notes',
      3,
    ])
  })

  it('keeps the tenant id out of the bare key, so one tenant shares one prefix', () => {
    expect(tenantQueryKey('tenant-1', 'a')[0]).toBe('tenant-1')
    expect(tenantQueryKey('tenant-1', 'b')[0]).toBe('tenant-1')
    expect(tenantQueryKey('tenant-2', 'a')[0]).toBe('tenant-2')
  })

  it('yields a key for a null tenant without inventing one', () => {
    expect(tenantQueryKey(null, 'notes')).toEqual([null, 'notes'])
  })
})

describe('evictTenantQueries', () => {
  it("removes the departing tenant's whole subtree and only that subtree", () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(tenantQueryKey('tenant-1', 'notes'), { rows: 1 })
    queryClient.setQueryData(tenantQueryKey('tenant-1', 'members'), {
      rows: 2,
    })
    queryClient.setQueryData(tenantQueryKey('tenant-2', 'notes'), { rows: 3 })
    queryClient.setQueryData(['me'], { user: 'ada' })

    evictTenantQueries(queryClient, 'tenant-1')

    expect(
      queryClient.getQueryData(tenantQueryKey('tenant-1', 'notes')),
    ).toBeUndefined()
    expect(
      queryClient.getQueryData(tenantQueryKey('tenant-1', 'members')),
    ).toBeUndefined()
    expect(queryClient.getQueryData(tenantQueryKey('tenant-2', 'notes'))).toEqual(
      { rows: 3 },
    )
    expect(queryClient.getQueryData(['me'])).toEqual({ user: 'ada' })
  })

  it('leaves the cache untouched when the tenant has no cached rows', () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(tenantQueryKey('tenant-2', 'notes'), { rows: 3 })
    evictTenantQueries(queryClient, 'tenant-1')
    expect(queryClient.getQueryData(tenantQueryKey('tenant-2', 'notes'))).toEqual(
      { rows: 3 },
    )
  })
})

describe('useTenantQueryKey over a host-supplied tenant', () => {
  let setTenant: (tenantId: string | null) => void
  function TenantRoot({ children }: { children: ReactNode }) {
    const [tenantId, set] = useState<string | null>(null)
    setTenant = set
    return createElement(TenantProvider, { tenantId, children })
  }

  it('derives the key and its enablement operand from the current tenant', () => {
    const { result } = renderHook(() => useTenantQueryKey(['notes']), {
      wrapper: TenantRoot,
    })
    expect(result.current).toEqual({
      tenantId: null,
      queryKey: [null, 'notes'],
    })
    act(() => {
      setTenant('tenant-1')
    })
    expect(result.current).toEqual({
      tenantId: 'tenant-1',
      queryKey: ['tenant-1', 'notes'],
    })
    act(() => {
      setTenant('tenant-2')
    })
    expect(result.current).toEqual({
      tenantId: 'tenant-2',
      queryKey: ['tenant-2', 'notes'],
    })
  })

  it('hands back a stable key for a stable bare key within one tenant', () => {
    const bareKey = ['notes'] as const
    const { result, rerender } = renderHook(
      () => useTenantQueryKey(bareKey),
      { wrapper: TenantRoot },
    )
    act(() => {
      setTenant('tenant-1')
    })
    const first = result.current.queryKey
    rerender()
    expect(result.current.queryKey).toBe(first)
  })
})
