/**
 * DisplayTimeZoneProvider: the host's display time zone, carried by
 * context.
 *
 * The context holds an IANA time zone name (for example
 * 'Asia/Shanghai') or `undefined`, and no validation happens here: the
 * pure validators live in `@speed/i18n`, and a provider that rejected
 * values itself would be a second, divergent authority. Rendering
 * surfaces read the context through `useDisplayTimeZone` and fall back to
 * their own defaults (a device zone, a stored preference) when it is
 * absent -- the default value is `undefined`, so a tree without the
 * provider renders exactly as before.
 *
 * The provider holds no state: the host owns the value and re-renders the
 * provider when it changes.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

/** The display time zone, or undefined when no provider supplies one. */
const DisplayTimeZoneContext = createContext<string | undefined>(undefined)

export interface DisplayTimeZoneProviderProps {
  /** An IANA time zone name; undefined means "no host zone". */
  readonly timeZone?: string
  readonly children?: ReactNode
}

/** Carries the host's display time zone to the surfaces below it. */
export function DisplayTimeZoneProvider({
  timeZone,
  children,
}: DisplayTimeZoneProviderProps) {
  return (
    <DisplayTimeZoneContext.Provider value={timeZone}>
      {children}
    </DisplayTimeZoneContext.Provider>
  )
}

/**
 * The host's display time zone, or `undefined` when no provider supplies
 * one. Consumers apply their own fallback; this package never guesses.
 */
export function useDisplayTimeZone(): string | undefined {
  return useContext(DisplayTimeZoneContext)
}
