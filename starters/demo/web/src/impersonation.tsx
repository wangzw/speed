/**
 * The impersonation shell's host half: one identity-change action shared by
 * both areas, the status read the banner renders from, the banner the tenant
 * frame mounts, and the one end-impersonation write attempt that the banner,
 * the header's impersonating chip and the account menu's end item all share.
 *
 * Starting or ending an impersonation swaps whose identity every later read
 * belongs to: the query cache's rows were taken under the old identity, the
 * session snapshot still carries the old principal, the host-attached
 * platform permission set was read under the old identity (and is retained
 * by restore(), which cannot know the identity changed), the current tenant
 * may differ, and the live notification subscription is registered per
 * effective account. applyIdentityChange is the one action that settles all
 * of them: it evicts the cache (except the status read's own query),
 * re-reads the session, and then lets the identity-keyed effects (app.tsx's
 * tenant effect and platform-permissions.tsx's read) re-read the permission
 * set and the current tenant; it bumps an identity generation the tenant
 * frame uses to re-register the notification stream. The permission set's
 * read resets it to unknown immediately before issuing its own read, so a
 * re-read that hangs or is refused never leaves the console waiting on a
 * set this action cleared. A start then sends the viewer to the tenant-area
 * home; an end navigates nowhere, because the page is already a tenant page.
 * The action's identity is stable for the frame's whole lifetime: the
 * expiry timer keys on it, and a callback that changed with the route would
 * restart the timer on every navigation.
 *
 * The action never rejects. Its callers are settled writes (the banner's
 * end, the console page's start/end callback) and the expiry timer; a
 * failure here must not overturn a write the server already accepted.
 *
 * The status read is enabled only while the snapshot says an impersonation
 * is in force, so a visitor who is not impersonating issues no
 * GET /auth/impersonation at all. While it is enabled, a bare 404 is the
 * settled "no impersonation any more" (ended, expired, or terminated
 * elsewhere) and runs the ended path once for that impersonation interval:
 * keying the guard on the error object would let the payload of the next
 * automatic read run it again. The status query itself is not evicted by
 * the identity change, so a settled 404 does not re-trigger its own read.
 * Once the one ended path has run, a snapshot that still says impersonating
 * starts the same bounded re-read the expiry path uses. The read is the only
 * source of the banner's tenant and expiry: the roster read belongs to the platform
 * management surface, which the gateway refuses to an impersonated
 * identity.
 *
 * Expiry is the host's timer, not the banner's. The server judges a
 * record's validity through the authn.session.check-ttl cache (default 5s,
 * bounded to 60s) and emits no event, so restore() may still answer
 * impersonating for up to one check-ttl after the recorded expiry. The
 * timer therefore runs the full identity change at the expiry instant and,
 * while the session still answers impersonating, re-reads it on a fixed
 * short interval for at most the check-ttl upper bound; it then stops and
 * leaves the banner and its end control in place, and the tab becoming
 * visible again re-reads through the query client's staleTime 0. A settled
 * 404 runs the same
 * bounded re-read from the moment it handled the 404, because that path has
 * no recorded expiry to bound it by.
 *
 * An explicit end is the write half of the same window. Its result is what
 * the user acted on, so the banner stops claiming an impersonation is in
 * force the moment the DELETE settles -- including a bare 404 -- without
 * waiting for the snapshot to flip; the confirmation shows in the live
 * region that outlives the banner, and the end control goes with it. The
 * null holds until a re-read reports the session no longer impersonates, or
 * until the bounded re-read runs out: the same interval and upper bound as
 * the expiry path, because every replica's validity cache may lag one
 * check-ttl behind an explicit end. When it runs out the banner returns to
 * being snapshot-driven; its end control treats a bare 404 as a settled end
 * and runs the change again, converging once the caches expire. A start has
 * no corresponding blank: the banner appears when the re-read settles the
 * impersonated identity.
 *
 * A start is also the one identity change that rotates the session cookie on
 * the server. Until its response lands, every read already in flight was
 * issued under the cookie the server has just replaced, and a read answered
 * 401 in that window runs the transport's silent restore with the stale
 * cookie -- which reads "no session" and turns the local session anonymous.
 * The mutation cache exposes the moment the start write leaves the browser
 * (the console page issues it), and this provider uses that moment, not the
 * settled write, to cut both halves of the window: the queries in flight are
 * cancelled (their generated query functions carry react-query's abort
 * signal into the transport, so no 401 answer reaches the session hook), and
 * new query fetches stay paused until applyIdentityChange finishes -- after
 * the eviction and the re-read, so the reads that follow carry the new
 * cookie. This is a mitigation of the start path's rotation window, not a
 * fix for the transport's old-token 401 handling.
 */

import { onlineManager } from '@tanstack/react-query'
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState } from '@speed/auth-core'
import {
  AUTH_UI_NAMESPACE,
  ImpersonationBanner,
  ImpersonationIndicator,
  useImpersonationNotice,
} from '@speed/auth-ui'
import type { ImpersonationBannerState } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { useWriteFeedback } from '@speed/ui-kit'
import {
  getAuthnImpersonationStartMutationOptions,
  getAuthnImpersonationStatusQueryKey,
  useAuthnImpersonationEnd,
  useAuthnImpersonationStatus,
} from './app-api/index.js'
import type { AuthnImpersonationGrant } from './app-api/index.js'
import { useAppServices } from './app-services.js'
import { TENANT_HOME_PATH } from './pages/not-found-page.js'

/** The upper bound of authn.session.check-ttl: how long after the recorded
 * expiry the server may still treat the session as impersonating. */
const CHECK_TTL_MAX_MS = 60_000

/** The fixed interval between re-reads while the server still answers
 * impersonating after the recorded expiry (the check-ttl default). */
const CHECK_TTL_RETRY_MS = 5_000

/** The generated start mutation's key. The console page issues the write, so
 * the shared mutation cache is the host's only view of the moment it leaves
 * the browser; the key is read from the generated options rather than
 * repeated here, so a regeneration cannot silently rename it. */
const START_MUTATION_KEY: readonly unknown[] | undefined =
  getAuthnImpersonationStartMutationOptions().mutationKey

/** The status read's generated query key. The identity change evicts every
 * other cached row and leaves this one alone: evicting it would make the
 * mounted status observer re-read immediately, and a re-read that answers
 * the bare 404 would re-enter the ended path it just handled. Once the read
 * is disabled -- the interval is over -- its own hook evicts it, so the next
 * interval starts from a fresh read rather than the finished grant. */
const STATUS_QUERY_KEY: readonly unknown[] = getAuthnImpersonationStatusQueryKey()

/** Whether a mutation key is the start write's (same parts, same order). */
function isStartMutationKey(key: readonly unknown[] | undefined): boolean {
  if (START_MUTATION_KEY === undefined || key === undefined) {
    return false
  }
  return (
    key.length === START_MUTATION_KEY.length &&
    key.every((part, index) => part === START_MUTATION_KEY[index])
  )
}

/** Whether a query key is the status read's (same parts, same order). */
function isStatusQueryKey(key: readonly unknown[]): boolean {
  return (
    key.length === STATUS_QUERY_KEY.length &&
    key.every((part, index) => part === STATUS_QUERY_KEY[index])
  )
}

/** The state of the start write's window: from the moment the write is issued
 * until applyIdentityChange settles the new identity. */
interface ReadHold {
  /** The client's online state when the hold began; a client that was
   * genuinely offline stays offline when the hold is released. */
  readonly resumeOnline: boolean
}

/** Which identity edge just settled. */
export type IdentityChange = 'started' | 'ended'

interface IdentityChangeContextValue {
  /** Settles every identity-dependent piece of host state; never rejects. */
  readonly applyIdentityChange: (change: IdentityChange) => Promise<void>
  /** Bumped after each identity change, for hosts that re-register
   * identity-scoped subscriptions. */
  readonly generation: number
}

const IdentityChangeContext =
  createContext<IdentityChangeContextValue | null>(null)

/**
 * Mounts the identity-change action over the router: it holds the query
 * cache eviction, the session re-read and the navigation a start needs, so
 * both the tenant frame and the console page report to one action.
 */
export function IdentityChangeProvider({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const { session, queryClient } = useAppServices()
  const navigate = useNavigate()
  const [generation, setGeneration] = useState(0)
  const holdRef = useRef<ReadHold | null>(null)

  /** Lets query fetches run again (a no-op unless a start put them on hold). */
  const releaseReads = useCallback((): void => {
    const hold = holdRef.current
    if (hold === null) {
      return
    }
    holdRef.current = null
    if (hold.resumeOnline) {
      onlineManager.setOnline(true)
    }
  }, [])

  /** Cuts both halves of the start write's rotation window. */
  const holdReads = useCallback((): void => {
    if (holdRef.current !== null) {
      return
    }
    // Every read in flight was issued under the session cookie the start
    // write is about to replace. Cancel them before any 401 answer can run
    // the transport's silent restore with the stale cookie.
    void queryClient.cancelQueries()
    const resumeOnline = onlineManager.isOnline()
    holdRef.current = { resumeOnline }
    if (resumeOnline) {
      // Pausing the client's fetches is the one client-wide gate react-query
      // exposes: queries created or refetching while held stay paused and
      // issue no request. The start write itself is exempted below (its
      // mutation key runs with networkMode 'always'), so the pause cannot
      // stall the write that opened the window.
      onlineManager.setOnline(false)
    }
  }, [queryClient])

  // Watches for the start write being issued through the shared mutation
  // cache. A pending start holds reads; a refused one releases them (no
  // identity change follows); a successful one stays held until the identity
  // change settles the new session.
  useEffect(() => {
    if (START_MUTATION_KEY === undefined) {
      return undefined
    }
    queryClient.setMutationDefaults(START_MUTATION_KEY, {
      networkMode: 'always',
    })
    const unsubscribe = queryClient.getMutationCache().subscribe((event) => {
      if (event.type !== 'added' && event.type !== 'updated') {
        return
      }
      const { mutation } = event
      if (!isStartMutationKey(mutation.options.mutationKey)) {
        return
      }
      if (mutation.state.status === 'pending') {
        holdReads()
      } else if (mutation.state.status === 'error') {
        releaseReads()
      }
    })
    return () => {
      unsubscribe()
      // A provider torn down mid-hold must not leave the shared client
      // paused for whatever renders next.
      releaseReads()
    }
  }, [queryClient, holdReads, releaseReads])

  // The action's callers include the expiry timer, which must not restart
  // when the route changes. The collaborators still reach the stable
  // callback through refs; the callback itself never changes for the
  // provider's lifetime.
  const navigateRef = useRef(navigate)
  navigateRef.current = navigate
  const queryClientRef = useRef(queryClient)
  queryClientRef.current = queryClient
  const sessionRef = useRef(session)
  sessionRef.current = session
  const releaseReadsRef = useRef(releaseReads)
  releaseReadsRef.current = releaseReads

  const applyIdentityChange = useCallback(
    async (change: IdentityChange): Promise<void> => {
      try {
        // Every cached row belongs to the identity that was current when it
        // was read; the session-end strategy evicts the same way. The status
        // read is the one exception: it is keyed on nothing identity-scoped
        // and is re-read by its own enabled edge, and evicting it here would
        // make a settled 404 re-enter this action.
        queryClientRef.current.removeQueries({
          predicate: (query) => !isStatusQueryKey(query.queryKey),
        })
        await sessionRef.current.restore()
        setGeneration((current) => current + 1)
        if (change === 'started') {
          navigateRef.current(TENANT_HOME_PATH)
        }
      } catch {
        // A failed re-read leaves the cache evicted (fail-closed); it never
        // reaches the settled write that called it. The permission set is
        // whatever its own last read attached -- the identity-keyed read
        // resets it only when it is about to issue a new read.
      } finally {
        // The identity is settled -- or the re-read failed, leaving the
        // cache empty. Either way reads may go out again, now under the
        // session the server holds.
        releaseReadsRef.current()
      }
    },
    [],
  )

  return (
    <IdentityChangeContext.Provider value={{ applyIdentityChange, generation }}>
      {children}
    </IdentityChangeContext.Provider>
  )
}

/** The identity-change action and its generation. */
export function useIdentityChange(): IdentityChangeContextValue {
  const value = useContext(IdentityChangeContext)
  if (value === null) {
    throw new Error('useIdentityChange must be called within an IdentityChangeProvider')
  }
  return value
}

/**
 * Whether a failure is the impersonation endpoint's bare 404: "there is no
 * impersonation in force". The transport surfaces it either as an HTTP
 * status or as the client.http.404 code, the same structural reading
 * console-ui makes of the same answer.
 */
export function isNotImpersonating(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) {
    return false
  }
  const candidate = error as { readonly status?: unknown; readonly code?: unknown }
  return candidate.status === 404 || candidate.code === 'client.http.404'
}

/**
 * The impersonation in force, read only while `enabled` (the session says
 * one is): the settled grant, or undefined while the read is pending,
 * failed, or answered the bare 404.
 *
 * It owns the two host reactions the status read needs: a bare 404 runs
 * the ended path once per impersonation interval, and the recorded expiry
 * schedules the identity change plus the bounded re-read described in this
 * file's header. The 404 path runs the same bounded re-read itself, because
 * it has no recorded expiry to bound it by.
 */
export function useImpersonationStatus(
  enabled: boolean,
): AuthnImpersonationGrant | undefined {
  const { session, queryClient } = useAppServices()
  const { applyIdentityChange } = useIdentityChange()
  const status = useAuthnImpersonationStatus({ query: { enabled } })

  // The status query lives in the shared cache across intervals, so an
  // interval that has ended would leave its grant behind for the cache's
  // gcTime -- and a page that reads it on first render would present the
  // finished impersonation as current. Evict it only once the observer is
  // disabled: evicting a live observer would make it re-read at once, and a
  // re-read answering the bare 404 would re-enter the ended path.
  useEffect(() => {
    if (!enabled) {
      queryClient.removeQueries({ queryKey: STATUS_QUERY_KEY })
    }
  }, [enabled, queryClient])

  // Each impersonation interval answers for itself: the status query lives
  // across intervals in the shared cache, and the previous interval's grant
  // (or the 404 that ended it) must not drive this one. The interval starts
  // when `enabled` turns true and is forgotten when it turns false. Its
  // start time is what tells an answer issued in this interval from a
  // cached one issued earlier.
  const enabledSince = useRef<number | null>(null)
  if (!enabled) {
    enabledSince.current = null
  } else if (enabledSince.current === null) {
    enabledSince.current = Date.now()
  }
  const periodStart = enabledSince.current ?? Number.POSITIVE_INFINITY

  const errorIsCurrent = status.errorUpdatedAt >= periodStart
  const notImpersonating =
    errorIsCurrent && isNotImpersonating(status.error)
  const grant =
    status.data !== undefined &&
    status.dataUpdatedAt >= periodStart &&
    !notImpersonating
      ? status.data.grant
      : undefined

  // A bare 404 while the snapshot still says impersonating means the server
  // no longer has one. Run the ended path at most once per impersonation
  // interval: the status query is not evicted by the identity change, and a
  // fresh error object on some later automatic read must not run it again.
  // Afterwards a snapshot that still says impersonating starts the bounded
  // re-read this path needs in place of the expiry timer's.
  const handledPeriod = useRef(false)
  useEffect(() => {
    if (!enabled) {
      handledPeriod.current = false
      return undefined
    }
    if (!notImpersonating || handledPeriod.current) {
      return undefined
    }
    handledPeriod.current = true
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    // There is no recorded expiry on this path, so the bound runs from the
    // moment the 404 was handled: after it every replica's check-ttl cache
    // has certainly expired.
    const deadline = Date.now() + CHECK_TTL_MAX_MS

    const retry = async (): Promise<void> => {
      try {
        await session.restore()
      } catch {
        // A failed re-read leaves the banner usable; the next interval or
        // the tab becoming visible again tries again.
      }
      if (!session.getSnapshot().impersonating) {
        // Judged before the cancellation check: the flip itself disables
        // this read and clears the timer, but the settled identity still
        // needs the full change.
        await applyIdentityChange('ended')
        return
      }
      if (cancelled) {
        return
      }
      scheduleRetry()
    }

    const scheduleRetry = (): void => {
      if (cancelled || Date.now() >= deadline) {
        return
      }
      timer = setTimeout(() => {
        void retry()
      }, CHECK_TTL_RETRY_MS)
    }

    void (async (): Promise<void> => {
      await applyIdentityChange('ended')
      if (cancelled || !session.getSnapshot().impersonating) {
        return
      }
      scheduleRetry()
    })()

    return () => {
      cancelled = true
      if (timer !== undefined) {
        clearTimeout(timer)
      }
    }
  }, [enabled, notImpersonating, applyIdentityChange, session])

  const endsAt =
    grant === undefined ? null : Date.parse(grant.ends_at)

  // The expiry instant is held apart from the status read: the identity
  // change evicts every other cached row, so the grant briefly disappears
  // and comes back. Keying the timer on the read's value would restart it on
  // every refetch -- and, once the recorded time is past, restart it
  // immediately in a loop. The recorded ends_at is the value that matters,
  // and it does not change while the record is in force.
  const [expiryAt, setExpiryAt] = useState<number | null>(null)
  useEffect(() => {
    if (endsAt === null || Number.isNaN(endsAt)) {
      return
    }
    setExpiryAt((current) => (current === endsAt ? current : endsAt))
  }, [endsAt])

  useEffect(() => {
    if (!enabled || expiryAt === null) {
      return undefined
    }
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    // The upper bound runs from the recorded expiry: after it the server's
    // own check-ttl cache has certainly expired.
    const deadline = expiryAt + CHECK_TTL_MAX_MS

    const retry = async (): Promise<void> => {
      try {
        await session.restore()
      } catch {
        // A failed re-read leaves the banner usable; the next interval or
        // the tab becoming visible again tries again.
      }
      if (!session.getSnapshot().impersonating) {
        // The server has settled back to the operator: run the full
        // identity change so the cache and the host state follow. Judged
        // before the cancellation check, because the flip itself turns
        // `enabled` off and this effect's cleanup then sets `cancelled`.
        await applyIdentityChange('ended')
        return
      }
      if (cancelled) {
        return
      }
      scheduleRetry()
    }

    const scheduleRetry = (): void => {
      if (cancelled || Date.now() >= deadline) {
        return
      }
      timer = setTimeout(() => {
        void retry()
      }, CHECK_TTL_RETRY_MS)
    }

    const atExpiry = async (): Promise<void> => {
      await applyIdentityChange('ended')
      if (cancelled || !session.getSnapshot().impersonating) {
        return
      }
      scheduleRetry()
    }

    timer = setTimeout(
      () => {
        void atExpiry()
      },
      Math.max(0, expiryAt - Date.now()),
    )
    return () => {
      cancelled = true
      if (timer !== undefined) {
        clearTimeout(timer)
      }
    }
  }, [enabled, expiryAt, applyIdentityChange, session])

  return grant
}

/** The notice state the auth-ui provider and its consumers render from. */
export interface ImpersonationNoticeState {
  /** The current impersonation, or null when none is displayed. */
  readonly impersonation: ImpersonationBannerState | null
  /** The effective identity plus the host's identity generation: any
   * identity change resets a dismissal. */
  readonly identityKey: string
  /** Starts the one end-impersonation write attempt. */
  readonly onEnd: () => void
  /** Whether that shared attempt is in flight. */
  readonly pending: boolean
  /** The attempt's failure code, rendered inside the banner. */
  readonly failureCode: string | null
  /** The attempt's success message, announced in the banner's live region. */
  readonly successMessage: string | null
}

/**
 * The impersonation notice's host state: what the tenant frame tells the
 * auth-ui notice provider, and the one end write every entry point shares.
 *
 * The subject is the snapshot principal's display name -- during an
 * impersonation the principal is the impersonated account -- with the
 * account identifier as the fallback. The tenant, expiry and grant id come
 * from the status read; all stay absent when that read has not settled.
 *
 * The end attempt is one write-feedback scope, so the banner's control and
 * the account menu's item are the same attempt: both read `pending` and
 * disable together, and a rejection renders inside the banner. A bare 404
 * counts as settled (there was nothing left to end). The write starts the
 * identity change without waiting for it: the attempt's contract is the
 * write, and a failure of the re-read must not render as a failed end. The
 * banner is withheld from the settled instant -- the write's own result
 * says the impersonation is over, and only the snapshot still says
 * otherwise. The null is held through the bounded re-read described in
 * this file's header and released when a re-read settles the operator or
 * the bound runs out; a start never withholds, because starting is
 * announced by the re-read.
 */
export function useImpersonationNoticeState(
  grant: AuthnImpersonationGrant | undefined,
): ImpersonationNoticeState {
  const snapshot = useAuthState()
  const { session } = useAppServices()
  const { applyIdentityChange, generation } = useIdentityChange()
  const endImpersonation = useAuthnImpersonationEnd()
  const { t } = useTranslation(AUTH_UI_NAMESPACE)
  const feedback = useWriteFeedback()

  const impersonating =
    snapshot.state === 'authenticated' && snapshot.impersonating

  // Whether the banner is withheld after an explicit end whose re-read has
  // not settled the operator yet. Releases on a re-read that reports the
  // operator, or when the bounded re-read spends its check-ttl bound; a
  // snapshot that says no impersonation is in force always releases it, so
  // a later start renders normally.
  const [withheld, setWithheld] = useState(false)

  // Cancels an in-flight bounded re-read when another end starts or the
  // frame unmounts, so no timer outlives the banner.
  const stopReRead = useRef<(() => void) | null>(null)
  useEffect(
    () => () => {
      stopReRead.current?.()
    },
    [],
  )

  useEffect(() => {
    if (!impersonating) {
      setWithheld(false)
    }
  }, [impersonating])

  /**
   * Runs the identity change and holds the banner withheld until a re-read
   * stops naming the impersonated identity. The bound runs from the settled
   * write: after it every replica's check-ttl cache has certainly expired,
   * so a snapshot that still says impersonating is the one to render.
   */
  const holdUntilSettled = useCallback(async (): Promise<void> => {
    stopReRead.current?.()
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    stopReRead.current = () => {
      cancelled = true
      if (timer !== undefined) {
        clearTimeout(timer)
      }
    }
    const deadline = Date.now() + CHECK_TTL_MAX_MS

    // The only moment the null can be released without a re-read: once the
    // bound is spent the banner follows the snapshot again.
    const scheduleOrRelease = (): void => {
      if (cancelled) {
        return
      }
      if (Date.now() >= deadline) {
        setWithheld(false)
        return
      }
      timer = setTimeout(() => {
        void retry()
      }, CHECK_TTL_RETRY_MS)
    }

    // A retry that observes the flip runs the full change once more: rows
    // read while the snapshot still said impersonating belong to that
    // identity, and settle back out with it.
    const retry = async (): Promise<void> => {
      try {
        await session.restore()
      } catch {
        // A failed re-read leaves the banner withheld; the next interval
        // tries again.
      }
      if (cancelled) {
        return
      }
      if (!session.getSnapshot().impersonating) {
        await applyIdentityChange('ended')
        return
      }
      scheduleOrRelease()
    }

    await applyIdentityChange('ended')
    if (cancelled || !session.getSnapshot().impersonating) {
      return
    }
    scheduleOrRelease()
  }, [applyIdentityChange, session])

  // The shared attempt's duplicate guard. A synchronous double activation
  // can reach this before React commits the busy state; the ref closes that
  // window so every entry point really is one write.
  const inFlight = useRef(false)
  const end = useCallback((): void => {
    if (inFlight.current) {
      return
    }
    inFlight.current = true
    void feedback
      .run(async () => {
        try {
          await endImpersonation.mutateAsync()
        } catch (error) {
          if (!isNotImpersonating(error)) {
            throw error
          }
        }
        // The write settled, so the banner must not keep saying an
        // impersonation is in force; the snapshot may take up to one
        // check-ttl to agree. A snapshot that flipped while the write was
        // in flight -- an expiry, a bare 404 elsewhere, another tab --
        // already withdrew it, and withholding again would outlive the
        // flip: the release edge has run and will not run again, so a later
        // impersonation in this mount would stay hidden. The identity change
        // and its bounded re-read follow without being awaited: the
        // attempt's contract is the write.
        if (session.getSnapshot().impersonating) {
          setWithheld(true)
        }
        void holdUntilSettled()
        return t('impersonation.ended')
      })
      .finally(() => {
        inFlight.current = false
      })
  }, [endImpersonation, feedback, holdUntilSettled, session, t])

  const principal = snapshot.principal
  const subject =
    principal === null
      ? ''
      : principal.display_name !== ''
        ? principal.display_name
        : principal.account_id

  // A standing end confirmation or failure belongs to the impersonation
  // that produced it. A new impersonation (or a different impersonated
  // identity) starts with an empty attempt slot, so the previous
  // interval's text cannot cover the new "acting as" announcement. The
  // withheld window is exempt: the end just settled and its confirmation
  // is what the live region must keep showing.
  const resetFeedback = feedback.reset
  useEffect(() => {
    if (impersonating && !withheld) {
      resetFeedback()
    }
  }, [impersonating, withheld, subject, resetFeedback])

  const outcome = feedback.outcome

  return {
    impersonation:
      impersonating && !withheld
        ? {
            subject,
            ...(grant === undefined
              ? {}
              : {
                  tenant: grant.tenant,
                  expiresAt: new Date(grant.ends_at),
                  grantId: grant.id,
                }),
          }
        : null,
    identityKey: `${principal?.account_id ?? ''}:${generation}`,
    onEnd: end,
    pending: feedback.pending,
    failureCode: outcome?.kind === 'failure' ? outcome.code : null,
    successMessage: outcome?.kind === 'success' ? outcome.message : null,
  }
}

/**
 * The banner the tenant frame mounts in ProductShell's `banner` slot.
 *
 * It is always mounted: the notice's `impersonation` is non-null exactly
 * while the snapshot says the session impersonates and the end has not
 * withheld it, so the component's one live region survives the banner's
 * disappearance and keeps the end confirmation. The dismissed state, the
 * shared attempt's state and the region id come from auth-ui's notice
 * provider, which also supplies the header chip and the account menu's end
 * item, so all three tell the same story.
 */
export function ImpersonationBannerSlot(): ReactElement | null {
  const notice = useImpersonationNotice()
  if (notice === null) {
    return null
  }
  return (
    <ImpersonationBanner
      impersonation={notice.impersonation}
      onEnd={notice.end}
      pending={notice.pending}
      failureCode={notice.failureCode}
      successMessage={notice.successMessage}
      dismissed={notice.dismissed}
      onDismiss={notice.dismiss}
      regionId={notice.regionId}
    />
  )
}

/**
 * The header's "impersonating" chip, mounted in the `userMenu` slot ahead
 * of the account menu. It reads the same notice state as the banner and
 * is the disclosure control over it: activating toggles the banner, and
 * the banner's close control moves focus here.
 */
export function ImpersonationChip(): ReactElement | null {
  const notice = useImpersonationNotice()
  if (notice === null) {
    return null
  }
  return (
    <ImpersonationIndicator
      impersonation={notice.impersonation}
      expanded={!notice.dismissed}
      controls={notice.regionId}
      onToggle={notice.toggle}
      ref={notice.indicatorRef}
    />
  )
}
