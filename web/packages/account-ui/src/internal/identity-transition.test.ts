/**
 * runIdentityTransition -- the window's lifecycle on every exit.
 *
 * The wrapper's only effect is the pair of calls it makes on the session:
 * begin before the write, release exactly once when the settle step has
 * settled -- or at once when the write or the settle step rejects. A suite
 * that exercised only the success path could not tell a release that also
 * runs on failure from one that never runs at all: a window left open by a
 * refused write would discard every genuine anonymous answer that follows,
 * and the user would never be signed out. These cases pin the release on
 * every exit and the result each path hands back.
 */

import { describe, expect, it } from 'vitest'
import type { AuthSession } from '@speed/auth-core'
import { runIdentityTransition } from './identity-transition.js'

/**
 * A session stand-in that records the window: how many times it was
 * opened, how many times its release was invoked, and how many windows
 * are still open. The release follows the session's contract -- calling
 * it again is a no-op -- so a second call neither reopens nor
 * over-closes.
 */
function fakeSession() {
  let begins = 0
  let releaseCalls = 0
  let open = 0
  let lastRelease: (() => void) | null = null
  const session: Pick<AuthSession, 'beginIdentityTransition'> = {
    beginIdentityTransition(): () => void {
      begins += 1
      open += 1
      let released = false
      const release = (): void => {
        releaseCalls += 1
        if (released) {
          return
        }
        released = true
        open -= 1
      }
      lastRelease = release
      return release
    },
  }
  return {
    session,
    begins: () => begins,
    releaseCalls: () => releaseCalls,
    open: () => open,
    lastRelease: () => lastRelease,
  }
}

describe('runIdentityTransition', () => {
  it('closes the window after settle resolves and returns its result', async () => {
    const fake = fakeSession()
    const result = await runIdentityTransition(
      fake.session,
      async () => 'written',
      async (written) => {
        // The settle step receives the write's own answer.
        expect(written).toBe('written')
        return 'settled'
      },
    )
    expect(result).toBe('settled')
    expect(fake.begins()).toBe(1)
    expect(fake.releaseCalls()).toBe(1)
    expect(fake.open()).toBe(0)
  })

  it('closes the window when the write rejects and rethrows that error', async () => {
    const fake = fakeSession()
    const failure = new Error('write refused')
    await expect(
      runIdentityTransition(
        fake.session,
        async () => {
          throw failure
        },
        async () => 'settled',
      ),
    ).rejects.toBe(failure)
    expect(fake.begins()).toBe(1)
    expect(fake.releaseCalls()).toBe(1)
    expect(fake.open()).toBe(0)
  })

  it('closes the window when settle rejects and rethrows that error', async () => {
    const fake = fakeSession()
    const failure = new Error('settle refused')
    await expect(
      runIdentityTransition(
        fake.session,
        async () => 'written',
        async () => {
          throw failure
        },
      ),
    ).rejects.toBe(failure)
    expect(fake.begins()).toBe(1)
    expect(fake.releaseCalls()).toBe(1)
    expect(fake.open()).toBe(0)
  })

  it('leaves the release safe to call again', async () => {
    const fake = fakeSession()
    await runIdentityTransition(
      fake.session,
      async () => 'written',
      async () => 'settled',
    )
    const release = fake.lastRelease()
    expect(release).not.toBeNull()
    // The wrapper already released the window; a second caller holding the
    // same release must neither over-close it nor reopen anything.
    release?.()
    release?.()
    expect(fake.begins()).toBe(1)
    expect(fake.open()).toBe(0)
  })
})
