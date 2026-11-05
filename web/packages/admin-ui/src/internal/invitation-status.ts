/**
 * The management view's invitation status.
 *
 * The stored state has three values -- pending, accepted, revoked -- and
 * expiry is derived, not stored: an invitation whose deadline has passed
 * still reads `pending` from the server. The management face needs the
 * fourth value, `expired`, to label the row and to offer it as a filter, so
 * this module derives it once from the deadline and the current instant.
 * The server's status query uses the same vocabulary; a filter value is
 * sent as-is and an absent filter lists every state.
 *
 * The closed sets are declared here rather than restated at each call site,
 * so a status the server adds is a visible change to one list.
 */

import type { AuthnInviteInvitation } from '@speed/api-sdk'

/** The status a management row displays: the stored state plus expiry. */
export type InvitationStatus = 'pending' | 'expired' | 'accepted' | 'revoked'

/** The status filter's values: every state, or one of the four. */
export type InvitationStatusFilter = 'all' | InvitationStatus

/** The filter values in display order. */
export const INVITATION_STATUS_FILTERS: readonly InvitationStatusFilter[] = [
  'all',
  'pending',
  'expired',
  'accepted',
  'revoked',
]

/**
 * Whether a value is one of the four displayed statuses. A value outside the
 * set renders the generic label rather than a raw value.
 */
export function isInvitationStatus(value: string): value is InvitationStatus {
  return (
    value === 'pending' ||
    value === 'expired' ||
    value === 'accepted' ||
    value === 'revoked'
  )
}

/**
 * The displayed status of one invitation at `now`. A stored `pending`
 * invitation whose deadline is at or before `now` reads `expired`; accepted
 * and revoked are terminal and never expire. A deadline that does not parse
 * leaves the invitation pending: the row keeps the stored state rather than
 * inventing an expiry from malformed data.
 */
export function invitationStatusOf(
  invitation: Pick<AuthnInviteInvitation, 'status' | 'expires_at'>,
  now: number = Date.now(),
): InvitationStatus {
  if (invitation.status === 'accepted' || invitation.status === 'revoked') {
    return invitation.status
  }
  const deadline = Date.parse(invitation.expires_at)
  return Number.isNaN(deadline) || deadline > now ? 'pending' : 'expired'
}
