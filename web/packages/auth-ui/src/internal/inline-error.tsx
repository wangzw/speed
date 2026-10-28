/**
 * InlineError: the code-level failure banner of the sign-in family.
 *
 * Every submit path of the family resolves its failure to one error
 * code and renders it through this banner instead of laying out
 * per-field error text (frontend standards: field errors belong to the
 * fields, whole-attempt failures belong to one alert). The banner
 * itself -- the alert shell and the failure classifier -- is ui-kit's
 * shared InlineError / errorCodeOf; this module is the family's binding
 * of it to the auth-ui namespace's error-text resolver, so call sites
 * keep passing a bare code and the resolved text can only ever come
 * from this namespace (whitelisted codes have their own errors-section
 * key, anything else renders the errors.unknown fallback).
 */

import { useCallback } from 'react'
import type { ReactNode } from 'react'
import { InlineError as SharedInlineError } from '@speed/ui-kit'
import { useAuthUiErrorText } from './error-text.js'
import { useAuthUiTranslation } from './translation.js'
import type { OpenFlowState } from './flow.js'

/** The refusal of a login name another account holds. */
const USERNAME_TAKEN = 'authn.username_taken'

/** The refusal of a login name that does not fit the server's rule. */
const BAD_REQUEST = 'authn.bad_request'

/** The refusal of a login name the system keeps for itself. */
const USERNAME_RESERVED = 'authn.username_reserved'

export interface InlineErrorProps {
  /** The code to render, or null for no error (renders nothing). */
  readonly code: string | null
  /** Content rendered inside the banner, below the message. */
  readonly detail?: ReactNode
  /**
   * A resolver to use instead of the namespace's own. The surface binds it
   * when the same code means something narrower in its current step.
   */
  readonly resolve?: (code: string) => string
}

/** The family's whole-attempt failure banner, one role="alert" per error. */
export function InlineError({ code, detail, resolve }: InlineErrorProps) {
  const fallback = useAuthUiErrorText()
  return (
    <SharedInlineError
      code={code}
      resolve={resolve ?? fallback}
      detail={detail}
    />
  )
}

/**
 * The family's error text adjusted for the flow's username step.
 *
 * A first login through a method that opens an account parks on the
 * `signup` step until the username arrives, and that step answers three
 * refusals the family's table would otherwise word generically: a name
 * another account holds (`authn.username_taken`, a 409 the flow keeps
 * parked for another try), a name that does not normalize
 * (`authn.bad_request`) and a reserved name (`authn.username_reserved`),
 * both 400s from the same step. On any other step, and for every other
 * code, the namespace's own resolver answers.
 */
export function useFlowErrorText(
  step: OpenFlowState | null,
): (code: string) => string {
  const base = useAuthUiErrorText()
  const { t } = useAuthUiTranslation()
  const usernameStep = step !== null && step.state === 'signup'
  return useCallback(
    (code: string) => {
      if (usernameStep) {
        if (code === USERNAME_TAKEN) {
          return t('signInFlow.usernameTaken')
        }
        if (code === BAD_REQUEST || code === USERNAME_RESERVED) {
          return t('signInFlow.usernameUnusable')
        }
      }
      return base(code)
    },
    [base, t, usernameStep],
  )
}
