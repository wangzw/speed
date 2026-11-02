/**
 * Whether the deployment offers any social sign-in.
 *
 * A host that wants to hide the whole social-bindings card when the
 * assembly carries no social provider asks here instead of reading the
 * discovery document itself: the answer is `session.listMethods()`'s
 * `kind: 'redirect'` entries. A read that fails answers "no provider", the
 * same direction the bindings surface takes for a document it cannot read;
 * the surface's own rows are not part of this answer, so a host keeps
 * showing the card while the account holds bound identities.
 */

import { useEffect, useState } from 'react'
import type { AuthSession } from '@speed/auth-core'

/** The deployment's social-sign-in availability, and whether the read that
 * decides it has settled. */
export interface SocialMethodsAvailability {
  /** True until the discovery read settles. */
  readonly pending: boolean
  /** True when the deployment serves at least one redirect method. */
  readonly available: boolean
}

/** Reads the discovery document once per session and reports whether the
 * deployment serves a social (redirect) sign-in method. */
export function useSocialMethodsAvailable(
  session: AuthSession,
): SocialMethodsAvailability {
  const [state, setState] = useState<SocialMethodsAvailability>({
    pending: true,
    available: false,
  })
  useEffect(() => {
    let active = true
    session.listMethods().then(
      (document) => {
        if (!active) {
          return
        }
        setState({
          pending: false,
          available: document.methods.some(
            (method) => method.kind === 'redirect',
          ),
        })
      },
      () => {
        if (active) {
          setState({ pending: false, available: false })
        }
      },
    )
    return () => {
      active = false
    }
  }, [session])
  return state
}
