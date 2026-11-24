/**
 * The identity-transition test rig: a cookie session and the authenticated-to-anonymous edge count.
 */

import type { AuthSession } from '@speed/auth-core'
import { makeCookieSessionRig } from './real-client.js'
import type { RealCall, RealResponder } from './real-client.js'
import { useProfileQuery } from '../src/internal/queries.js'

export const SESSION_PATH = '/api/v1/auth/session'
export const FEATURES_PATH = '/api/v1/features'

export const AUTHENTICATED_SESSION: Record<string, unknown> = {
  authenticated: true,
  session_id: 'sess-1',
  account_id: 'user-1',
}

export const EMPTY_FEATURES: Record<string, unknown> = { features: [] }

export interface RotationRig {
  readonly session: AuthSession
  readonly calls: RealCall[]
  endedEdges(): number
  transitionBegins(): number
}

/**
 * The responder must answer the restore's reads authenticated, or this throws;
 * every `beginIdentityTransition` is counted.
 */
export async function makeRotationRig(
  respond: RealResponder,
): Promise<RotationRig> {
  const { session, calls } = makeCookieSessionRig(respond)
  await session.restore()
  if (session.getSnapshot().state !== 'authenticated') {
    throw new Error(
      'the rotation rig did not reach an authenticated snapshot',
    )
  }
  let state = session.getSnapshot().state
  let edges = 0
  session.subscribe((snapshot) => {
    if (state === 'authenticated' && snapshot.state === 'anonymous') {
      edges += 1
    }
    state = snapshot.state
  })
  const begin = session.beginIdentityTransition.bind(session)
  let begins = 0
  session.beginIdentityTransition = () => {
    begins += 1
    return begin()
  }
  return {
    session,
    calls,
    endedEdges: () => edges,
    transitionBegins: () => begins,
  }
}

export function ProfileQueryObserver(): null {
  useProfileQuery()
  return null
}
