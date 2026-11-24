import { act, render, screen, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it } from 'vitest'
import { attachSession } from '@speed/auth-core'
import type { AuthDomain, AuthSnapshot } from '@speed/auth-core'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { getCurrentTenant, setCurrentTenant } from './chrome.js'
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

const PERMISSIONS_PATH = '/api/v1/authz/me/permissions'

interface AttachedSet {
  readonly domain: AuthDomain
  readonly perms: readonly string[] | null
}

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

function ReadProbe(): ReactElement {
  const read = usePermissionRead()
  const tenantSet = useTenantPermissionSet()
  return (
    <div>
      <span data-testid="read">{read.failed ? 'failed' : 'ok'}</span>
      <span data-testid="tenant">{read.tenantName ?? 'none'}</span>
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

function mountWithProbe(session: TestSession) {
  return render(
    <PlatformPermissions session={session}>
      <ReadProbe />
    </PlatformPermissions>,
  )
}

describe('PlatformPermissions', () => {
  afterEach(() => {
    setCurrentTenant(null)
  })

  it("attaches the caller's own platform and tenant permission sets", async () => {
    setCurrentTenant('acme')
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
    setCurrentTenant('acme')
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

    await waitFor(() => expect(screen.getByTestId('read')).toHaveTextContent('failed'))
    expect(attached).toEqual([
      { domain: 'system', perms: null },
      { domain: 'tenant', perms: null },
    ])
    expect(screen.getByTestId('perms')).toHaveTextContent('unknown')
    expect(screen.getByTestId('tenant-set')).toHaveTextContent('unknown')
    expect(screen.getByTestId('tenant')).toHaveTextContent('acme')
    expect(screen.getByTestId('pending')).toHaveTextContent('idle')

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
    setCurrentTenant('acme')
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
    setCurrentTenant('acme')
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
    expect(requested).not.toContain('/api/v1/system/permissions')
  })

  it('reads even when the feature document does not serve authz.admin', async () => {
    setCurrentTenant('acme')
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

      await act(async () => {
        await Promise.resolve()
      })
      expect(requested).toEqual([])
      expect(attached).toEqual([])
    },
  )

  it('re-reads when the effective identity changes', async () => {
    setCurrentTenant('acme')
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
    setCurrentTenant(null)
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path !== PERMISSIONS_PATH) {
        return Promise.reject(new Error(`unexpected request: ${path}`))
      }
      const tenantName = getCurrentTenant()
      return Promise.resolve({
        platform: ['audit.read'],
        tenant: tenantName === null ? [] : [`${tenantName}:read`],
      } as T)
    })

    const { session, attached } = recordingSession()
    attachSession(session)
    render(
      <PlatformPermissions session={session}>
        <div>content</div>
      </PlatformPermissions>,
    )

    await act(async () => {
      await Promise.resolve()
    })
    expect(requested).toEqual([])
    expect(attached).toEqual([])

    act(() => {
      setCurrentTenant('acme')
    })
    await waitFor(() =>
      expect(attached).toEqual([
        { domain: 'system', perms: null },
        { domain: 'tenant', perms: null },
        { domain: 'system', perms: ['audit.read'] },
        { domain: 'tenant', perms: ['acme:read'] },
      ]),
    )

    act(() => {
      setCurrentTenant('globex')
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
    setCurrentTenant('acme')
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
    expect(screen.getByTestId('tenant')).toHaveTextContent('acme')
    expect(screen.getByTestId('perms')).toHaveTextContent('acme:read')
    expect(screen.getByTestId('pending')).toHaveTextContent('idle')
  })

  it("never tags the new tenant with the previous tenant's set", async () => {
    setCurrentTenant('acme')
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

    act(() => setCurrentTenant('globex'))
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
    setCurrentTenant('acme')
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
    act(() => setCurrentTenant('globex'))
    await waitFor(() => expect(answers).toHaveLength(2))

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
