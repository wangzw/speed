// @vitest-environment jsdom
/**
 * query-key.test.tsx -- the tenant-scoped query-key convention
 * (query-key.ts): the factory's key shape, the switch-time eviction over
 * a real QueryClient, and the hook's key-and-enablement over a
 * host-supplied TenantProvider.
 *
 * The eviction test is the discriminating one: a client holds two
 * tenants' subtrees plus an identity row cached under a bare key, and
 * after evictTenantQueries for the departing tenant exactly that
 * tenant's subtree is gone -- the other tenant's rows and the
 * tenant-less row survive. An eviction that removed everything, removed
 * nothing, or matched the wrong segment fails it.
 *
 * The @vitest-environment pragma is per-file (the hook needs a DOM);
 * vitest runs without globals, which disables @testing-library/react's
 * auto-cleanup, so cleanup runs explicitly after each test.
 */

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
    // An identity-domain row cached under a bare key: no tenant segment,
    // out of the eviction's reach.
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
    // No tenant in the route: a key with a null tenant, and the null
    // operand the read gates `enabled` on.
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
    // The memo keys on the tenant id and the bare key, so a re-render
    // with a stable bare key does not churn the key identity.
    expect(result.current.queryKey).toBe(first)
  })
})
