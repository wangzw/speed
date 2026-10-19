/**
 * NotificationPresenceProvider: the host's notification feature-presence
 * facts, carried by context.
 *
 * The same shape as NotificationStreamProvider: the props are facts the
 * host read from its feature document, and this package reads no feature
 * state for them. Two facts travel together because the surfaces consume
 * them together -- whether the preference matrix exists at all, and which
 * optional delivery channels the deployment serves beside the always-on
 * in-app channel.
 *
 * The default value fails closed: a tree without a provider reads
 * `preferences` false and `channels` empty, so the notification surface
 * renders its inbox alone and a standalone preference matrix offers only
 * the in-app column. There is no transitional branch in between -- a
 * surface never guesses that a preference matrix or a channel exists.
 */

import { createContext, useContext, useMemo } from 'react'
import type { ReactNode } from 'react'

export type NotificationPresenceProviderProps = {
  /** Whether the feature `notification.preferences` is served. */
  readonly preferences: boolean
  /** The optional delivery channels the deployment serves, beside the
   * always-on in-app channel; the host maps the serving
   * `notification.channel.*` features onto it. */
  readonly channels: readonly ('email' | 'sms')[]
  readonly children: ReactNode
}

type NotificationPresence = {
  readonly preferences: boolean
  readonly channels: readonly ('email' | 'sms')[]
}

/** The fail-closed value: no preference surface, no optional channel. */
const ABSENT: NotificationPresence = { preferences: false, channels: [] }

const NotificationPresenceContext = createContext<NotificationPresence>(ABSENT)

/** Carries the host's notification feature-presence facts to the surfaces
 * below it. */
export function NotificationPresenceProvider({
  preferences,
  channels,
  children,
}: NotificationPresenceProviderProps) {
  // A fresh object per render would re-render every consumer whenever the
  // host's provider re-renders; the value changes only with the facts.
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
