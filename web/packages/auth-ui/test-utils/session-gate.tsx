/** The host-role session gate the journey tests compose: it observes the
 * attached session's snapshot rather than threading `onSignedIn`. */

import { useEffect, useState } from 'react'
import type { ReactNode } from 'react'
import { useAuthState } from '@speed/auth-core'
import { SessionEndedScreen } from '../src/SessionEndedScreen.js'

export interface SessionGateProps {
  /** The host's authenticated view (its app content). */
  readonly app: ReactNode
  /** The host's sign-in surface, mounted while anonymous. */
  readonly signIn: ReactNode
}

export function SessionGate({ app, signIn }: SessionGateProps) {
  const snapshot = useAuthState()
  // Tells an anonymous snapshot apart: before the first authentication it is a
  // plain sign-in view, after one a session that ended.
  const [reachedApp, setReachedApp] = useState(false)
  useEffect(() => {
    if (snapshot.state === 'authenticated') {
      setReachedApp(true)
    }
  }, [snapshot.state])

  if (snapshot.state === 'authenticated') {
    return <>{app}</>
  }
  if (reachedApp) {
    return (
      <SessionEndedScreen onSignIn={() => setReachedApp(false)} />
    )
  }
  return <>{signIn}</>
}
