
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { renderHook } from '@testing-library/react'
import {
  DisplayTimeZoneProvider,
  useDisplayTimeZone,
} from './display-time-zone.js'

function wrapperOf(timeZone?: string) {
  return ({ children }: { readonly children: ReactNode }) => (
    <DisplayTimeZoneProvider timeZone={timeZone}>{children}</DisplayTimeZoneProvider>
  )
}

describe('DisplayTimeZoneProvider', () => {
  it('returns undefined without a provider', () => {
    const { result } = renderHook(() => useDisplayTimeZone())
    expect(result.current).toBeUndefined()
  })

  it('carries the provided IANA time zone', () => {
    const { result } = renderHook(() => useDisplayTimeZone(), {
      wrapper: wrapperOf('Asia/Shanghai'),
    })
    expect(result.current).toBe('Asia/Shanghai')
  })

  it('passes an unvalidated value through verbatim', () => {
    const { result } = renderHook(() => useDisplayTimeZone(), {
      wrapper: wrapperOf('Not/AZone'),
    })
    expect(result.current).toBe('Not/AZone')
  })

  it('takes the nearest provider value', () => {
    const { result } = renderHook(() => useDisplayTimeZone(), {
      wrapper: ({ children }: { readonly children: ReactNode }) => (
        <DisplayTimeZoneProvider timeZone="Europe/Berlin">
          <DisplayTimeZoneProvider timeZone="America/New_York">
            {children}
          </DisplayTimeZoneProvider>
        </DisplayTimeZoneProvider>
      ),
    })
    expect(result.current).toBe('America/New_York')
  })
})
