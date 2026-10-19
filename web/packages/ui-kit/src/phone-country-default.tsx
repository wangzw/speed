/**
 * PhoneCountryDefaultProvider: the host's preferred starting country for
 * phone controls, carried by context.
 *
 * The context holds an ISO 3166-1 alpha-2 country code or `undefined`. A
 * host whose product serves one market sets it once, and a consumer that
 * calls `usePhoneCountryDefault()` gets that country instead of the
 * browser-locale answer. No validation happens here -- the phone
 * metadata's own country set is the authority (`isPhoneCountry` in
 * `src/phone-number.ts`) -- and the default value is `undefined`, so a
 * tree without the provider falls back to the existing
 * `defaultPhoneCountry()` result.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import { defaultPhoneCountry } from './phone-number.js'

/** The host's preferred starting country, or undefined when it sets none. */
const PhoneCountryDefaultContext = createContext<string | undefined>(undefined)

export interface PhoneCountryDefaultProviderProps {
  /** An ISO 3166-1 alpha-2 country code; undefined means "no host default". */
  readonly country?: string
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
 * `defaultPhoneCountry()`'s browser-locale answer. Consumers that need a
 * locale-specific answer keep calling `defaultPhoneCountry(locale)`
 * directly.
 */
export function usePhoneCountryDefault(): string {
  const provided = useContext(PhoneCountryDefaultContext)
  return provided ?? defaultPhoneCountry()
}
