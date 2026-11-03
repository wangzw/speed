/**
 * PlatformPermissions: the caller's own permission sets are attached to the
 * session once signed in.
 *
 * The component drives the auth state (auth-core's useAuthState) and the api
 * seam (the app's bound client); the session records what setPermissionSet
 * receives, so the suite asserts the attached value without a live server. The
 * read under test is GET /api/v1/authz/me/permissions, whose answer carries the
 * platform and tenant sets; an answered read attaches both (an empty pair is a
 * known empty set), and a failed read attaches nothing: the sets stay unknown
 * and the read's state reports the failure so a surface can retry it.
 */

import { act, render, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { attachSession } from '@speed/auth-core'
import type { AuthDomain, AuthSnapshot } from '@speed/auth-core'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { getCurrentTenantId, setCurrentTenantId } from './chrome.js'
import {
  PlatformPermissions,
  usePermissionRead,
  useTenantPermissionSet,
} from './platform-permissions.js'
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

/** A consumer of the read's outcome, so a case can observe the failure the
 * surfaces see, the tenant the settled sets are tagged with, and drive the
 * retry. */
function ReadProbe(): ReactElement {
  const read = usePermissionRead()
  const tenantSet = useTenantPermissionSet()
  return (
    <div>
      <span data-testid="read">{read.failed ? 'failed' : 'ok'}</span>
      <span data-testid="tenant">{read.tenantId ?? 'none'}</span>
      <span data-testid="perms">
        {read.perms === null ? 'unknown' : JSON.stringify(read.perms)}
      </span>
      <span data-testid="pending">{read.pending ? 'pending' : 'idle'}</span>
      <span data-testid="tenant-set">
        {tenantSet === null ? 'unknown' : tenantSet.join(',')}
      </span>
      <button type="button" onClick={read.retry}>
        retry
      </button>
    </div>
  )
}

/** A promise a case settles when it chooses, so it can move the tenant while
 * a read is in flight. */
function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** Mounts the attach with the probe beside its content. */
function mountWithProbe(session: TestSession) {
  return render(
    <PlatformPermissions session={session}>
      <ReadProbe />
    </PlatformPermissions>,
  )
}

describe('PlatformPermissions', () => {
  afterEach(() => {
    // The current tenant is a module-level cell; a case that moves it must not
    // leave the next one reading from a tenant it never settled.
    setCurrentTenantId(null)
  })

  it("attaches the caller's own platform and tenant permission sets", async () => {
    // The guarded route needs a tenant name: the host settles the current
    // tenant before the read, as the tenant frame does on a page load.
    setCurrentTenantId('acme')
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

  it('keeps both sets unknown when the read fails, and a retry answers for them', async () => {
    setCurrentTenantId('acme')
    let answer: { platform: string[]; tenant: string[] } | null = null
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      return answer === null
        ? Promise.reject(new Error('the service is unavailable'))
        : Promise.resolve(answer as T)
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    mountWithProbe(session)

    // The read reset both sets to unknown and then failed: no empty set is
    // attached, and the failure is what the surfaces see. The tagged state
    // keeps the sets unknown too -- a failure is not an empty answer -- and
    // names the tenant the failed attempt was for.
    await waitFor(() => expect(screen.getByTestId('read')).toHaveTextContent('failed'))
    expect(attached).toEqual([
      { domain: 'system', perms: null },
      { domain: 'tenant', perms: null },
    ])
    expect(screen.getByTestId('perms')).toHaveTextContent('unknown')
    expect(screen.getByTestId('tenant-set')).toHaveTextContent('unknown')
    expect(screen.getByTestId('tenant')).toHaveTextContent('acme')
    expect(screen.getByTestId('pending')).toHaveTextContent('idle')

    // The retry issues the read again; its answer attaches the sets and
    // clears the failure.
    answer = { platform: ['audit.read'], tenant: [] }
    act(() => {
      screen.getByRole('button', { name: 'retry' }).click()
    })
    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: [] },
      ]),
    )
    expect(screen.getByTestId('read')).toHaveTextContent('ok')
    expect(requested).toEqual([PERMISSIONS_PATH, PERMISSIONS_PATH])
  })

  it('attaches an answered empty document as a known empty set', async () => {
    // Both domains empty is a read answer, not a failure: a member's known
    // empty set is what the admin gate turns into the home redirect.
    setCurrentTenantId('acme')
    const requested = bindPermissionsAnswer({ platform: [], tenant: [] })

    const { session, attached } = recordingSession()
    attachSession(session)
    mountWithProbe(session)

    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: [] },
        { domain: 'tenant', perms: [] },
      ]),
    )
    expect(screen.getByTestId('read')).toHaveTextContent('ok')
    expect(requested).toEqual([PERMISSIONS_PATH])
  })

  it('never reads the declared-permission catalog', async () => {
    setCurrentTenantId('acme')
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
    setCurrentTenantId('acme')
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
    setCurrentTenantId('acme')
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

  it('does not read until the current tenant settles, then follows every switch', async () => {
    setCurrentTenantId(null)
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      // The tenant list answers for the tenant the request resolved to, and
      // the host announces the current tenant from the cell, so the answer
      // varies with it.
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

    // The guarded route answers 404 to a request that names no tenant, so an
    // undecided current tenant is not read at all: the sets stay unknown
    // rather than being attached as the fail-closed empty answer.
    await act(async () => {
      await Promise.resolve()
    })
    expect(requested).toEqual([])
    expect(attached).toEqual([])

    // The host settles the tenant: the first read carries its name and
    // attaches the set held there.
    act(() => {
      setCurrentTenantId('acme')
    })
    await waitFor(() =>
      expect(attached).toEqual([
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
        { domain: 'tenant', perms: ['acme:read'] },
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['globex:read'] },
      ]),
    )
    expect(requested).toEqual([PERMISSIONS_PATH, PERMISSIONS_PATH])
  })

  it('tags the settled sets with the tenant they were read for', async () => {
    setCurrentTenantId('acme')
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      return Promise.resolve({
        platform: ['audit.read'],
        tenant: ['acme:read'],
      } as T)
    })

    const { session } = recordingSession()
    attachSession(session)
    mountWithProbe(session)

    await waitFor(() =>
      expect(screen.getByTestId('tenant-set')).toHaveTextContent('acme:read'),
    )
    // The state names the tenant its sets belong to, so a consumer can tell a
    // current answer from one read for another tenant.
    expect(screen.getByTestId('tenant')).toHaveTextContent('acme')
    expect(screen.getByTestId('perms')).toHaveTextContent('acme:read')
    expect(screen.getByTestId('pending')).toHaveTextContent('idle')
  })

  it("never tags the new tenant with the previous tenant's set", async () => {
    setCurrentTenantId('acme')
    const answers: Array<
      ReturnType<typeof deferred<{ platform: string[]; tenant: string[] }>>
    > = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      const answer = deferred<{ platform: string[]; tenant: string[] }>()
      answers.push(answer)
      return answer.promise as Promise<T>
    })

    const { session } = recordingSession()
    attachSession(session)
    mountWithProbe(session)

    await waitFor(() => expect(answers).toHaveLength(1))
    act(() =>
      answers[0]?.resolve({ platform: ['audit.read'], tenant: ['acme:read'] }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('tenant-set')).toHaveTextContent('acme:read'),
    )

    // Switch while the new read is in flight: the tag names the new tenant and
    // the sets are unknown until that tenant's answer lands. No frame carries
    // globex with acme's set.
    act(() => setCurrentTenantId('globex'))
    expect(screen.getByTestId('tenant')).toHaveTextContent('globex')
    expect(screen.getByTestId('tenant-set')).toHaveTextContent('unknown')
    expect(screen.getByTestId('pending')).toHaveTextContent('pending')

    await waitFor(() => expect(answers).toHaveLength(2))
    act(() =>
      answers[1]?.resolve({ platform: ['audit.read'], tenant: ['globex:read'] }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('tenant-set')).toHaveTextContent('globex:read'),
    )
  })

  it('discards a slow answer for the tenant the viewer has left', async () => {
    setCurrentTenantId('acme')
    const answers: Array<
      ReturnType<typeof deferred<{ platform: string[]; tenant: string[] }>>
    > = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      const answer = deferred<{ platform: string[]; tenant: string[] }>()
      answers.push(answer)
      return answer.promise as Promise<T>
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    mountWithProbe(session)

    await waitFor(() => expect(answers).toHaveLength(1))
    // Move the tenant with the first read still in flight.
    act(() => setCurrentTenantId('globex'))
    await waitFor(() => expect(answers).toHaveLength(2))

    // The answer for acme lands late: neither the session nor the tagged
    // state may take it.
    act(() =>
      answers[0]?.resolve({ platform: ['audit.read'], tenant: ['acme:read'] }),
    )
    await act(async () => {
      await Promise.resolve()
    })
    expect(attached).not.toContainEqual({
      domain: 'tenant',
      perms: ['acme:read'],
    })
    expect(screen.getByTestId('tenant-set')).toHaveTextContent('unknown')

    act(() =>
      answers[1]?.resolve({ platform: ['audit.read'], tenant: ['globex:read'] }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('tenant-set')).toHaveTextContent('globex:read'),
    )
  })
})
