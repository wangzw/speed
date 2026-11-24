/**
 * runIdentityTransition checks that the window is released exactly once on every exit.
 */

import { describe, expect, it } from 'vitest'
import type { AuthSession } from '@speed/auth-core'
import { runIdentityTransition } from './identity-transition.js'

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
    release?.()
    release?.()
    expect(fake.begins()).toBe(1)
    expect(fake.open()).toBe(0)
  })
})
