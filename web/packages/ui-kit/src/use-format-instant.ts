/**
 * The one entry a rendering surface formats an instant through.
 *
 * The display time zone is the host's `DisplayTimeZoneProvider` value,
 * resolved through `@speed/i18n`'s three-tier chain: the provider value
 * when Intl accepts it, the browser's own zone when the value is absent or
 * unusable, and UTC as the terminal tier. The language is the active i18n
 * language, so switching the UI language re-renders the surface's times in
 * the new language without a separate subscription.
 *
 * `useResolvedTimeZone` exposes that resolved zone on its own: a surface
 * that needs the zone for something other than formatting (an input that
 * interprets a wall clock) reads it here rather than rebuilding the chain.
 */

import { useCallback, useMemo } from 'react'
import { formatInstant, resolveTimeZone, useTranslation } from '@speed/i18n'
import type { FormatInstantOptions } from '@speed/i18n'
import { useDisplayTimeZone } from './display-time-zone.js'

/** The length tier `useFormatInstant` accepts; the zone is never a caller's
 * decision, so it is not part of these options. */
export interface UseFormatInstantOptions {
  /** Length tier: short, medium (the default) or long. */
  readonly style?: NonNullable<FormatInstantOptions['style']>
}

/**
 * The zone the surfaces below the provider render instants in: the provider
 * value when Intl accepts it, else the browser zone, else UTC. The answer is
 * always usable as an `Intl.DateTimeFormat` `timeZone`.
 */
export function useResolvedTimeZone(): string {
  const hostTimeZone = useDisplayTimeZone()
  return useMemo(() => resolveTimeZone(hostTimeZone), [hostTimeZone])
}

/**
 * A formatter for instants in the resolved display zone and the active UI
 * language. The returned function answers the empty string for a value that
 * carries no instant, exactly like `formatInstant`; it never throws for a
 * bad language or a hostile zone.
 */
export function useFormatInstant(): (
  value: string | number | Date,
  options?: UseFormatInstantOptions,
) => string {
  const { i18n } = useTranslation()
  const timeZone = useResolvedTimeZone()
  const language = i18n.language
  return useCallback(
    (value: string | number | Date, options: UseFormatInstantOptions = {}) =>
      formatInstant(value, language, { timeZone, style: options.style }),
    [language, timeZone],
  )
}
