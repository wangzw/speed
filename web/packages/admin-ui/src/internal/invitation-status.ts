/**
 * The invitation lifecycle's shared vocabulary.
 *
 * Both management surfaces -- the organization page and the platform page --
 * render the same four states, so the label, the tone, the terminal note and
 * the near-expiry remainder are stated once here rather than restated at each
 * page. The state is the server's: the generated invitation view carries
 * `state`, judged with the same clock the listing's status filter uses, so
 * neither page derives expiry from the browser's clock.
 *
 * The filter value is sent as-is and an absent filter lists every state. The
 * closed set lives here rather than at each call site, so a state the server
 * adds is a visible change to one list.
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

/** A translated label of one invitation status. */
export function statusLabel(
  status: InvitationStatus,
  t: (key: string) => string,
): string {
  return t(`invitations.status.${status}`)
}

/**
 * The tone of one invitation status. Pending is the state still waiting for
 * an answer, so it takes the informational tone; accepted is the successful
 * end; expired and revoked are terminal without being good or bad news, so
 * both take the neutral tone. No invitation state uses the warning or error
 * tone: a terminal invitation is history, not a fault.
 */
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

/**
 * The terminal instant a settled invitation shows under its status tag.
 * Each terminal state reads its own field; a field the server reports as
 * null leaves the note out rather than inventing an instant. The caller
 * passes its display-time formatter, so a pure helper never picks a zone.
 */
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

/**
 * The remaining time before a pending invitation's deadline, shown beside
 * the deadline only when it is within a day. The server has already judged
 * the row pending; this text is a reading aid, not a state decision.
 */
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
