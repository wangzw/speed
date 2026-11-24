/** The one entry a rendering surface formats an instant through. */

import { useCallback, useMemo } from 'react'
import { formatInstant, resolveTimeZone, useTranslation } from '@speed/i18n'
import type { FormatInstantOptions } from '@speed/i18n'
import { useDisplayTimeZone } from './display-time-zone.js'

/** The options `useFormatInstant` accepts. */
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
 * language. The returned function answers the empty string for a value
 * that carries no instant and never throws.
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
