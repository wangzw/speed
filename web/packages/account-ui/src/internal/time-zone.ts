/**
 * The time zone the account surfaces format times with: the zone the host
 * resolved for display.
 */
import { useResolvedTimeZone } from '@speed/ui-kit'

export function useAccountTimeZone(): string {
  return useResolvedTimeZone()
}
