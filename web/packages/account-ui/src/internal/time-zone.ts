/**
 * The display-timezone chain the account surfaces format times with: the
 * account's stored preference wins, then the host's display-timezone context
 * (which replaces only the device tier), then the device zone, then UTC. Every
 * candidate is validated through the shared i18n helper, so the formatter
 * never receives a zone the engine refuses.
 */
import { useMemo } from 'react'
import { isSupportedTimeZone, resolveTimeZone } from '@speed/i18n'
import { useDisplayTimeZone } from '@speed/ui-kit'
import { useProfileQuery } from './queries.js'

/** The time zone a surface renders its instants in. */
export function useAccountTimeZone(): string {
  const { data: profile } = useProfileQuery()
  const hostTimeZone = useDisplayTimeZone()
  return useMemo(() => {
    const stored = profile?.time_zone?.trim() ?? ''
    if (stored !== '' && isSupportedTimeZone(stored)) {
      return stored
    }
    if (
      hostTimeZone !== undefined &&
      hostTimeZone !== '' &&
      isSupportedTimeZone(hostTimeZone)
    ) {
      return hostTimeZone
    }
    return resolveTimeZone(undefined)
  }, [profile?.time_zone, hostTimeZone])
}
