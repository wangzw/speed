/**
 * PlatformPermissions: the caller's platform permission set is attached to the
 * session once signed in.
 *
 * The component drives the auth state (auth-core's useAuthState) and the api
 * seam (the app's generated catalog read); the session records what
 * setPermissionSet receives, so the suite asserts the attached value without a
 * live server. An answered catalog read attaches its codes; a refused one
 * attaches the empty set (fail-closed).
 */

import { render, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { attachSession } from '@speed/auth-core'
import type { AuthDomain, AuthSession } from '@speed/auth-core'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { PlatformPermissions } from './platform-permissions.js'
import { authenticatedSnapshot, createTestSession } from './test-utils/session.js'

/** A session that records every permission set attached to it. */
function recordingSession(): {
  readonly session: AuthSession
  readonly attached: ReadonlyArray<readonly string[] | null>
} {
  const base = createTestSession(authenticatedSnapshot())
  const attached: Array<readonly string[] | null> = []
  const session: AuthSession = {
    ...base,
    setPermissionSet: (_domain: AuthDomain, perms: readonly string[] | null) => {
      attached.push(perms)
    },
  }
  return { session, attached }
}

describe('PlatformPermissions', () => {
  it("attaches the caller's platform permissions from the catalog read", async () => {
    const requestFn: RequestFn = <T,>(
      path: string,
    ): Promise<T> => {
      if (path === '/authz/admin/permissions') {
        return Promise.resolve({
          permissions: [{ permission: 'tenant.read' }, { permission: 'audit.read' }],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    }
    bindRequestFn(requestFn)

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await waitFor(() =>
      expect(attached).toEqual([['tenant.read', 'audit.read']]),
    )
  })

  it('attaches the empty set when the catalog read is refused', async () => {
    const requestFn: RequestFn = <T,>(): Promise<T> =>
      Promise.reject(new Error('refused'))
    bindRequestFn(requestFn)

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await waitFor(() => expect(attached).toEqual([[]]))
  })
})
