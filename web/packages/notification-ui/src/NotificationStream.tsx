/**
 * The live-update subscription for the notification surface: opens the
 * host-supplied stream and invalidates the acting tenant's reads on each
 * `message` frame. Reconnect and hidden-tab pausing are its own policy.
 */

import { createContext, useContext, useEffect } from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { EventStreamHandle, EventStreamOpener } from '@speed/api-client'
import {
  getNotificationInboxGetUnreadCountQueryKey,
  getNotificationInboxListMessagesQueryKey,
  getNotificationStreamSubscribeQueryKey,
} from '@speed/api-sdk'
import {
  isTenantAdmissionRefusal,
  tenantQueryKey,
  useCurrentTenant,
} from '@speed/auth-core'

const NOTIFICATION_STREAM_PATH = getNotificationStreamSubscribeQueryKey()[0]

const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 30000
const RECONNECT_MAX_EXPONENT = 6

const NotificationStreamContext = createContext<EventStreamOpener | null>(null)

/** Props for the stream provider: the host's opener, the enabled gate, and
 * the identity counter that reopens the connection. */
export interface NotificationStreamProviderProps {
  /**
   * The event-stream opener. Absent, no stream is opened and the
   * subscription is inert.
   */
  readonly openStream?: EventStreamOpener
  /**
   * Whether to hold the connection open; defaults to true. The host passes
   * false while the surface should not stream yet.
   */
  readonly enabled?: boolean
  /**
   * A counter the host bumps when the effective identity changes (for
   * example at the start or end of an impersonation). A bump reopens the
   * connection; children stay mounted. Defaults to 0.
   */
  readonly identityGeneration?: number
  /** The subtree that stays mounted across reconnects. */
  readonly children: ReactNode
}

/** The live-update subscription provider; wraps the surfaces that should
 * follow server-pushed updates. */
export function NotificationStreamProvider({
  openStream,
  enabled = true,
  identityGeneration = 0,
  children,
}: NotificationStreamProviderProps): ReactElement {
  return (
    <NotificationStreamContext.Provider value={openStream ?? null}>
      {openStream !== undefined && enabled && (
        <NotificationStreamSubscriber
          key={identityGeneration}
          openStream={openStream}
        />
      )}
      {children}
    </NotificationStreamContext.Provider>
  )
}

/** The opener a nested component reads, or null when the host mounted
 * none. */
export function useNotificationStreamOpener(): EventStreamOpener | null {
  return useContext(NotificationStreamContext)
}

function NotificationStreamSubscriber({
  openStream,
}: {
  readonly openStream: EventStreamOpener
}): null {
  const queryClient = useQueryClient()
  const tenantId = useCurrentTenant()?.tenantId ?? null

  useEffect(() => {
    if (tenantId === null) {
      return undefined
    }
    let handle: EventStreamHandle | null = null
    let closed = false
    let retries = 0
    let timer: ReturnType<typeof setTimeout> | undefined
    let refused = false

    const invalidate = (): void => {
      void queryClient.invalidateQueries({
        queryKey: tenantQueryKey(
          tenantId,
          ...getNotificationInboxListMessagesQueryKey(),
        ),
      })
      void queryClient.invalidateQueries({
        queryKey: tenantQueryKey(
          tenantId,
          ...getNotificationInboxGetUnreadCountQueryKey(),
        ),
      })
    }

    const clearTimer = (): void => {
      if (timer !== undefined) {
        clearTimeout(timer)
        timer = undefined
      }
    }

    const connect = (): void => {
      timer = undefined
      if (closed || document.visibilityState !== 'visible') {
        return
      }
      refused = false
      handle = openStream(NOTIFICATION_STREAM_PATH, {
        onEvent: (event) => {
          if (event.event !== 'message') {
            return
          }
          retries = 0
          invalidate()
        },
        // The transport fires onError then onClose for one connection's
        // failure; only onClose schedules, or a failure would schedule twice
        // and skip the base delay.
        onError: (error: unknown) => {
          refused = isTenantAdmissionRefusal(error)
        },
        onClose: () => {
          if (refused) {
            refused = false
            handle = null
            return
          }
          schedule()
        },
      })
    }

    const schedule = (): void => {
      if (closed) {
        return
      }
      handle = null
      const exponent = Math.min(retries, RECONNECT_MAX_EXPONENT)
      const delay = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** exponent)
      retries += 1
      clearTimer()
      timer = setTimeout(connect, delay)
    }

    const onVisibility = (): void => {
      if (document.visibilityState === 'visible') {
        clearTimer()
        if (handle === null) {
          retries = 0
          connect()
        }
      } else {
        clearTimer()
        handle?.close()
        handle = null
      }
    }

    connect()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      closed = true
      document.removeEventListener('visibilitychange', onVisibility)
      clearTimer()
      handle?.close()
      handle = null
    }
  }, [openStream, queryClient, tenantId])

  return null
}
