/**
 * The time zone the account surfaces format times with: the zone the host
 * resolved for display. Reading a preference here would be a second source
 * of truth.
 */
import { useResolvedTimeZone } from '@speed/ui-kit'

/** The time zone a surface renders its instants in. */
export function useAccountTimeZone(): string {
  return useResolvedTimeZone()
}
