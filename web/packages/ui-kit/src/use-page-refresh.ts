/**
 * The shared mechanism behind a surface's refresh action: re-reads the
 * query handles the host passes, reading them structurally and importing no
 * query client or transport.
 */

import { useCallback, useEffect, useRef, useState } from 'react'

/** The settled shape the mechanism needs from a read. */
export interface PageRefreshSettled<TData = unknown> {
  /** react-query's settled status; a failed read settles as `error`. */
  readonly status: 'pending' | 'error' | 'success'
  /** The read's data, or undefined while it has none. */
  readonly data: TData | undefined
}

/** The slice of a query handle a page-body read is built from. */
export interface PageRefreshReadHandle<TData = unknown> {
  /** Whether the query is enabled. */
  readonly isEnabled: boolean
  /** Whether a read is in flight. */
  readonly isFetching: boolean
  /** The query's settled status. */
  readonly status: 'pending' | 'error' | 'success'
  /** When the data on screen last settled successfully, in epoch milliseconds; 0 if never. */
  readonly dataUpdatedAt: number
  /** Whether the data on screen is a previous key's answer. */
  readonly isPlaceholderData: boolean
  /** Issues the re-read and resolves with its settled shape. */
  readonly refetch: () => Promise<PageRefreshSettled<TData>>
}

/** One page-body read as the mechanism holds it, its data type erased. */
export interface PageRefreshRead {
  /** The dedupe key: reads sharing it are re-read once. */
  readonly key: string
  /** Whether the read participates in the refresh. */
  readonly enabled: boolean
  /** Whether the read is in flight. */
  readonly fetching: boolean
  /** The read's settled status. */
  readonly status: 'pending' | 'error' | 'success'
  /** When the data on screen last settled, in epoch milliseconds; 0 if never. */
  readonly dataUpdatedAt: number
  /** The read keeps a previous key's data on screen. */
  readonly placeholder: boolean
  /** The page re-reads this read by moving its key; the mechanism issues no refetch. */
  readonly keyDriven: boolean
  /** Issues the re-read. */
  readonly refetch: () => Promise<PageRefreshSettled>
  /** Receives this refresh's own re-read result; undefined for a key-driven read. */
  readonly onSettled: ((settled: PageRefreshSettled) => void) | undefined
}

/**
 * Pairs a query handle with its dedupe key; reads sharing a key are re-read
 * once. `enabled` overrides the query's own enablement, `onSettled` runs only
 * for this refresh's re-read, and `keyDriven` counts but issues no refetch.
 */
export function pageRefreshRead<TData>(
  key: string,
  query: PageRefreshReadHandle<TData>,
  options: {
    readonly enabled?: boolean
    readonly keyDriven?: boolean
    readonly onSettled?: (settled: PageRefreshSettled<TData>) => void
  } = {},
): PageRefreshRead {
  const onSettled = options.onSettled
  return {
    key,
    enabled: options.enabled ?? query.isEnabled,
    fetching: query.isFetching,
    status: query.status,
    dataUpdatedAt: query.dataUpdatedAt,
    placeholder: query.isPlaceholderData,
    keyDriven: options.keyDriven === true,
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
  /**
   * The oldest successful settle time among the enabled reads, in
   * milliseconds since the epoch; undefined when no read has settled.
   */
  readonly updatedAt: number | undefined
  /** How many refresh completions the page has announced; 0 before the first and at every click. */
  readonly announcements: number
}

type RefreshOutcome = 'pending' | 'error' | { readonly at: number }

interface AwaitingRefresh {
  readonly clickAt: number
  readonly keys: readonly string[]
  readonly keyDriven: ReadonlyMap<string, boolean>
  readonly outcomes: Map<string, RefreshOutcome>
}

function settleTime(
  read: PageRefreshRead,
  settled: ReadonlyMap<string, number>,
): number | undefined {
  // A placeholder read keeps the settle time recorded before its key moved.
  if (read.placeholder) {
    return settled.get(read.key)
  }
  return read.dataUpdatedAt > 0 ? read.dataUpdatedAt : undefined
}

/**
 * The refresh mechanism. `reads` is rebuilt every render from the page's
 * query handles; `reset` clears the page's body-scope slots and row markers.
 */
export function usePageRefresh(
  reads: readonly PageRefreshRead[],
  reset: () => void,
): PageRefresh {
  const settled = useRef(new Map<string, number>())
  const awaiting = useRef<AwaitingRefresh | null>(null)
  const inFlight = useRef(false)
  const [waiting, setWaiting] = useState(false)
  const [announcements, setAnnouncements] = useState(0)
  // Settlements are not this hook's state, so each bumps a tick that re-runs the judgement.
  const [decision, setDecision] = useState(0)

  useEffect(() => {
    for (const read of reads) {
      if (!read.enabled || read.placeholder || read.dataUpdatedAt <= 0) {
        continue
      }
      settled.current.set(read.key, read.dataUpdatedAt)
    }
  })

  let updatedAt: number | undefined
  for (const read of reads) {
    if (!read.enabled) {
      continue
    }
    const at = settleTime(read, settled.current)
    if (at === undefined) {
      continue
    }
    if (updatedAt === undefined || at < updatedAt) {
      updatedAt = at
    }
  }

  useEffect(() => {
    const current = awaiting.current
    if (current === null) {
      return
    }

    let allSettled = true
    let failed = false
    for (const key of current.keys) {
      const outcome = current.outcomes.get(key)
      if (outcome === 'error') {
        failed = true
        continue
      }
      if (outcome !== undefined && outcome !== 'pending') {
        continue
      }
      if (!current.keyDriven.get(key)) {
        allSettled = false
        continue
      }

      // A key-driven read is judged by the settle time on screen; one that
      // settled only before the click ends the wait without an announcement.
      const read = reads.find(
        (candidate) => candidate.enabled && candidate.key === key,
      )
      const at =
        read === undefined ? current.clickAt : settleTime(read, settled.current)
      if (at !== undefined && at >= current.clickAt) {
        current.outcomes.set(key, { at })
        continue
      }
      if (
        read !== undefined &&
        !read.fetching &&
        read.status !== 'pending'
      ) {
        current.outcomes.set(key, 'error')
        failed = true
        continue
      }
      allSettled = false
    }

    if (failed) {
      awaiting.current = null
      inFlight.current = false
      setWaiting(false)
      return
    }
    if (!allSettled) {
      return
    }
    awaiting.current = null
    inFlight.current = false
    setWaiting(false)
    setAnnouncements((count) => count + 1)
  }, [reads, decision])

  const busy =
    waiting || reads.some((read) => read.enabled && read.fetching)

  const refresh = useCallback(() => {
    if (inFlight.current) {
      return
    }
    reset()
    setAnnouncements(0)

    const included = new Map<string, PageRefreshRead>()
    for (const read of reads) {
      if (read.enabled && !included.has(read.key)) {
        included.set(read.key, read)
      }
    }
    if (included.size === 0) {
      return
    }

    const clickAt = Date.now()
    const keyDriven = new Map<string, boolean>()
    for (const [key, read] of included) {
      keyDriven.set(key, read.keyDriven)
    }
    const current: AwaitingRefresh = {
      clickAt,
      keys: [...included.keys()],
      keyDriven,
      outcomes: new Map(
        [...included.keys()].map((key) => [key, 'pending' as const]),
      ),
    }
    awaiting.current = current
    inFlight.current = true
    setWaiting(true)

    const handlers = new Map<string, ((settled: PageRefreshSettled) => void)[]>()
    for (const read of reads) {
      if (!read.enabled || read.onSettled === undefined) {
        continue
      }
      const list = handlers.get(read.key) ?? []
      list.push(read.onSettled)
      handlers.set(read.key, list)
    }

    const settle = (key: string, outcome: RefreshOutcome) => {
      if (awaiting.current !== current) {
        return
      }
      current.outcomes.set(key, outcome)
      setDecision((tick) => tick + 1)
    }

    for (const [key, read] of included) {
      if (read.keyDriven) {
        continue
      }
      void Promise.resolve()
        .then(() => read.refetch())
        .then(
          (result) => {
            try {
              for (const handler of handlers.get(key) ?? []) {
                handler(result)
              }
            } finally {
              // A refused re-read is reported by the read's stale marker, not here.
              settle(
                key,
                result.status === 'success' ? { at: Date.now() } : 'error',
              )
            }
          },
          () => settle(key, 'error'),
        )
    }
  }, [reads, reset])

  return { busy, refresh, updatedAt, announcements }
}
