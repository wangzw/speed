/**
 * PhoneCountryDefaultProvider contract: the host's preferred starting
 * country wins when it supplies one, and the hook falls back to the
 * existing `defaultPhoneCountry()` answer otherwise. No validation is
 * applied here.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import {
  PhoneCountryDefaultProvider,
  usePhoneCountryDefault,
} from './phone-country-default.js'
import { defaultPhoneCountry } from './phone-number.js'

function wrapperOf(country?: string) {
  return ({ children }: { readonly children: ReactNode }) => (
    <PhoneCountryDefaultProvider country={country}>
      {children}
    </PhoneCountryDefaultProvider>
  )
}

describe('PhoneCountryDefaultProvider', () => {
  it('falls back to the browser default without a provider', () => {
    const { result } = renderHook(() => usePhoneCountryDefault())
    expect(result.current).toBe(defaultPhoneCountry())
  })

  it('returns the provider country when present', () => {
    const { result } = renderHook(() => usePhoneCountryDefault(), {
      wrapper: wrapperOf('DE'),
    })
    expect(result.current).toBe('DE')
  })

  it('falls back when the provider carries undefined', () => {
    const { result } = renderHook(() => usePhoneCountryDefault(), {
      wrapper: wrapperOf(undefined),
    })
    expect(result.current).toBe(defaultPhoneCountry())
  })

  it('passes a value the phone metadata does not cover through unchanged', () => {
    // The country set is the phone helpers' authority, not this context's.
    const { result } = renderHook(() => usePhoneCountryDefault(), {
      wrapper: wrapperOf('ZZ'),
    })
    expect(result.current).toBe('ZZ')
  })
})
