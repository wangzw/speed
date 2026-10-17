/**
 * The impersonation shell's host half: one identity-change action shared by
 * both areas, the status read the banner renders from, and the banner the
 * tenant frame mounts.
 *
 * Starting or ending an impersonation swaps whose identity every later read
 * belongs to: the query cache's rows were taken under the old identity, the
 * session snapshot still carries the old principal, the host-attached
 * platform permission set was read under the old identity (and is retained
 * by restore(), which cannot know the identity changed), the current tenant
 * may differ, and the live notification subscription is registered per
 * effective account. applyIdentityChange is the one action that settles all
 * of them: it evicts the cache, resets the platform permission set to
 * unknown, re-reads the session, and then lets the identity-keyed effects
 * (app.tsx's tenant effect and platform-permissions.tsx's read) re-read the
 * permission set and the current tenant; it bumps an identity generation the
 * tenant frame uses to re-register the notification stream. A start then
 * sends the viewer to the tenant-area home; an end navigates nowhere,
 * because the page is already a tenant page.
 *
 * The action never rejects. Its callers are settled writes (the banner's
 * end, the console page's start/end callback) and the expiry timer; a
 * failure here must not overturn a write the server already accepted.
 *
 * The status read is enabled only while the snapshot says an impersonation
 * is in force, so a visitor who is not impersonating issues no
 * GET /auth/impersonation at all. While it is enabled, a bare 404 is the
 * settled "no impersonation any more" (ended, expired, or terminated
 * elsewhere) and runs the ended path. The read is the only source of the
 * banner's tenant and expiry: the roster read belongs to the platform
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
 * leaves the banner and its end control in place, and a refocus re-reads
 * through the query client's staleTime 0.
 */

import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState } from '@speed/auth-core'
import { ImpersonationBanner } from '@speed/auth-ui'
import { useNavigate } from '@speed/route-kit'
import {
  useAuthnImpersonationEnd,
  useAuthnImpersonationStatus,
} from './app-api/index.js'
import type { AuthnImpersonationGrant } from './app-api/index.js'
import { useAppServices } from './app-services.js'
import { TENANT_HOME_PATH } from './pages/not-found-page.js'

/** The platform permission domain the console's codes resolve in (auth-core
 * names 'system' the tenant-independent platform set). */
const PLATFORM_DOMAIN = 'system'

/** The upper bound of authn.session.check-ttl: how long after the recorded
 * expiry the server may still treat the session as impersonating. */
const CHECK_TTL_MAX_MS = 60_000

/** The fixed interval between re-reads while the server still answers
 * impersonating after the recorded expiry (the check-ttl default). */
const CHECK_TTL_RETRY_MS = 5_000

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

  const applyIdentityChange = useCallback(
    async (change: IdentityChange): Promise<void> => {
      try {
        // Every cached row belongs to the identity that was current when it
        // was read; the session-end strategy evicts the same way.
        queryClient.removeQueries()
        // restore() retains the sets the host attached, and cannot know the
        // identity changed, so the set is reset to unknown here and re-read
        // by the identity-keyed effect in platform-permissions.tsx.
        session.setPermissionSet(PLATFORM_DOMAIN, null)
        await session.restore()
        setGeneration((current) => current + 1)
        if (change === 'started') {
          navigate(TENANT_HOME_PATH)
        }
      } catch {
        // A failed re-read leaves the cache evicted and the sets unknown
        // (fail-closed); it never reaches the settled write that called it.
      }
    },
    [navigate, queryClient, session],
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
 * the ended path once, and the recorded expiry schedules the identity
 * change plus the bounded re-read described in this file's header.
 */
export function useImpersonationStatus(
  enabled: boolean,
): AuthnImpersonationGrant | undefined {
  const { session } = useAppServices()
  const { applyIdentityChange } = useIdentityChange()
  const status = useAuthnImpersonationStatus({ query: { enabled } })
  const error = status.error
  const notImpersonating = isNotImpersonating(error)
  const grant = notImpersonating ? undefined : status.data?.grant

  // A bare 404 while the snapshot still says impersonating means the
  // server no longer has one. Handle each error object once: the same
  // cached error must not re-run the identity change on every render.
  const handledError = useRef<unknown>(null)
  useEffect(() => {
    if (!enabled || !isNotImpersonating(error)) {
      return
    }
    if (handledError.current === error) {
      return
    }
    handledError.current = error
    void applyIdentityChange('ended')
  }, [enabled, error, applyIdentityChange])

  const endsAt =
    grant === undefined ? null : Date.parse(grant.ends_at)

  // The expiry instant is held apart from the status read: the identity
  // change evicts the query, so the grant briefly disappears and comes back.
  // Keying the timer on the read's value would restart it on every refetch --
  // and, once the recorded time is past, restart it immediately in a loop.
  // The recorded ends_at is the value that matters, and it does not change
  // while the record is in force.
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
        // A failed re-read leaves the banner usable; the next interval or a
        // refocus tries again.
      }
      if (cancelled) {
        return
      }
      if (!session.getSnapshot().impersonating) {
        // The server has settled back to the operator: run the full
        // identity change so the cache and the host state follow.
        await applyIdentityChange('ended')
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

/**
 * The banner the tenant frame mounts in ProductShell's `banner` slot.
 *
 * It is always mounted: `impersonation` is non-null exactly while the
 * snapshot says the session impersonates, so the component's one live
 * region survives the banner's disappearance and keeps the end
 * confirmation. The subject is the snapshot principal's display name --
 * during an impersonation the principal is the impersonated account --
 * with the account identifier as the fallback. The tenant and expiry come
 * from the status read; both stay absent when that read has not settled.
 *
 * The end action resolves when the DELETE settles, and a bare 404 counts
 * as settled (there was nothing left to end). It starts the identity
 * change without waiting for it: the banner's contract is the write, and
 * a failure of the re-read must not render as a failed end.
 */
export function ImpersonationBannerSlot({
  grant,
}: {
  readonly grant: AuthnImpersonationGrant | undefined
}): ReactElement {
  const snapshot = useAuthState()
  const { applyIdentityChange } = useIdentityChange()
  const endImpersonation = useAuthnImpersonationEnd()

  const impersonating =
    snapshot.state === 'authenticated' && snapshot.impersonating

  const end = useCallback(async (): Promise<void> => {
    try {
      await endImpersonation.mutateAsync()
    } catch (error) {
      if (!isNotImpersonating(error)) {
        throw error
      }
    }
    void applyIdentityChange('ended')
  }, [applyIdentityChange, endImpersonation])

  const principal = snapshot.principal
  const subject =
    principal === null
      ? ''
      : principal.display_name !== ''
        ? principal.display_name
        : principal.account_id

  return (
    <ImpersonationBanner
      impersonation={
        impersonating
          ? {
              subject,
              ...(grant === undefined
                ? {}
                : { tenant: grant.tenant, expiresAt: new Date(grant.ends_at) }),
            }
          : null
      }
      onEnd={end}
    />
  )
}
