/**
 * SignOutButton: the sign-out action over the session's logout operation.
 *
 * A click drives session.logout(); while the request is in flight the
 * button keeps focus, carries aria-disabled and aria-busy, refuses a
 * second activation and shows the signOut.busy label; a failed logout
 * renders the answer's code text in one InlineError and leaves the button
 * ready for another click. Logout is deliberately quiet and terminal
 * either way: auth-core clears the local session and flips the snapshot to
 * anonymous whether or not the server's revocation answered -- a session
 * it could not revoke is no longer trustworthy locally -- so the host's
 * own auth-core hooks observe the same flip on both paths, and the
 * component never navigates and fires no completion callback. The banner
 * is what a refused logout still owes the viewer: the code of the refusal,
 * shown against a button that is already signed out.
 *
 * Nothing here reads the session state: the button renders whether the
 * snapshot is authenticated or not, and the host mounts it where a
 * sign-out action belongs (typically app chrome).
 *
 * The button renders with `color="inherit"` by default, so its text
 * follows the ambient text color instead of defaulting to the primary
 * palette color: mounted inside a colored header (the common host
 * placement, an AppBar whose background is the primary color), an
 * inherited color resolves to the surface's own contrastText and stays
 * legible where a primary-on-primary default would vanish (pinned by
 * this package's colour-inheritance test). On a plain surface the same
 * inheritance resolves to the surrounding text color, which is equally
 * safe.
 */

import { useState } from 'react'
import Button from '@mui/material/Button'
import type { AuthSession } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import { errorCodeOf, useActivationGuard } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'

export interface SignOutButtonProps {
  /** The session the sign-out drives. */
  readonly session: AuthSession
}

export function SignOutButton({ session }: SignOutButtonProps) {
  const { t } = useAuthUiTranslation()
  const [pending, setPending] = useState(false)
  const [errorCode, setErrorCode] = useState<string | null>(null)

  const handleClick = async (): Promise<void> => {
    setErrorCode(null)
    setPending(true)
    try {
      await session.logout()
      // Success is the host's to observe: the snapshot flipped anonymous
      // and the host unmounts this button; if it does not, the finally
      // below clears the busy state for the next click (logout is
      // idempotent server-side -- a second call answers 204 too).
    } catch (error) {
      // A refused logout still cleared the local session (auth-core
      // clears unconditionally and rejects); the banner carries the
      // refusal's code against an already signed-out button.
      setErrorCode(errorCodeOf(error))
    } finally {
      setPending(false)
    }
  }

  // The button stays focusable while the logout runs, so the write is
  // refused by the guard rather than by a native disabled attribute
  // (which would drop focus from the control that started it).
  const activate = useActivationGuard(
    (): void => {
      void handleClick()
    },
    { busy: pending },
  )

  return (
    <>
      {/* Inherit the ambient text color: hosts mount this control inside
          a colored AppBar whose own text is its contrastText -- the
          default primary palette color reads primary-on-primary on that
          surface. See the file header for the full rationale. */}
      <Button
        type="button"
        color="inherit"
        aria-disabled={pending ? true : undefined}
        aria-busy={pending ? true : undefined}
        onClick={activate}
      >
        {t(pending ? 'signOut.busy' : 'signOut.label')}
      </Button>
      <InlineError code={errorCode} />
    </>
  )
}
