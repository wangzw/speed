/** The host's preferred starting country for phone controls, carried by context. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import { defaultPhoneCountry } from './phone-number.js'

const PhoneCountryDefaultContext = createContext<string | undefined>(undefined)

export interface PhoneCountryDefaultProviderProps {
  /** An ISO 3166-1 alpha-2 country code; undefined means "no host default". */
  readonly country?: string
  /** The subtree that reads the preferred country through `usePhoneCountryDefault`. */
  readonly children?: ReactNode
}

/** Carries the host's preferred starting country to the controls below it. */
export function PhoneCountryDefaultProvider({
  country,
  children,
}: PhoneCountryDefaultProviderProps) {
  return (
    <PhoneCountryDefaultContext.Provider value={country}>
      {children}
    </PhoneCountryDefaultContext.Provider>
  )
}

/**
 * The host's preferred starting country when one is provided, otherwise
 * `defaultPhoneCountry()`'s browser-locale answer.
 */
export function usePhoneCountryDefault(): string {
  const provided = useContext(PhoneCountryDefaultContext)
  return provided ?? defaultPhoneCountry()
}
