/** The sign-out action for an account menu. */

import type { ReactElement } from 'react'
import MenuItem from '@mui/material/MenuItem'
import type { AuthSession } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'

/** The host supplies the session the item drives and an optional activation
 * callback; the item reads no session state. */
export interface SignOutMenuItemProps {
  /** The session the sign-out drives. */
  readonly session: AuthSession
  /** Called after the item activates, typically to close the menu. */
  readonly onActivate?: () => void
}

/**
 * Calls `onActivate` (a throw is contained), then drives `session.logout()`.
 * Logout is terminal, so a refusal is swallowed rather than rendered.
 */
export function SignOutMenuItem({
  session,
  onActivate,
}: SignOutMenuItemProps): ReactElement {
  const { t } = useAuthUiTranslation()

  const activate = (): void => {
    try {
      onActivate?.()
    } catch {
      // A throwing host callback is not a failure of the sign-out.
    }
    void session.logout().catch(() => {
      // Swallowed by design; the local session clears regardless.
    })
  }

  return <MenuItem onClick={activate}>{t('signOut.label')}</MenuItem>
}
