/**
 * PlatformPermissions: the caller's own permission sets are attached to the
 * session once signed in.
 *
 * The component drives the auth state (auth-core's useAuthState) and the api
 * seam (the app's bound client); the session records what setPermissionSet
 * receives, so the suite asserts the attached value without a live server. The
 * read under test is GET /api/v1/authz/me/permissions, whose answer carries the
 * platform and tenant sets; an answered read attaches both, and a refused one
 * attaches the empty set to both (fail-closed).
 */

import { act, render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { attachSession } from '@speed/auth-core'
import type { AuthDomain, AuthSnapshot } from '@speed/auth-core'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { getCurrentTenantId, setCurrentTenantId } from './chrome.js'
import { PlatformPermissions } from './platform-permissions.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'
import type { TestSession } from './test-utils/session.js'

/** The route the bridge reads: the caller's own permissions. */
const PERMISSIONS_PATH = '/api/v1/authz/me/permissions'

/** One permission set the component attached, with the domain it targeted. */
interface AttachedSet {
  readonly domain: AuthDomain
  readonly perms: readonly string[] | null
}

/** A session that records every permission set attached to it, and the
 * snapshot it can be flipped to (the identity-change trigger). */
function recordingSession(snapshot: AuthSnapshot = authenticatedSnapshot()): {
  readonly session: TestSession
  readonly attached: AttachedSet[]
} {
  const base = createTestSession(snapshot)
  const attached: AttachedSet[] = []
  const session: TestSession = {
    ...base,
    setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
      attached.push({ domain, perms })
    },
  }
  return { session, attached }
}

/** Binds a request function that answers the permission read with `answer`
 * and records every path it was asked for. Any other path rejects, so a
 * bridge reading a different route fails the assertion rather than passing
 * with an answer it did not earn. */
function bindPermissionsAnswer(answer: {
  platform: string[]
  tenant: string[]
}): string[] {
  const requested: string[] = []
  const requestFn: RequestFn = <T,>(path: string): Promise<T> => {
    requested.push(path)
    if (path === PERMISSIONS_PATH) {
      return Promise.resolve(answer as T)
    }
    return Promise.reject(new Error(`unexpected request: ${path}`))
  }
  bindRequestFn(requestFn)
  return requested
}

describe('PlatformPermissions', () => {
  afterEach(() => {
    // The current tenant is a module-level cell; a case that moves it must not
    // leave the next one reading from a tenant it never settled.
    setCurrentTenantId(null)
  })

  it("attaches the caller's own platform and tenant permission sets", async () => {
    const requested = bindPermissionsAnswer({
      platform: ['audit.read', 'tenant.read'],
      tenant: ['notes:read'],
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    // The re-read resets both sets to unknown immediately before issuing its
    // read, so the known sets always follow the unknown ones.
    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read', 'tenant.read'] },
        { domain: 'tenant', perms: ['notes:read'] },
      ]),
    )
    expect(requested).toEqual([PERMISSIONS_PATH])
  })

  it('attaches the empty set to both domains when the read is refused', async () => {
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      return Promise.reject(new Error('refused'))
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: [] },
        { domain: 'tenant', perms: [] },
      ]),
    )
    // Fail-closed is about the answer, not about skipping the read: the bridge
    // still asks the caller-scoped route.
    expect(requested).toEqual([PERMISSIONS_PATH])
  })

  it('never reads the declared-permission catalog', async () => {
    const requested = bindPermissionsAnswer({
      platform: ['audit.read'],
      tenant: [],
    })

    const { session } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await waitFor(() => expect(requested).toEqual([PERMISSIONS_PATH]))
    // The old bridge treated GET /api/v1/system/permissions as the held set.
    // Its path must not appear: that route answers every declared permission,
    // not the ones this caller holds.
    expect(requested).not.toContain('/api/v1/system/permissions')
  })

  it('reads even when the feature document does not serve authz.admin', async () => {
    // The permission read is caller-scoped and serves no optional capability,
    // so its availability does not depend on the management surface's feature.
    const requested = bindPermissionsAnswer({
      platform: ['audit.read'],
      tenant: [],
    })

    const { session, attached } = recordingSession(
      authenticatedSnapshot({ features: readyFeatures('tenant.admin') }),
    )
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: [] },
      ]),
    )
    expect(requested).toEqual([PERMISSIONS_PATH])
  })

  it.each([
    ['pending', { state: 'pending' } as const],
    ['failed', { state: 'failed', error: new Error('offline') } as const],
  ])(
    'keeps both sets unknown while the feature document is %s',
    async (_label, features) => {
      const requested = bindPermissionsAnswer({ platform: [], tenant: [] })

      const { session, attached } = recordingSession(
        authenticatedSnapshot({ features }),
      )
      attachSession(session)
      render(
        <PlatformPermissions session={session}>
          <div>content</div>
        </PlatformPermissions>,
      )

      // Flush the microtask the read would be deferred into; a bridge that
      // ignored the feature state would have requested and attached by now.
      await act(async () => {
        await Promise.resolve()
      })
      expect(requested).toEqual([])
      expect(attached).toEqual([])
    },
  )

  it('re-reads when the effective identity changes', async () => {
    let answer = {
      platform: ['audit.read'],
      tenant: ['notes:read'],
    }
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === PERMISSIONS_PATH) {
        return Promise.resolve(answer as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['notes:read'] },
      ]),
    )

    // A started impersonation swaps the effective account without the feature
    // state moving; the read is keyed on the identity, not on the features.
    answer = { platform: [], tenant: [] }
    act(() => {
      session.set({ ...authenticatedSnapshot(), impersonating: true })
    })

    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['notes:read'] },
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: [] },
        { domain: 'tenant', perms: [] },
      ]),
    )
    expect(requested).toEqual([PERMISSIONS_PATH, PERMISSIONS_PATH])
  })

  it('re-reads when the current tenant settles and on every switch', async () => {
    setCurrentTenantId(null)
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      // The tenant list answers for the tenant the request resolved to, and
      // the host announces the current tenant from the cell, so the answer
      // varies with it. A read issued before the tenant settles carries none.
      const tenantId = getCurrentTenantId()
      return Promise.resolve({
        platform: ['audit.read'],
        tenant: tenantId === null ? [] : [`${tenantId}:read`],
      } as T)
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    // The first read goes out before the host has settled a tenant: the tenant
    // set is a known empty answer, never an error.
    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: [] },
      ]),
    )

    // The roster settles the tenant. The read is repeated and attaches the set
    // held there, rather than leaving the pre-settlement answer in place.
    act(() => {
      setCurrentTenantId('acme')
    })
    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: [] },
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['acme:read'] },
      ]),
    )

    // A switch to another tenant reads again and attaches that tenant's set.
    act(() => {
      setCurrentTenantId('globex')
    })
    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: [] },
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['acme:read'] },
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['globex:read'] },
      ]),
    )
    expect(requested).toEqual([
      PERMISSIONS_PATH,
      PERMISSIONS_PATH,
      PERMISSIONS_PATH,
    ])
  })
})
