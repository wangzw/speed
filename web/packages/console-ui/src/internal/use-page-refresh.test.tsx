/**
 * use-page-refresh: the shared mechanism behind the console refresh action.
 *
 * The tests hold the mechanism's contract: the reset runs before any
 * re-read, a disabled read is skipped, two reads resolving to one query key
 * issue one request, every settlement callback of that key is notified, and
 * the control reports busy while a re-read is in flight and drops a second
 * activation that lands before the busy state renders.
 *
 * They also hold the refresh label and announcement: the label takes the
 * oldest successful settle time among the enabled reads, a placeholder read
 * keeps its pre-change time until the new value lands, and a click
 * announces only once every re-read read settles successfully, with the
 * announcement cleared on each click and never raised by an automatic
 * re-read.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, renderHook, waitFor } from '@testing-library/react'
import { pageRefreshRead, usePageRefresh } from './use-page-refresh.js'
import type {
  PageRefreshRead,
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
  readonly status?: 'pending' | 'error' | 'success'
  readonly dataUpdatedAt?: number
  readonly placeholder?: boolean
}): PageRefreshReadHandle<TData> {
  return {
    isEnabled: options.enabled ?? true,
    isFetching: options.fetching ?? false,
    status: options.status ?? 'success',
    dataUpdatedAt: options.dataUpdatedAt ?? 0,
    isPlaceholderData: options.placeholder ?? false,
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

describe('refresh settle times', () => {
  it('takes the oldest successful settle time and drops reads that never settled', () => {
    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead('newer', handle({ refetch: neverReRead, dataUpdatedAt: 2_000 })),
          pageRefreshRead('older', handle({ refetch: neverReRead, dataUpdatedAt: 1_000 })),
          pageRefreshRead('never', handle({ refetch: neverReRead, dataUpdatedAt: 0 })),
          pageRefreshRead(
            'absent',
            handle({ refetch: neverReRead, dataUpdatedAt: 500 }),
            { enabled: false },
          ),
        ],
        () => undefined,
      ),
    )

    expect(result.current.updatedAt).toBe(1_000)
  })

  it('keeps a placeholder read on its pre-change time until the new value lands', () => {
    const { result, rerender } = renderHook(
      ({ reads }: { readonly reads: readonly PageRefreshRead[] }) =>
        usePageRefresh(reads, () => undefined),
      {
        initialProps: {
          reads: [
            pageRefreshRead('records', handle({ refetch: neverReRead, dataUpdatedAt: 1_000 })),
          ] as readonly PageRefreshRead[],
        },
      },
    )
    expect(result.current.updatedAt).toBe(1_000)

    // The key moved and the old answer stays on screen: the time it settled
    // at is still the one shown.
    rerender({
      reads: [
        pageRefreshRead(
          'records',
          handle({ refetch: neverReRead, dataUpdatedAt: 0, placeholder: true }),
        ),
      ],
    })
    expect(result.current.updatedAt).toBe(1_000)

    // The new value landed, so the label moves to its settle time.
    rerender({
      reads: [
        pageRefreshRead('records', handle({ refetch: neverReRead, dataUpdatedAt: 3_000 })),
      ],
    })
    expect(result.current.updatedAt).toBe(3_000)
  })

  it('reports no update time before any read has settled', () => {
    const { result } = renderHook(() =>
      usePageRefresh(
        [pageRefreshRead('records', handle({ refetch: neverReRead, dataUpdatedAt: 0 }))],
        () => undefined,
      ),
    )
    expect(result.current.updatedAt).toBeUndefined()
  })
})

describe('refresh announcement', () => {
  it('announces only once every re-read read settles successfully', async () => {
    let resolveSecond!: (settled: PageRefreshSettled<string>) => void
    const second = new Promise<PageRefreshSettled<string>>((resolve) => {
      resolveSecond = resolve
    })
    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead('first', handle({ refetch: () => Promise.resolve(success('a')) })),
          pageRefreshRead('second', handle({ refetch: () => second })),
        ],
        () => undefined,
      ),
    )

    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.busy).toBe(true))
    expect(result.current.announcements).toBe(0)

    await act(async () => {
      resolveSecond(success('b'))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.announcements).toBe(1))
    expect(result.current.busy).toBe(false)
  })

  it('suppresses the announcement when any re-read read fails', async () => {
    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead('first', handle({ refetch: () => Promise.resolve(success('a')) })),
          pageRefreshRead(
            'second',
            handle({
              refetch: () =>
                Promise.resolve<PageRefreshSettled<string>>({
                  status: 'error',
                  data: undefined,
                }),
            }),
          ),
        ],
        () => undefined,
      ),
    )

    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.announcements).toBe(0)
  })

  it('suppresses the announcement when a re-read is refused', async () => {
    const { result } = renderHook(() =>
      usePageRefresh(
        [
          pageRefreshRead(
            'records',
            handle({ refetch: () => Promise.reject(new Error('boom')) }),
          ),
        ],
        () => undefined,
      ),
    )

    act(() => result.current.refresh())
    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.announcements).toBe(0)
  })

  it('clears on each click so a repeated success announces again', async () => {
    const pending: ((settled: PageRefreshSettled<readonly string[]>) => void)[] = []
    const refetch = vi.fn(
      () =>
        new Promise<PageRefreshSettled<readonly string[]>>((resolve) => {
          pending.push(resolve)
        }),
    )
    const { result } = renderHook(() =>
      usePageRefresh([pageRefreshRead('records', handle({ refetch }))], () => undefined),
    )

    act(() => result.current.refresh())
    // The refresh issues its re-read in a microtask, as every activation
    // does; let it land before answering it.
    await act(async () => {
      await Promise.resolve()
    })
    await act(async () => {
      pending.shift()!(success(['a']))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.announcements).toBe(1))

    act(() => result.current.refresh())
    // The region is emptied at the click, before the re-read settles, so the
    // same text fills it again and is announced a second time.
    expect(result.current.announcements).toBe(0)
    await act(async () => {
      await Promise.resolve()
    })
    await act(async () => {
      pending.shift()!(success(['a']))
      await Promise.resolve()
    })
    await waitFor(() => expect(result.current.announcements).toBe(1))
  })

  it('never announces an automatic re-read', () => {
    const { result, rerender } = renderHook(
      ({ reads }: { readonly reads: readonly PageRefreshRead[] }) =>
        usePageRefresh(reads, () => undefined),
      {
        initialProps: {
          reads: [
            pageRefreshRead('records', handle({ refetch: neverReRead, dataUpdatedAt: 1_000 })),
          ] as readonly PageRefreshRead[],
        },
      },
    )

    rerender({
      reads: [
        pageRefreshRead('records', handle({ refetch: neverReRead, dataUpdatedAt: 2_000 })),
      ],
    })

    expect(result.current.updatedAt).toBe(2_000)
    expect(result.current.announcements).toBe(0)
  })

  it('judges a key-driven read by its settle time and never re-reads it', async () => {
    const now = vi.spyOn(Date, 'now').mockReturnValue(5_000)
    try {
      const refetch = vi.fn(() => Promise.resolve(success('unused')))
      const read = (dataUpdatedAt: number, placeholder = false) =>
        pageRefreshRead(
          'windowed',
          handle({
            refetch,
            dataUpdatedAt,
            placeholder,
            fetching: placeholder,
          }),
          { keyDriven: true },
        )
      const { result, rerender } = renderHook(
        ({ reads }: { readonly reads: readonly PageRefreshRead[] }) =>
          usePageRefresh(reads, () => undefined),
        { initialProps: { reads: [read(1_000)] as readonly PageRefreshRead[] } },
      )

      act(() => result.current.refresh())
      expect(refetch).not.toHaveBeenCalled()
      expect(result.current.busy).toBe(true)

      // The page moved the key; the old value is still on screen.
      rerender({ reads: [read(1_000, true)] })
      expect(result.current.announcements).toBe(0)
      expect(result.current.busy).toBe(true)

      // The new value landed at a time not earlier than the click.
      rerender({ reads: [read(6_000)] })
      await waitFor(() => expect(result.current.announcements).toBe(1))
      expect(result.current.busy).toBe(false)
    } finally {
      now.mockRestore()
    }
  })
})

/** A read no refresh in these cases re-reads. */
function neverReRead(): Promise<PageRefreshSettled<never>> {
  return Promise.resolve({ status: 'success', data: undefined as never })
}
