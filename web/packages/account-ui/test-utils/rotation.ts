/**
 * rotation.ts -- the identity-transition test rig.
 *
 * A credential-rotating write is only observable through the composition a
 * production browser host builds: a cookie session (same-origin
 * credentials, the session's own restore() as the 401 leg) whose snapshot
 * the silent leg can end. This module builds that session, establishes
 * the authenticated snapshot a write starts from, and counts the
 * authenticated -> anonymous edges a shell acts on, so a case can assert
 * that a read landing with the outgoing credential did not end the
 * session.
 *
 * The caller's responder owns the changeover: it answers the restore reads
 * (`SESSION_PATH`, `PROFILE_PATH`, `FEATURES_PATH`) authenticated until the
 * write under test lands, then answers the reads that follow it 401. That
 * scripted 401 stands for a read issued under the credential being
 * replaced -- one already in flight, or one a focus, reconnect or
 * staleTime 0 issues in the same round; a read issued after the write's
 * response lands carries the new credential.
 */

import type { AuthSession } from '@speed/auth-core'
import { makeCookieSessionRig } from './real-client.js'
import type { RealCall, RealResponder } from './real-client.js'
import { useProfileQuery } from '../src/internal/queries.js'

export const SESSION_PATH = '/api/v1/auth/session'
export const FEATURES_PATH = '/api/v1/features'

/** GET /api/v1/auth/session's authenticated body. */
export const AUTHENTICATED_SESSION: Record<string, unknown> = {
  authenticated: true,
  session_id: 'sess-1',
  account_id: 'user-1',
}

/** GET /api/v1/features' empty body (the restore leg reads it). */
export const EMPTY_FEATURES: Record<string, unknown> = { features: [] }

export interface RotationRig {
  readonly session: AuthSession
  readonly calls: RealCall[]
  /** The authenticated -> anonymous edges observed so far. */
  endedEdges(): number
  /** How many identity-transition windows have been opened so far. */
  transitionBegins(): number
}

/**
 * Builds the cookie-session rig, restores the authenticated snapshot and
 * starts counting sign-out edges. The responder must answer the restore's
 * reads authenticated, or this throws rather than letting a case pin a
 * session it never established.
 *
 * Every `beginIdentityTransition` call is counted, so a case can tell a
 * step-up re-run's own window from the first attempt's: the wrapper
 * delegates to the session's own operation and only observes the count.
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

/**
 * A profile-query observer standing in for the other account surfaces the
 * page carries: invalidating the profile query re-reads it, exactly as it
 * does when a neighbouring surface holds it. A section that does not read
 * the profile itself still causes the reads those surfaces issue after its
 * write.
 */
export function ProfileQueryObserver(): null {
  useProfileQuery()
  return null
}
