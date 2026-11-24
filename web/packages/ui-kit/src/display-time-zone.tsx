/** The host's display time zone, carried by context. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

const DisplayTimeZoneContext = createContext<string | undefined>(undefined)

export interface DisplayTimeZoneProviderProps {
  /** An IANA time zone name; undefined means "no host zone". */
  readonly timeZone?: string
  /** The subtree that resolves the host zone through `useDisplayTimeZone`. */
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

/** The host's display time zone, or `undefined` when no provider supplies one. */
export function useDisplayTimeZone(): string | undefined {
  return useContext(DisplayTimeZoneContext)
}
