/**
 * The identity-transition window around a credential-rotating write.
 */

import type { AuthSession } from '@speed/auth-core'

/**
 * Runs `write` inside an identity-transition window and, on success, `settle`
 * with its answer. The window closes after `settle` resolves or either
 * rejects; the result is `settle`'s.
 */
export async function runIdentityTransition<TWrite, TResult>(
  session: Pick<AuthSession, 'beginIdentityTransition'>,
  write: () => Promise<TWrite>,
  settle: (written: TWrite) => Promise<TResult>,
): Promise<TResult> {
  const release = session.beginIdentityTransition()
  try {
    return await settle(await write())
  } finally {
    release()
  }
}
