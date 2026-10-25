/**
 * The invitation landing a registration started from, kept for the email
 * verification detour.
 *
 * A password registration lands on the account page's verification section
 * before the account may use the tenant area. When the visitor registered
 * from an invitation link, that landing has to survive the detour so the
 * invitation can still be accepted afterwards. The token is the invitation's
 * bearer secret: it travels in no request, no server-side record and no log
 * line, so the landing is held in the tab's own session storage and read once,
 * by the account page, after the address is proved.
 *
 * Only an invitation landing is ever stored or returned. A stale or foreign
 * value therefore cannot turn the way back into a navigation elsewhere.
 */

import { INVITATION_PREFIX } from './paths.js'

const STORAGE_KEY = 'speed.invitation-return'

/**
 * Whether the path is an invitation landing: the prefix, then exactly one
 * non-empty token segment.
 */
function isInvitationLanding(path: string): boolean {
  const prefix = `${INVITATION_PREFIX}/`
  if (!path.startsWith(prefix)) {
    return false
  }
  const token = path.slice(prefix.length)
  return token !== '' && !token.includes('/')
}

/**
 * Records the landing a password registration started from: the invitation
 * path when the visitor registered from one, and nothing otherwise. A
 * registration elsewhere clears an earlier landing, so the account page never
 * returns this registration to a link it did not come from.
 */
export function rememberInvitationLanding(path: string): void {
  if (!isInvitationLanding(path)) {
    forgetInvitationLanding()
    return
  }
  try {
    window.sessionStorage.setItem(STORAGE_KEY, path)
  } catch {
    // A tab that refuses storage keeps no way back; the visitor can still
    // reopen the invitation link itself.
  }
}

/**
 * The invitation landing remembered for this tab, if any. The value stays in
 * place: the account page clears it only when it actually returns, so a
 * verification that never settles leaves the way back intact.
 */
export function pendingInvitationLanding(): string | null {
  try {
    const path = window.sessionStorage.getItem(STORAGE_KEY)
    return path !== null && isInvitationLanding(path) ? path : null
  } catch {
    return null
  }
}

/** Drops the remembered landing once the visitor is on the way back. */
export function forgetInvitationLanding(): void {
  try {
    window.sessionStorage.removeItem(STORAGE_KEY)
  } catch {
    // Nothing to clear when the tab refuses storage.
  }
}
