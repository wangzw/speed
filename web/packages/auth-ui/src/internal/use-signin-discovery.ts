/** The sign-in family's discovery-document read. */

import { useEffect, useState } from 'react'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
import { errorCodeOf } from '@speed/ui-kit'

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
