/**
 * NotificationStreamProvider: the live-update subscription for the
 * notification surface.
 *
 * The backend pushes "the inbox just gained a row" over the
 * server-sent-event stream at GET /api/v1/notifications/stream (the
 * notification.stream module); this provider opens that stream through
 * the host-supplied opener (the @speed/api-client createClient's
 * openEventStream, wired from the assembly) and, on every `message`
 * frame, invalidates the acting tenant's unread count and message list so
 * the header bell and the inbox re-read. It carries no message content of
 * its own -- the frame names only a message id (see the stream design) --
 * so the re-read is the point, never a patched cache.
 *
 * Reconnect is the provider's policy, not the transport's: the opener
 * reports a dropped or ended connection once, and the provider re-opens
 * with capped exponential backoff, resetting the backoff whenever a
 * frame arrives. The page's own visibility gates it too -- a hidden tab
 * closes the connection (the backend sends nothing a background tab
 * needs, and a long-lived connection per hidden tab is waste) and a
 * visible tab reconnects at once, so a returned viewer sees the latest
 * state without waiting out a backoff.
 *
 * When the host mounts no opener (a deployment without the stream, or a
 * read-only surface), no connection is opened and the surfaces still
 * read their data on mount and when the tab becomes visible again --
 * real-time is an enhancement, never a prerequisite.
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

/**
 * The stream path, taken from the generated contract's own query-key
 * constructor (the orval surface's first — and only — segment) rather
 * than hand-written, so the frontend names no endpoint path itself.
 */
const NOTIFICATION_STREAM_PATH = getNotificationStreamSubscribeQueryKey()[0]

/** Base delay before the first reconnect attempt. */
const RECONNECT_BASE_MS = 1000
/** Cap on the reconnect backoff. */
const RECONNECT_MAX_MS = 30000
/** Cap on the backoff exponent, so 2 ** n stays a sane number. */
const RECONNECT_MAX_EXPONENT = 6

const NotificationStreamContext = createContext<EventStreamOpener | null>(null)

export interface NotificationStreamProviderProps {
  /**
   * The event-stream opener (the assembled client's `openEventStream`).
   * Absent, no stream is opened and the subscription is inert.
   */
  readonly openStream?: EventStreamOpener
  /**
   * Whether to hold the connection open. Defaults to true. A host passes
   * false when its own state says the surface should not be streaming yet
   * -- before sign-in, say -- so the subscription opens only for a viewer
   * who may read it (the stream is authenticated; connecting anonymously
   * would only 401 and reconnect). The gate is the host's because it is
   * the host that owns the session; this package reads no session state
   * for it.
   */
  readonly enabled?: boolean
  /**
   * A counter the host bumps whenever the identity behind the connection
   * changes -- starting or ending an impersonation, whose effective
   * account swaps without the tenant necessarily changing. The
   * subscription registers per (tenant, effective account), so a bump
   * closes the current connection and opens a new one; children stay
   * mounted, because the identity changed, not the page. Defaults to 0,
   * which never triggers a rebuild.
   */
  readonly identityGeneration?: number
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
        // Keyed by the identity generation, not by the children: a bump
        // tears the subscription down and re-registers it under the new
        // effective account, while the surfaces below stay mounted.
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

/** Opens the stream for the acting tenant and re-reads on each frame;
 * renders nothing. */
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
    // Whether the connection that just ended was refused by the admission
    // layer. The transport reports a failure through onError and then the
    // terminal state through onClose, so the two callbacks carry one
    // connection's outcome.
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
        // A failure and a clean server end both mean "reconnect": the
        // transport neither retries nor reconnects (see event-stream.ts),
        // so the policy lives here. Both terminal states surface through
        // onClose -- the transport fires it for a failure too, right after
        // onError -- so subscribing to onClose alone reconnects exactly
        // once per drop. Subscribing to onError as well would schedule
        // twice per failure, skipping the base delay and doubling the
        // backoff step.
        //
        // The one failure that stops the loop is the admission layer's
        // refusal: retrying cannot change it, and every attempt would
        // refresh a console error. A tenant or identity change re-registers
        // the subscription, and a hidden tab that becomes visible opens
        // once, so both paths still recover.
        onError: (error: unknown) => {
          // The admission layer's refusal is classified by auth-core, so a
          // stream failure that crossed the transport boundary reads the
          // same way the surfaces read it.
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
