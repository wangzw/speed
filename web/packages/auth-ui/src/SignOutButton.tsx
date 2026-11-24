/** The sign-out button over the session's logout operation. */

import { useState } from 'react'
import Button from '@mui/material/Button'
import type { AuthSession } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import { errorCodeOf, useActivationGuard } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'

/** The host supplies the session the button drives; the button reads no
 * session state and owns its pending and failure state. */
export interface SignOutButtonProps {
  /** The session the sign-out drives. */
  readonly session: AuthSession
}

/** Drives `session.logout()` on click; while in flight it keeps focus, carries
 * `aria-disabled` and `aria-busy`, and refuses a second activation. A refused
 * logout renders its code in one `InlineError` and stays retryable. */
export function SignOutButton({ session }: SignOutButtonProps) {
  const { t } = useAuthUiTranslation()
  const [pending, setPending] = useState(false)
  const [errorCode, setErrorCode] = useState<string | null>(null)

  const handleClick = async (): Promise<void> => {
    setErrorCode(null)
    setPending(true)
    try {
      await session.logout()
    } catch (error) {
      setErrorCode(errorCodeOf(error))
    } finally {
      setPending(false)
    }
  }

  // Refuse the second activation through the guard, not a native disabled
  // attribute, so the control that started the logout keeps focus.
  const activate = useActivationGuard(
    (): void => {
      void handleClick()
    },
    { busy: pending },
  )

  return (
    <>
      {/* color="inherit" follows the ambient text color: inside a colored
          AppBar the primary palette color would read primary-on-primary. */}
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
