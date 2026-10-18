/**
 * use-page-refresh: the shared mechanism behind the console page refresh
 * action.
 *
 * The refresh action is the operator's reconciliation control. A page
 * supplies every read that describes its body -- the reads inside a modal
 * panel, the account search and the tenant roster the selector addresses
 * itself by are not body reads -- and the mechanism:
 *
 *  - resets the page's feedback scopes and row markers first, because the
 *    refresh changes the context those results were written under: an old
 *    confirmation or failure would otherwise sit beside the re-read table
 *    describing another context;
 *  - re-reads every read enabled at that moment, skipping a disabled one --
 *    a disabled read re-read would issue a request missing its arguments --
 *    and reading each query key once, so two reads resolving to one key
 *    (the settings page's selected-namespace read and its navigation read
 *    with "all namespaces" chosen) issue one request;
 *  - reports `busy` while any included read is in flight, the refresh's own
 *    re-read or an automatic one. The refresh control renders that as
 *    `aria-busy`, never as a native disabled state, and the mechanism's own
 *    guard drops a second activation that lands before the busy state
 *    reaches a render.
 *
 * Automatic re-reads (remount, visibility, reconnect, post-write
 * invalidation) never call the reset: some of them re-read exactly the
 * write that just landed.
 */

import { useCallback, useRef, useState } from 'react'

/** The settled shape the mechanism needs from a read. */
export interface PageRefreshSettled<TData = unknown> {
  /** react-query's settled status; a failed read settles as `error`. */
  readonly status: 'pending' | 'error' | 'success'
  readonly data: TData | undefined
}

/** The slice of a query handle a page-body read is built from. */
export interface PageRefreshReadHandle<TData = unknown> {
  readonly isEnabled: boolean
  readonly isFetching: boolean
  readonly refetch: () => Promise<PageRefreshSettled<TData>>
}

/** One page-body read as the mechanism holds it, its data type erased. */
export interface PageRefreshRead {
  readonly key: string
  readonly enabled: boolean
  readonly fetching: boolean
  readonly refetch: () => Promise<unknown>
  readonly onSettled: ((settled: unknown) => void) | undefined
}

/**
 * Pairs a query handle with its dedupe key. Two reads resolving to one query
 * key pass the same key and are re-read once. `enabled` overrides the
 * query's own enablement where the page knows a read is off the body (the
 * permission catalog while no tenant is chosen); `onSettled` receives the
 * result of this refresh's own re-read, and is not called for an automatic
 * one.
 */
export function pageRefreshRead<TData>(
  key: string,
  query: PageRefreshReadHandle<TData>,
  options: {
    readonly enabled?: boolean
    readonly onSettled?: (settled: PageRefreshSettled<TData>) => void
  } = {},
): PageRefreshRead {
  const onSettled = options.onSettled
  return {
    key,
    enabled: options.enabled ?? query.isEnabled,
    fetching: query.isFetching,
    refetch: query.refetch,
    onSettled:
      onSettled === undefined
        ? undefined
        : (settled) => {
            onSettled(settled as PageRefreshSettled<TData>)
          },
  }
}

export interface PageRefresh {
  /** A re-read this mechanism included -- issued or automatic -- is in flight. */
  readonly busy: boolean
  /** Resets the page's feedback scopes and markers, then re-reads. */
  readonly refresh: () => void
}

/**
 * The refresh mechanism. `reads` is rebuilt every render from the page's
 * query handles; `reset` clears the page's body-scope slots and row markers
 * (the same function the page calls when a scope's context changes).
 */
export function usePageRefresh(
  reads: readonly PageRefreshRead[],
  reset: () => void,
): PageRefresh {
  const [pending, setPending] = useState(false)
  const inFlight = useRef(false)

  const busy =
    pending || reads.some((read) => read.enabled && read.fetching)

  const refresh = useCallback(() => {
    if (inFlight.current) {
      return
    }
    reset()

    // One entry per query key: the first enabled read for the key issues it,
    // and every settlement callback that key carries is notified.
    const keyed = new Map<string, PageRefreshRead>()
    for (const read of reads) {
      if (read.enabled && !keyed.has(read.key)) {
        keyed.set(read.key, read)
      }
    }
    if (keyed.size === 0) {
      return
    }

    const handlers = new Map<string, ((settled: unknown) => void)[]>()
    for (const read of reads) {
      if (!read.enabled || read.onSettled === undefined) {
        continue
      }
      const list = handlers.get(read.key) ?? []
      list.push(read.onSettled)
      handlers.set(read.key, list)
    }

    inFlight.current = true
    setPending(true)
    let remaining = keyed.size
    const settle = () => {
      remaining -= 1
      if (remaining === 0) {
        inFlight.current = false
        setPending(false)
      }
    }
    for (const [key, read] of keyed) {
      void Promise.resolve()
        .then(() => read.refetch())
        .then(
          (settled) => {
            try {
              for (const handler of handlers.get(key) ?? []) {
                handler(settled)
              }
            } finally {
              settle()
            }
          },
          // A refused re-read settles without notifying the page: the read's
          // own stale marker reports it, and the control leaves its busy
          // state.
          () => settle(),
        )
    }
  }, [reads, reset])

  return { busy, refresh }
}
