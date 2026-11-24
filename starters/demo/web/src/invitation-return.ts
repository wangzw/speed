import { INVITATION_PREFIX } from './paths.js'

const STORAGE_KEY = 'speed.invitation-return'

export function isInvitationLanding(path: string): boolean {
  const prefix = `${INVITATION_PREFIX}/`
  if (!path.startsWith(prefix)) {
    return false
  }
  const token = path.slice(prefix.length)
  return token !== '' && !token.includes('/')
}

export function rememberInvitationLanding(path: string): void {
  if (!isInvitationLanding(path)) {
    forgetInvitationLanding()
    return
  }
  try {
    window.sessionStorage.setItem(STORAGE_KEY, path)
  } catch {
  }
}

export function pendingInvitationLanding(): string | null {
  try {
    const path = window.sessionStorage.getItem(STORAGE_KEY)
    return path !== null && isInvitationLanding(path) ? path : null
  } catch {
    return null
  }
}

export function forgetInvitationLanding(): void {
  try {
    window.sessionStorage.removeItem(STORAGE_KEY)
  } catch {
  }
}
