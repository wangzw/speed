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
import type { AdminImpersonationStarting } from '@speed/admin-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { httpStatusOf, useWriteFeedback } from '@speed/ui-kit'
import {
  getAuthnImpersonationStatusQueryKey,
  useAuthnImpersonationEnd,
  useAuthnImpersonationStatus,
} from './app-api/index.js'
import type { AuthnImpersonationGrant } from './app-api/index.js'
import { useAppServices } from './app-services.js'
import { TENANT_HOME_PATH } from './pages/not-found-page.js'
import { clearSelfTenants } from './tenant-self.js'

const CHECK_TTL_MAX_MS = 60_000

const CHECK_TTL_RETRY_MS = 5_000

const STATUS_QUERY_KEY: readonly unknown[] = getAuthnImpersonationStatusQueryKey()

function isStatusQueryKey(key: readonly unknown[]): boolean {
  return (
    key.length === STATUS_QUERY_KEY.length &&
    key.every((part, index) => part === STATUS_QUERY_KEY[index])
  )
}

interface ReadHold {
  readonly resumeOnline: boolean
  readonly releaseTransition: () => void
  released: boolean
}

export type IdentityChange = 'started' | 'ended'

interface IdentityChangeContextValue {
  readonly applyIdentityChange: (change: IdentityChange) => Promise<void>
  readonly generation: number
  readonly onImpersonationStarting: AdminImpersonationStarting
}

const IdentityChangeContext =
  createContext<IdentityChangeContextValue | null>(null)

export function IdentityChangeProvider({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const { session, queryClient } = useAppServices()
  const navigate = useNavigate()
  const [generation, setGeneration] = useState(0)
  const holdRef = useRef<ReadHold | null>(null)

  const releaseReads = useCallback((): void => {
    const hold = holdRef.current
    if (hold === null) {
      return
    }
    hold.released = true
    holdRef.current = null
    hold.releaseTransition()
    if (hold.resumeOnline) {
      onlineManager.setOnline(true)
    }
  }, [])

  const holdReads = useCallback((): (() => void) => {
    let hold = holdRef.current
    if (hold === null) {
      const releaseTransition = session.beginIdentityTransition()
      void queryClient.cancelQueries()
      const created: ReadHold = {
        resumeOnline: onlineManager.isOnline(),
        releaseTransition,
        released: false,
      }
      hold = created
      holdRef.current = created
      if (created.resumeOnline) {
        setTimeout(() => {
          if (holdRef.current === created && !created.released) {
            onlineManager.setOnline(false)
          }
        }, 0)
      }
    }
    const held = hold
    return () => {
      if (holdRef.current === held) {
        releaseReads()
      }
    }
  }, [queryClient, releaseReads, session])

  useEffect(() => releaseReads, [releaseReads])

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
        // Evict every cached row except the status read: evicting a live status observer would make it re-read at once.
        queryClientRef.current.removeQueries({
          predicate: (query) => !isStatusQueryKey(query.queryKey),
        })
        clearSelfTenants()
        await sessionRef.current.restore()
        setGeneration((current) => current + 1)
        if (change === 'started') {
          navigateRef.current(TENANT_HOME_PATH)
        }
      } catch {
      } finally {
        releaseReadsRef.current()
      }
    },
    [],
  )

  return (
    <IdentityChangeContext.Provider
      value={{
        applyIdentityChange,
        generation,
        onImpersonationStarting: holdReads,
      }}
    >
      {children}
    </IdentityChangeContext.Provider>
  )
}

export function useIdentityChange(): IdentityChangeContextValue {
  const value = useContext(IdentityChangeContext)
  if (value === null) {
    throw new Error('useIdentityChange must be called within an IdentityChangeProvider')
  }
  return value
}

export function isNotImpersonating(error: unknown): boolean {
  return httpStatusOf(error) === 404
}

export function useImpersonationStatus(
  enabled: boolean,
): AuthnImpersonationGrant | undefined {
  const { session, queryClient } = useAppServices()
  const { applyIdentityChange } = useIdentityChange()
  const status = useAuthnImpersonationStatus({ query: { enabled } })

  useEffect(() => {
    if (!enabled) {
      queryClient.removeQueries({ queryKey: STATUS_QUERY_KEY })
    }
  }, [enabled, queryClient])

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
    const deadline = Date.now() + CHECK_TTL_MAX_MS

    const retry = async (): Promise<void> => {
      try {
        await session.restore()
      } catch {
      }
      if (!session.getSnapshot().impersonating) {
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
    const deadline = expiryAt + CHECK_TTL_MAX_MS

    const retry = async (): Promise<void> => {
      try {
        await session.restore()
      } catch {
      }
      if (!session.getSnapshot().impersonating) {
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

export interface ImpersonationNoticeState {
  readonly impersonation: ImpersonationBannerState | null
  readonly identityKey: string
  readonly onEnd: () => void
  readonly pending: boolean
  readonly failureCode: string | null
  readonly successMessage: string | null
}

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

  const [withheld, setWithheld] = useState(false)

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

    const retry = async (): Promise<void> => {
      try {
        await session.restore()
      } catch {
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
  const username =
    principal?.identifiers.find((identifier) => identifier.kind === 'username')
      ?.value ?? ''
  const subject =
    principal === null
      ? ''
      : principal.display_name !== '' && username !== ''
        ? `${principal.display_name} (${username})`
        : username !== ''
          ? username
          : principal.display_name !== ''
            ? principal.display_name
            : t('impersonation.accountUnavailable')

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

export function ImpersonationChip({
  narrow = false,
}: {
  readonly narrow?: boolean
} = {}): ReactElement | null {
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
      compact={narrow}
    />
  )
}
