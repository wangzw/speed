/**
 * use-page-refresh: the shared mechanism behind a surface's refresh
 * action.
 *
 * The mechanism reads query handles structurally -- `isEnabled`,
 * `isFetching`, `status`, `dataUpdatedAt`, `isPlaceholderData`,
 * `refetch` -- and imports only React. It never imports a query client,
 * a transport or an api-client: the host passes handles it already
 * holds, and only `refetch` ever reaches the network, through the host's
 * own read.
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
 *
 * It also carries the update label and the completion announcement the two
 * audit surfaces render beside the action:
 *
 *  - every read reports the settle time of the data on screen
 *    (`dataUpdatedAt`). A read that keeps a previous key's answer on screen
 *    (`isPlaceholderData`) keeps the settle time it had before the key
 *    moved until the new answer lands. The page's update time is the
 *    oldest of those among its enabled reads; a read that never settled --
 *    a feature-absent read is disabled and excluded, a refused read has no
 *    successful settle -- contributes nothing, and no settled read means no
 *    update time at all.
 *  - a click records the click instant and empties the announcement in the
 *    same step, so a refill is a change even when the text repeats. A read
 *    this mechanism re-read reports through that re-read's settlement; a
 *    key-driven read has no re-read result to await (the dashboard's
 *    windowed health reads re-read because the page moved their key), so it
 *    is judged by its own settle time. When every included read settles
 *    successfully at a time not earlier than the click, the announcement is
 *    raised once; any failure, or a read that settles only before the
 *    click and never again, suppresses it. The failure itself is carried by
 *    the stale marker. Automatic re-reads are outside this bookkeeping and
 *    therefore never announce.
 *
 * The console package's internal copy
 * (`console-ui/src/internal/use-page-refresh.ts`) is scheduled for
 * removal in the batch that switches the console to this export.
 */

import { useCallback, useEffect, useRef, useState } from 'react'

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
  /** The query's settled status. */
  readonly status: 'pending' | 'error' | 'success'
  /**
   * When the data on screen last settled successfully, in milliseconds
   * since the epoch; 0 when it never has.
   */
  readonly dataUpdatedAt: number
  /** Whether the data on screen is a previous key's answer. */
  readonly isPlaceholderData: boolean
  readonly refetch: () => Promise<PageRefreshSettled<TData>>
}

/** One page-body read as the mechanism holds it, its data type erased. */
export interface PageRefreshRead {
  readonly key: string
  readonly enabled: boolean
  readonly fetching: boolean
  readonly status: 'pending' | 'error' | 'success'
  readonly dataUpdatedAt: number
  /** The read keeps a previous key's data on screen. */
  readonly placeholder: boolean
  /** The page re-reads this read by moving its key; the mechanism issues no refetch. */
  readonly keyDriven: boolean
  readonly refetch: () => Promise<PageRefreshSettled>
  readonly onSettled: ((settled: PageRefreshSettled) => void) | undefined
}

/**
 * Pairs a query handle with its dedupe key. Two reads resolving to one query
 * key pass the same key and are re-read once. `enabled` overrides the
 * query's own enablement where the page knows a read is off the body (the
 * permission catalog while no tenant is chosen); `onSettled` receives the
 * result of this refresh's own re-read, and is not called for an automatic
 * one. `keyDriven` marks a read the page re-reads by moving its query key
 * (the dashboard's windowed health reads): it is part of the refresh's
 * announcement accounting, but no refetch of the old key is issued.
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
  /**
   * How many refresh completions the page has announced. 0 before the first
   * one and again at every click, so the announcement region empties and
   * refills even when the text is identical.
   */
  readonly announcements: number
}

/** One included read's part in a refresh still being judged. */
type RefreshOutcome = 'pending' | 'error' | { readonly at: number }

interface AwaitingRefresh {
  readonly clickAt: number
  readonly keys: readonly string[]
  readonly keyDriven: ReadonlyMap<string, boolean>
  readonly outcomes: Map<string, RefreshOutcome>
}

/** The settle time of the data a read has on screen, if it has one. */
function settleTime(
  read: PageRefreshRead,
  settled: ReadonlyMap<string, number>,
): number | undefined {
  // A placeholder read is showing the previous key's answer, whose settle
  // time is the one recorded before the key moved.
  if (read.placeholder) {
    return settled.get(read.key)
  }
  return read.dataUpdatedAt > 0 ? read.dataUpdatedAt : undefined
}

/**
 * The refresh mechanism. `reads` is rebuilt every render from the page's
 * query handles; `reset` clears the page's body-scope slots and row markers
 * (the same function the page calls when a scope's context changes). The
 * console package's internal copy is scheduled for removal when the
 * console switches to this export.
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
  // A re-read settlement is not a React state change of this hook, so each
  // one bumps a decision tick that re-runs the judgement below.
  const [decision, setDecision] = useState(0)

  // Record the settle time of every read currently on screen. A placeholder
  // read is skipped: the time it keeps is the one recorded before its key
  // moved.
  useEffect(() => {
    for (const read of reads) {
      if (!read.enabled || read.placeholder || read.dataUpdatedAt <= 0) {
        continue
      }
      settled.current.set(read.key, read.dataUpdatedAt)
    }
  })

  // The page's update time: the oldest successful settle among the reads it
  // renders. A read that never settled contributes nothing.
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

  // Judge the refresh an earlier click started. A read this mechanism
  // re-read reports through its own settlement; a key-driven read is judged
  // by the settle time of the answer now on screen.
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
        // The mechanism's own re-read has not settled yet.
        allSettled = false
        continue
      }

      // No re-read result exists for this read: its settle time is the
      // judge. A read absent at this point was not re-read. The last
      // clause covers a key-driven read that settled only before the click
      // and has nothing in flight: no announcement can ever follow, so the
      // wait ends without one.
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
    // Every click empties the announcement region, whether or not there is
    // a read to re-read: the refill must be a change even when the text is
    // the one already shown.
    setAnnouncements(0)

    // One entry per query key: the first enabled read for the key issues it,
    // and every settlement callback that key carries is notified.
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
        // The page moves this read's key; there is no re-read to await.
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
              // A refused re-read settles without notifying the page: the
              // read's own stale marker reports it, and the refresh ends
              // without an announcement.
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
