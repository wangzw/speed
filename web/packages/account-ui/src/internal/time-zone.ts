/**
 * The time zone the account surfaces format times with: the zone the host
 * resolved for display. The account surfaces read no preference of their
 * own here -- the host fills the display-time-zone context from the profile
 * and re-renders it when the choice changes, so a second read would be a
 * second source of truth.
 */
import { useResolvedTimeZone } from '@speed/ui-kit'

/** The time zone a surface renders its instants in. */
export function useAccountTimeZone(): string {
  return useResolvedTimeZone()
}
