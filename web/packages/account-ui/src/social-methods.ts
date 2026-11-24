/**
 * Whether the deployment offers any social sign-in.
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

/**
 * Reads the discovery document once per session and reports whether the
 * deployment serves a social (redirect) sign-in method. A failed read
 * reports no provider.
 */
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
