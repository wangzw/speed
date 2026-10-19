/**
 * SignOutMenuItem: the sign-out action for an account menu.
 *
 * A menu item over the session's logout operation, for hosts whose
 * sign-out lives in a menu instead of a standalone button. Activating it
 * first hands the host its `onActivate` (typically closing the menu, while
 * the item is still mounted), then drives session.logout().
 *
 * Unlike SignOutButton there is no failure surface: logout is terminal.
 * auth-core clears the local session and flips the snapshot to anonymous
 * whether or not the server's revocation answered, so the menu (and this
 * item with it) is gone with the session; a refusal has no place to render
 * and nothing to retry. The refusal is therefore swallowed. The label is
 * the shared signOut.label text.
 *
 * Nothing here reads the session state: the host mounts the item where a
 * sign-out action belongs and unmounts it with the menu.
 */

import type { ReactElement } from 'react'
import MenuItem from '@mui/material/MenuItem'
import type { AuthSession } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'

export interface SignOutMenuItemProps {
  /** The session the sign-out drives. */
  readonly session: AuthSession
  /** Called after the item activates, typically to close the menu. */
  readonly onActivate?: () => void
}

export function SignOutMenuItem({
  session,
  onActivate,
}: SignOutMenuItemProps): ReactElement {
  const { t } = useAuthUiTranslation()

  const activate = (): void => {
    try {
      onActivate?.()
    } catch {
      // A throwing host callback is not a failure of the sign-out: the
      // session still ends below.
    }
    // Logout is terminal (see the file header): the local session clears
    // and the snapshot flips anonymous either way, so a refusal is not
    // this menu's to render.
    void session.logout().catch(() => {
      // Swallowed by design; the viewer is signed out regardless.
    })
  }

  return <MenuItem onClick={activate}>{t('signOut.label')}</MenuItem>
}
