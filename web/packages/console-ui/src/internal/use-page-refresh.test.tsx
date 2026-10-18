/**
 * use-page-refresh: the shared mechanism behind the console refresh action.
 *
 * The tests hold the mechanism's contract: the reset runs before any
 * re-read, a disabled read is skipped, two reads resolving to one query key
 * issue one request, every settlement callback of that key is notified, and
 * the control reports busy while a re-read is in flight and drops a second
 * activation that lands before the busy state renders.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { pageRefreshRead, usePageRefresh } from './use-page-refresh.js'
import type {
  PageRefreshReadHandle,
  PageRefreshSettled,
} from './use-page-refresh.js'

/** A settled successful read answer. */
function success<TData>(data: TData): PageRefreshSettled<TData> {
  return { status: 'success', data }
}

/** A query handle the tests script; `refetch` is a spy. */
function handle<TData>(options: {
  readonly refetch: () => Promise<PageRefreshSettled<TData>>
  readonly enabled?: boolean
  readonly fetching?: boolean
}): PageRefreshReadHandle<TData> {
  return {
    isEnabled: options.enabled ?? true,
    isFetching: options.fetching ?? false,
    refetch: options.refetch,
  }
}

describe('usePageRefresh', () => {
  it('resets first, then re-reads each enabled read once and skips a disabled one', async () => {
    const order: string[] = []
    const refetchRoles = vi.fn(() => {
      order.push('roles')
      return Promise.resolve(success(['editor']))
    })
    const refetchGrants = vi.fn(() => {
      order.push('grants')
      return Promise.resolve(success([]))
    })
    const refetchDisabled = vi.fn(() => Promise.resolve(success([])))

    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead('roles', handle({ refetch: refetchRoles })),
          pageRefreshRead(
            'grants',
            handle({ refetch: refetchGrants, enabled: false }),
          ),
          pageRefreshRead('catalog', handle({ refetch: refetchDisabled }), {
            enabled: false,
          }),
        ],
        () => order.push('reset'),
      ),
    )

    act(() => result.current.refresh())

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(order[0]).toBe('reset')
    expect(refetchRoles).toHaveBeenCalledTimes(1)
    expect(refetchGrants).not.toHaveBeenCalled()
    expect(refetchDisabled).not.toHaveBeenCalled()
  })

  it('re-reads two reads resolving to one query key with one request and notifies both', async () => {
    const settled = vi.fn()
    const refetchAny = vi.fn(() => Promise.resolve(success('all')))
    const refetchSameKey = vi.fn(() => Promise.resolve(success('all')))

    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead('settings:all', handle({ refetch: refetchAny }), {
            onSettled: settled,
          }),
          pageRefreshRead('settings:all', handle({ refetch: refetchSameKey }), {
            onSettled: settled,
          }),
        ],
        () => undefined,
      ),
    )

    act(() => result.current.refresh())

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(refetchAny).toHaveBeenCalledTimes(1)
    expect(refetchSameKey).not.toHaveBeenCalled()
    // Both readers of the one key learn the result this refresh settled.
    expect(settled).toHaveBeenCalledTimes(2)
    expect(settled).toHaveBeenCalledWith(success('all'))
  })

  it('marks busy while a re-read is in flight and drops a second activation', async () => {
    let resolveRead!: (settled: PageRefreshSettled<string[]>) => void
    const refetch = vi.fn(
      () =>
        new Promise<PageRefreshSettled<string[]>>((resolve) => {
          resolveRead = resolve
        }),
    )

    const { result } = renderHook(() =>
      usePageRefresh(
        [pageRefreshRead('roles', handle({ refetch }))],
        () => undefined,
      ),
    )

    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.busy).toBe(true))

    // The guard is the mechanism's, so a second activation landing before
    // the busy state has rendered still issues nothing.
    act(() => result.current.refresh())
    expect(refetch).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveRead(success(['editor']))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.busy).toBe(false))
  })

  it('reports busy for an automatic re-read of an included read', () => {
    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead(
            'roles',
            handle({
              refetch: () => Promise.resolve(success([])),
              fetching: true,
            }),
          ),
        ],
        () => undefined,
      ),
    )

    expect(result.current.busy).toBe(true)
  })

  it('still resets and clears busy when the re-read is refused', async () => {
    const reset = vi.fn()
    const refetch = vi.fn(() => Promise.reject(new Error('boom')))
    const onSettled = vi.fn()

    const { result } = renderHook(() =>
      usePageRefresh(
        [pageRefreshRead('roles', handle({ refetch }), { onSettled })],
        reset,
      ),
    )

    act(() => result.current.refresh())

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(reset).toHaveBeenCalledTimes(1)
    expect(onSettled).not.toHaveBeenCalled()
  })
})
