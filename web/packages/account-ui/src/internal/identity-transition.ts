/**
 * The identity-transition window around a credential-rotating write.
 *
 * A few self-service writes replace the calling session's credential as
 * they commit: the password change, the phone and email verifications, the
 * one-step username add, unlinking an external identity and removing an
 * active factor. A read issued under the outgoing credential can answer
 * anonymous before the new credential is in effect -- an already-in-flight
 * read, or one a focus, reconnect or staleTime 0 issues in the same round
 * as the write. The session's silent leg reads that anonymous answer as the
 * end of the session, and a shell that watches the session then evicts its
 * cache and shows the signed-out view.
 *
 * The session's identity-transition window is the guard: while it is open,
 * the silent leg discards an anonymous answer and keeps the snapshot. The
 * initiating page opens it synchronously before the write goes out and
 * closes it once the invalidation/refetch that follows the write has
 * settled -- the settle step is where every one of these surfaces re-reads
 * the state the write changed, so the window covers exactly the reads that
 * can still carry the outgoing credential.
 *
 * `write` is invoked synchronously inside the window. `settle` runs on its
 * successful answer and resolves with the surface's result (the
 * translated confirmation, or the answer itself when a caller branches on
 * it). A failed write releases the window at once, so the ordinary error
 * and step-up paths never leave it open; a step-up re-run is a fresh call
 * and opens its own window.
 */

import type { AuthSession } from '@speed/auth-core'

/**
 * Runs `write` inside an identity-transition window and, on success,
 * `settle` with its answer; the window closes after `settle` resolves (or
 * as soon as either rejects). Resolves with `settle`'s result.
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
