/**
 * useSignInDiscovery: the sign-in family's channel source.
 *
 * The ways in are the deployment's, not the host's: the screen reads the
 * discovery document (`session.listMethods`) once per session and renders
 * exactly what it names, so a method enabled or disabled server-side
 * reaches the surface without a frontend change. The read is a session
 * OPERATION on the session the screen already drives as a prop -- the
 * same seam every channel form uses -- rather than one of auth-core's
 * hooks (useAuthState/usePermission), so this package still reads no
 * host-attached session state of its own (see design-web-auth-ui.md).
 *
 * The result is one of three states: still loading, ready with the
 * document, or failed with the code to render. A failed read settles into
 * the error state instead of throwing out of the screen; bumping `attempt`
 * reads once more. `enabled: false` skips the read entirely, so a host
 * that injects the document itself (SSR, a test) fires no request.
 */

import { useEffect, useState } from 'react'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'

/** The discovery read's outcome. */
export type SignInDiscovery =
  | { readonly status: 'loading' }
  | { readonly status: 'ready'; readonly discovery: AuthnMethodList }
  | { readonly status: 'error'; readonly code: string }

export interface UseSignInDiscoveryOptions {
  /** Whether to read (default true); false leaves the state at loading so
   * an injected document is the only source. */
  readonly enabled?: boolean
  /** Bump to read once more (a retry); the effect re-runs on change. */
  readonly attempt?: number
}

export function useSignInDiscovery(
  session: AuthSession,
  options: UseSignInDiscoveryOptions = {},
): SignInDiscovery {
  const { enabled = true, attempt = 0 } = options
  const [state, setState] = useState<SignInDiscovery>({ status: 'loading' })
  useEffect(() => {
    if (!enabled) {
      return
    }
    // One read per (session, attempt): the guard survives StrictMode's
    // double effect invocation, so a settled answer is only written while
    // this effect is still the live one.
    let active = true
    setState({ status: 'loading' })
    void (async () => {
      try {
        const discovery = await session.listMethods()
        if (active) {
          setState({ status: 'ready', discovery })
        }
      } catch (error) {
        if (active) {
          setState({ status: 'error', code: errorCodeOf(error) })
        }
      }
    })()
    return () => {
      active = false
    }
  }, [session, enabled, attempt])
  return state
}
