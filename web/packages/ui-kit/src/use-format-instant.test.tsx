/**
 * useFormatInstant / useResolvedTimeZone behaviour: the display-time-zone
 * context resolved through the shared chain, and instants formatted in the
 * resolved zone and the active UI language.
 *
 * The expected strings are computed with `@speed/i18n`'s own
 * `formatInstant`, so the suite pins how the hook wires zone, language and
 * style together rather than a locale's exact wording. The zone-fallback
 * cases are the point: an unusable provider value must not throw and must
 * not silently become the engine's process-local default.
 */

import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import {
  I18nextProvider,
  formatInstant,
  switchLanguage,
} from '@speed/i18n'
import { createTestI18n } from '@speed/test-utils/render'
import { DisplayTimeZoneProvider } from './display-time-zone.js'
import { useFormatInstant, useResolvedTimeZone } from './use-format-instant.js'

/** The browser zone this suite's vitest config pins (a non-UTC name), read
 * from the engine rather than restated, so a config change shows up as a
 * mismatch here. */
const BROWSER_TIME_ZONE = Intl.DateTimeFormat().resolvedOptions().timeZone

function wrapperFor(timeZone?: string, language = 'zh-CN') {
  const i18n = createTestI18n(language)
  const Wrapper = ({ children }: { readonly children: ReactNode }) => (
    <I18nextProvider i18n={i18n}>
      <DisplayTimeZoneProvider timeZone={timeZone}>{children}</DisplayTimeZoneProvider>
    </I18nextProvider>
  )
  return { i18n, Wrapper }
}

function bareWrapper(language = 'zh-CN') {
  const i18n = createTestI18n(language)
  const Wrapper = ({ children }: { readonly children: ReactNode }) => (
    <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
  )
  return { i18n, Wrapper }
}

const INSTANT = '2026-01-02T15:04:00Z'

describe('useResolvedTimeZone', () => {
  it('returns the provider value Intl accepts', () => {
    const { Wrapper } = wrapperFor('Asia/Tokyo')
    const { result } = renderHook(() => useResolvedTimeZone(), {
      wrapper: Wrapper,
    })
    expect(result.current).toBe('Asia/Tokyo')
  })

  it('falls back to the browser zone for an unusable provider value', () => {
    // The config pins a non-UTC browser zone; otherwise this assertion
    // would hold for an implementation that skips straight to UTC.
    expect(BROWSER_TIME_ZONE).not.toBe('UTC')
    const { Wrapper } = wrapperFor('Mars/Olympus')
    const { result } = renderHook(() => useResolvedTimeZone(), {
      wrapper: Wrapper,
    })
    expect(result.current).toBe(BROWSER_TIME_ZONE)
  })

  it('resolves to the browser zone when no provider supplies a zone', () => {
    expect(BROWSER_TIME_ZONE).not.toBe('UTC')
    const { Wrapper } = bareWrapper()
    const { result } = renderHook(() => useResolvedTimeZone(), {
      wrapper: Wrapper,
    })
    expect(result.current).toBe(BROWSER_TIME_ZONE)
  })
})

describe('useFormatInstant', () => {
  it('formats in the provider zone and the active language', () => {
    const { Wrapper } = wrapperFor('Asia/Tokyo', 'zh-CN')
    const { result } = renderHook(() => useFormatInstant(), {
      wrapper: Wrapper,
    })
    expect(result.current(INSTANT)).toBe(
      formatInstant(INSTANT, 'zh-CN', { timeZone: 'Asia/Tokyo' }),
    )
  })

  it('takes the requested length tier', () => {
    const { Wrapper } = wrapperFor('Asia/Tokyo', 'en-US')
    const { result } = renderHook(() => useFormatInstant(), {
      wrapper: Wrapper,
    })
    expect(result.current(INSTANT, { style: 'long' })).toBe(
      formatInstant(INSTANT, 'en-US', { timeZone: 'Asia/Tokyo', style: 'long' }),
    )
    expect(result.current(INSTANT, { style: 'short' })).toBe(
      formatInstant(INSTANT, 'en-US', { timeZone: 'Asia/Tokyo', style: 'short' }),
    )
  })

  it('re-renders in the new language after a language switch', async () => {
    const { i18n, Wrapper } = wrapperFor('Asia/Tokyo', 'zh-CN')
    const { result } = renderHook(() => useFormatInstant(), {
      wrapper: Wrapper,
    })
    const zh = result.current(INSTANT)
    expect(zh).toBe(formatInstant(INSTANT, 'zh-CN', { timeZone: 'Asia/Tokyo' }))

    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })

    const en = result.current(INSTANT)
    expect(en).toBe(formatInstant(INSTANT, 'en-US', { timeZone: 'Asia/Tokyo' }))
    expect(en).not.toBe(zh)
  })

  it('formats in the browser zone for an unusable provider value', () => {
    expect(BROWSER_TIME_ZONE).not.toBe('UTC')
    const { Wrapper } = wrapperFor('Mars/Olympus', 'en-US')
    const { result } = renderHook(() => useFormatInstant(), {
      wrapper: Wrapper,
    })
    expect(result.current(INSTANT)).toBe(
      formatInstant(INSTANT, 'en-US', { timeZone: BROWSER_TIME_ZONE }),
    )
  })

  it('renders an empty account time zone in the browser zone, not UTC', () => {
    // The host hands an unset account preference through as an empty value;
    // the surface must land on the browser's zone, not the terminal tier.
    expect(BROWSER_TIME_ZONE).not.toBe('UTC')
    const { Wrapper } = wrapperFor('', 'en-US')
    const { result } = renderHook(() => useFormatInstant(), {
      wrapper: Wrapper,
    })
    expect(result.current(INSTANT)).toBe(
      formatInstant(INSTANT, 'en-US', { timeZone: BROWSER_TIME_ZONE }),
    )
    expect(result.current(INSTANT)).not.toBe(
      formatInstant(INSTANT, 'en-US', { timeZone: 'UTC' }),
    )
  })

  it('answers the empty string for a value that carries no instant', () => {
    const { Wrapper } = wrapperFor('Asia/Tokyo', 'en-US')
    const { result } = renderHook(() => useFormatInstant(), {
      wrapper: Wrapper,
    })
    expect(result.current('')).toBe('')
    expect(result.current('not an instant')).toBe('')
    expect(result.current(new Date(Number.NaN))).toBe('')
  })
})
