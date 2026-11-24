/**
 * Carries the host's notification feature-presence facts by context. A
 * tree without a provider fails closed: no preference surface and no
 * optional channel.
 */

import { createContext, useContext, useMemo } from 'react'
import type { ReactNode } from 'react'

/** The presence facts the provider supplies: whether the preference
 * feature is served and which optional channels it offers. */
export type NotificationPresenceProviderProps = {
  /** Whether the feature `notification.preferences` is served. */
  readonly preferences: boolean
  /** The optional delivery channels the deployment serves, beside the
   * always-on in-app channel; the host maps the serving
   * `notification.channel.*` features onto it. */
  readonly channels: readonly ('email' | 'sms')[]
  /** The subtree that reads these facts. */
  readonly children: ReactNode
}

type NotificationPresence = {
  readonly preferences: boolean
  readonly channels: readonly ('email' | 'sms')[]
}

const ABSENT: NotificationPresence = { preferences: false, channels: [] }

const NotificationPresenceContext = createContext<NotificationPresence>(ABSENT)

/** Carries the host's notification feature-presence facts to the surfaces
 * below it. */
export function NotificationPresenceProvider({
  preferences,
  channels,
  children,
}: NotificationPresenceProviderProps) {
  const value = useMemo(() => ({ preferences, channels }), [preferences, channels])
  return (
    <NotificationPresenceContext.Provider value={value}>
      {children}
    </NotificationPresenceContext.Provider>
  )
}

/**
 * The host's notification presence facts. Without a provider both facts
 * fail closed (no preference matrix, no optional channel).
 */
export function useNotificationPresence(): NotificationPresence {
  return useContext(NotificationPresenceContext)
}
