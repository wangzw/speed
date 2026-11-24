/**
 * The invitation lifecycle's shared vocabulary. The state is the server's,
 * judged with the listing's clock: neither page derives expiry from the
 * browser's clock.
 */

import type { AuthnInviteInvitationState } from '@speed/api-sdk'
import type { StatusTone } from './status-tag.js'

/** The status a management row displays: the server-judged state. */
export type InvitationStatus = AuthnInviteInvitationState

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

export function statusLabel(
  status: InvitationStatus,
  t: (key: string) => string,
): string {
  return t(`invitations.status.${status}`)
}

export function statusTone(status: InvitationStatus): StatusTone {
  switch (status) {
    case 'pending':
      return 'info'
    case 'accepted':
      return 'success'
    default:
      return 'default'
  }
}

/** The terminal instant a settled invitation shows, or undefined when its field is null. */
export function terminalNote(
  invitation: {
    readonly accepted_at: string | null
    readonly revoked_at: string | null
    readonly expires_at: string
  },
  status: InvitationStatus,
  t: (key: string, options: Record<string, string>) => string,
  formatTime: (value: string) => string,
): string | undefined {
  const instant =
    status === 'accepted'
      ? invitation.accepted_at
      : status === 'expired'
        ? invitation.expires_at
        : status === 'revoked'
          ? invitation.revoked_at
          : null
  if (instant === null) {
    return undefined
  }
  return t(`invitations.statusSince.${status}`, { time: formatTime(instant) })
}

/** How soon a deadline is, when it falls within the next day. */
const EXPIRY_SOON_MS = 24 * 60 * 60 * 1000

/** The remaining time before a pending invitation's deadline, within a day only. */
export function remainingText(
  expiresAt: string,
  t: (key: string, options: Record<string, string>) => string,
): string | undefined {
  const remaining = Date.parse(expiresAt) - Date.now()
  if (Number.isNaN(remaining) || remaining <= 0 || remaining > EXPIRY_SOON_MS) {
    return undefined
  }
  const hours = Math.floor(remaining / 3_600_000)
  if (hours >= 1) {
    return t('invitations.expires.remainingHours', { hours: String(hours) })
  }
  const minutes = Math.max(1, Math.ceil(remaining / 60_000))
  return t('invitations.expires.remainingMinutes', { minutes: String(minutes) })
}
