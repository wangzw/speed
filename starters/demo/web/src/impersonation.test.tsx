/**
 * The impersonation shell in the demo host: the identity-change action, the
 * banner in the tenant frame, the console's redirect while impersonating,
 * the status read's bare-404 rule, the expiry timer, and the read cutover a
 * start write opens (its in-flight reads cancelled, new ones held until the
 * identity change settles, and the session's identity-transition guard
 * opened once and released exactly once).
 *
 * The suites drive the real views over a scripted session and request
 * function: the session's restore() commits whatever identity the script
 * says the server now names, and the request function answers the generated
 * endpoints the views reach. The session is the test-utils one (a snapshot
 * the suite can set), extended with a restore() that flips it -- the
 * identity change the host's action performs.
 */

import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { onlineManager } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthDomain, AuthSnapshot } from '@speed/auth-core'
import type { RequestFn, RequestOptions, SpeedClient } from '@speed/api-client'
import { FEATURE_NAMES } from '@speed/api-sdk/features'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { AUTH_UI_NAMESPACE } from '@speed/auth-ui'
import {
  CONSOLE_UI_NAMESPACE,
  consoleUiResources,
} from '@speed/console-ui'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import {
  createSpeedI18n,
} from '@speed/product-shell/bootstrap'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { PlatformPermissions } from './platform-permissions.js'
import {
  DEFAULT_TENANT_ID,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

/**
 * The expected copy, resolved from the packages' shipped resources through a
 * production instance rather than read from a file path: the release the app
 * ships is what the suite asserts, and the text is never a literal here.
 */
const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: CONSOLE_UI_NAMESPACE, resources: consoleUiResources },
    { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
  ],
})
const tAuth = textI18n.getFixedT('en-US', AUTH_UI_NAMESPACE)
const tConsole = textI18n.getFixedT('en-US', CONSOLE_UI_NAMESPACE)
const tLayout = textI18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)
const tTenancy = textI18n.getFixedT('en-US', TENANCY_UI_NAMESPACE)

const TENANT = { id: 'acme', alias: 'acme', status: 'active' as const }

interface GrantFixture {
  readonly id: string
  readonly target: string
  readonly tenant: string
  readonly starts_at: string
  readonly ends_at: string
}

function grantFixture(endsAt: string): GrantFixture {
  return {
    id: 'imp-1',
    target: 'acc-1',
    tenant: 'acme',
    starts_at: new Date(Date.now() - 60_000).toISOString(),
    ends_at: endsAt,
  }
}

/** The operator's own identity: platform staff, not impersonating. */
function operatorSnapshot(): AuthSnapshot {
  const base = authenticatedSnapshot({
    system: ['tenant.read', 'authn.impersonation.read'],
  })
  return {
    ...base,
    principal: {
      ...base.principal!,
      account_id: 'staff-1',
      display_name: 'Staff One',
    },
    impersonating: false,
  }
}

/**
 * The impersonated identity. The platform permission set is deliberately
 * left non-empty: the console redirect and the hidden entry must not depend
 * on it.
 */
function targetSnapshot(): AuthSnapshot {
  const base = authenticatedSnapshot({
    system: ['tenant.read', 'authn.impersonation.read'],
  })
  return {
    ...base,
    principal: {
      ...base.principal!,
      account_id: 'acc-1',
      display_name: 'Target One',
    },
    impersonating: true,
  }
}

/** A session whose restore() commits the identity the script names. */
function scriptedSession(
  initial: AuthSnapshot,
  next: () => AuthSnapshot | null,
): ReturnType<typeof createTestSession> {
  const base = createTestSession(initial)
  return {
    ...base,
    restore: () => {
      const snapshot = next()
      if (snapshot !== null) {
        base.set(snapshot)
      }
      return Promise.resolve(snapshot !== null)
    },
  }
}

/**
 * A session that counts the identity transitions the host opens and
 * releases. The start hold opens one before the write goes out; every path
 * that does not land must close it exactly once, and a landed write closes
 * it through the identity change's own release.
 */
function transitionTrackingSession(initial: AuthSnapshot): {
  readonly session: ReturnType<typeof createTestSession>
  readonly opens: () => number
  readonly releases: () => number
} {
  const base = createTestSession(initial)
  let opens = 0
  let releases = 0
  return {
    session: {
      ...base,
      beginIdentityTransition: () => {
        opens += 1
        let released = false
        return () => {
          if (released) {
            return
          }
          released = true
          releases += 1
        }
      },
    },
    opens: () => opens,
    releases: () => releases,
  }
}

/**
 * A session whose restore() answers a task later, like the server round
 * trip a real restore is, and whose permission attachment notifies like the
 * real session does. Both matter to the end sequence: the identity change
 * first attaches the reset permission set (a commit -- the real session
 * notifies on it) and evicts the status read, so the grant disappears while
 * the snapshot still says impersonating and the frame sets the current
 * tenant unknown; only then does the restore answer and commit the new
 * identity. Collapsing either step hides that intermediate state.
 */
function roundTripSession(
  initial: AuthSnapshot,
  next: () => AuthSnapshot | null,
): ReturnType<typeof createTestSession> {
  const base = createTestSession(initial)
  return {
    ...base,
    setPermissionSet: (domain, perms) => {
      const current = base.getSnapshot()
      base.set({
        ...current,
        permissionSets: {
          tenant: domain === 'tenant' ? perms : current.permissionSets.tenant,
          system: domain === 'system' ? perms : current.permissionSets.system,
        },
      })
    },
    restore: () =>
      new Promise<boolean>((resolve) => {
        setTimeout(() => {
          const snapshot = next()
          if (snapshot !== null) {
            base.set(snapshot)
          }
          resolve(snapshot !== null)
        }, 0)
      }),
  }
}

/**
 * A session whose restore() answers only when the suite releases it: the
 * window between a settled end and the re-read that flips the snapshot,
 * which the server widens whenever a replica's validity cache lags.
 */
function gatedSession(
  initial: AuthSnapshot,
  next: () => AuthSnapshot | null,
): {
  readonly session: ReturnType<typeof createTestSession>
  readonly pendingRestores: () => number
  readonly releaseRestore: () => Promise<void>
} {
  const base = createTestSession(initial)
  const gates: (() => void)[] = []
  return {
    session: {
      ...base,
      restore: () =>
        new Promise<boolean>((resolve) => {
          gates.push(() => {
            const snapshot = next()
            if (snapshot !== null) {
              base.set(snapshot)
            }
            resolve(snapshot !== null)
          })
        }),
    },
    pendingRestores: () => gates.length,
    releaseRestore: async () => {
      const gate = gates.shift()
      if (gate === undefined) {
        throw new Error('no pending restore to release')
      }
      await act(async () => {
        gate()
      })
    },
  }
}

interface Backend {
  readonly fn: RequestFn
  /** Every GET issued through the request function (a plain effect read is
   * counted like a query's). */
  readonly reads: () => number
  readonly rosterReads: () => number
  readonly statusReads: () => number
  readonly statusReadsWithGrant: () => number
  readonly recordReads: () => number
  /** Whether the record read's abort signal fired: the read the start
   * write's hold cancelled. */
  readonly recordsAborted: () => boolean
  readonly postReads: () => number
  readonly unreadReads: () => number
  /** The current tenant's id at the moment each unread-count read left the
   * browser: the alias the request surface would have announced. */
  readonly unreadTenantIds: () => readonly (string | null)[]
  readonly objectsReads: () => number
  readonly deletes: () => number
  /** A read of a tenant-scoped data surface (notifications, objects): what
   * must not run under a tenant the frame has not settled on. */
  readonly tenantReads: () => number
}

/** A promise-bounded gate: the suite decides when the gated read answers. */
function deferred(): { readonly promise: Promise<void>; readonly resolve: () => void } {
  let resolve!: () => void
  const promise = new Promise<void>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/** The generated endpoints the views reach, scripted per test. */
function makeBackend(options: {
  readonly grant?: GrantFixture
  readonly endStatus?: 'ok' | '404' | 'error'
  /** Holds the roster read open until the suite resolves it. */
  readonly rosterGate?: Promise<void>
  /** Keeps the status read answering the grant after the DELETE settles: a
   * replica whose check-ttl cache has not expired yet. */
  readonly statusLags?: boolean
  /** Answers the status read with the bare 404 once the recorded expiry has
   * passed: the server's own Active judgement, which the session read may
   * still lag behind for one check-ttl. */
  readonly status404AfterExpiry?: boolean
  /** Holds the status read open, once the start write has been issued, until
   * the suite resolves it: the window in which the remounted tenant frame
   * has no grant yet. Reads before the start answer normally (the console
   * page's own status surface needs its settled "not impersonating"). */
  readonly statusGateAfterStart?: Promise<void>
  /** The tenant id the start write grants; defaults to the fixture's. */
  readonly startTenant?: string
  /** Refuses the status read once it has been issued more than this many
   * times. A self-driving 404 loop is a microtask storm that would otherwise
   * hang the test until its timeout; this turns it into an answer the test
   * can assert against (the storm is the failure, not the elapsed time). */
  readonly statusReadLimit?: number
  /** Holds the start write open until the suite resolves it: the slow round
   * trip the rotation window is about. */
  readonly postGate?: Promise<void>
  /** Holds the record read open until the suite resolves it, so a read is
   * in flight when the start write is issued. */
  readonly recordsGate?: Promise<void>
  /** Refuses the start write: no identity change follows it. */
  readonly startRefused?: boolean
} = {}): Backend {
  let grant = options.grant
  let reads = 0
  let rosterReads = 0
  let statusReads = 0
  let statusReadsWithGrant = 0
  let recordReads = 0
  let recordSignal: AbortSignal | undefined
  let postReads = 0
  let unreadReads = 0
  const unreadTenantIds: (string | null)[] = []
  let objectsReads = 0
  let deletes = 0
  let tenantReads = 0
  const fn: RequestFn = <T,>(
    path: string,
    request?: RequestOptions,
  ): Promise<T> => {
    const method = request?.method ?? 'GET'
    if (method === 'GET') {
      reads += 1
    }
    if (
      path.startsWith('/api/v1/notifications/') ||
      path.startsWith('/api/v1/objects')
    ) {
      tenantReads += 1
    }
    if (path === '/api/v1/system/tenants') {
      rosterReads += 1
      if (options.rosterGate !== undefined) {
        return options.rosterGate.then(() => ({ tenants: [TENANT] }) as T)
      }
      return Promise.resolve({ tenants: [TENANT] } as T)
    }
    if (path === '/api/v1/auth/impersonation') {
      if (method === 'GET') {
        statusReads += 1
        if (
          options.statusReadLimit !== undefined &&
          statusReads > options.statusReadLimit
        ) {
          return Promise.reject(new Error('the status read is looping'))
        }
        const answer = (): Promise<T> => {
          if (grant === undefined) {
            return Promise.reject({ status: 404, code: 'client.http.404' })
          }
          if (
            options.status404AfterExpiry === true &&
            Date.now() >= Date.parse(grant.ends_at)
          ) {
            return Promise.reject({ status: 404, code: 'client.http.404' })
          }
          statusReadsWithGrant += 1
          return Promise.resolve({ grant } as T)
        }
        if (
          options.statusGateAfterStart !== undefined &&
          postReads > 0
        ) {
          return options.statusGateAfterStart.then(answer)
        }
        return answer()
      }
      if (method === 'POST') {
        postReads += 1
        if (options.startRefused === true) {
          return Promise.reject({
            status: 403,
            code: 'rbac.permission_denied',
          })
        }
        grant = {
          ...grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
          ...(options.startTenant === undefined
            ? {}
            : { tenant: options.startTenant }),
        }
        const answer = { grant } as T
        if (options.postGate !== undefined) {
          return options.postGate.then(() => answer)
        }
        return Promise.resolve(answer)
      }
      if (method === 'DELETE') {
        deletes += 1
        if (options.endStatus === 'error') {
          // The write was refused: the impersonation stays in force.
          return Promise.reject({
            status: 503,
            code: 'authn.unavailable',
          })
        }
        if (options.statusLags !== true) {
          grant = undefined
        }
        if (options.endStatus === '404') {
          return Promise.reject({ status: 404, code: 'client.http.404' })
        }
        return Promise.resolve(undefined as T)
      }
    }
    if (path.startsWith('/api/v1/tenants/')) {
      recordReads += 1
      recordSignal = request?.signal
      if (options.recordsGate !== undefined) {
        return options.recordsGate.then(() => ({ impersonations: [] }) as T)
      }
      return Promise.resolve({ impersonations: [] } as T)
    }
    // The tenant header's bell reads the unread count; the inbox page the
    // message list. Answer both so a mounted tenant frame has no query
    // whose function returned undefined.
    if (path === '/api/v1/notifications/messages/unread-count') {
      unreadReads += 1
      unreadTenantIds.push(getCurrentTenantId())
      return Promise.resolve({ unread: 0 } as T)
    }
    if (path === '/api/v1/notifications/messages') {
      return Promise.resolve({ messages: [], total: 0 } as T)
    }
    // The object store's page lists the tenant's objects.
    if (path === '/api/v1/objects') {
      objectsReads += 1
      return Promise.resolve({ objects: [], next_cursor: '' } as T)
    }
    // Nothing in a mounted tenant frame may fall through to an undefined
    // query answer: react-query logs "Query data cannot be undefined" and the
    // view renders a result it never received. An unanswered path is a hole
    // in the scripted backend, not an empty answer.
    return Promise.reject(
      new Error(`the suite does not answer ${method} ${path}`),
    )
  }
  return {
    fn,
    reads: () => reads,
    rosterReads: () => rosterReads,
    statusReads: () => statusReads,
    statusReadsWithGrant: () => statusReadsWithGrant,
    recordReads: () => recordReads,
    recordsAborted: () => recordSignal?.aborted ?? false,
    postReads: () => postReads,
    unreadReads: () => unreadReads,
    unreadTenantIds: () => unreadTenantIds,
    objectsReads: () => objectsReads,
    deletes: () => deletes,
    tenantReads: () => tenantReads,
  }
}

/** A client whose event-stream opener records every connection and the
 * current tenant at the moment it opens. */
function streamClient(): {
  readonly client: SpeedClient
  readonly opened: { closed: number }[]
  readonly tenants: (string | null)[]
} {
  const opened: { closed: number }[] = []
  const tenants: (string | null)[] = []
  const request = (() =>
    Promise.reject(new Error('the suite answers through bindRequestFn'))) as RequestFn
  const client = Object.assign(request, {
    openEventStream: () => {
      const record = { closed: 0 }
      opened.push(record)
      tenants.push(getCurrentTenantId())
      return {
        close: () => {
          record.closed += 1
        },
      }
    },
  }) as SpeedClient
  return { client, opened, tenants }
}

/** The banner's named region, or null while none is in force. */
function banner(): HTMLElement | null {
  return screen.queryByRole('region', {
    name: tAuth('impersonation.regionLabel'),
  })
}

/**
 * The tenant switcher's trigger, or null while it is not mounted. The trigger
 * is named by the current tenant's row, or by the no-current-tenant label
 * while the host has settled none.
 */
function tenantSwitcherTrigger(): HTMLElement | null {
  return (
    screen.queryByRole('button', { name: 'acme' }) ??
    screen.queryByRole('button', {
      name: tTenancy('tenantSwitcher.noCurrentTenant'),
    })
  )
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(DEFAULT_TENANT_ID)
  setTenantAliases(new Map())
})

describe('the impersonation shell', () => {
  it('lands a start on the tenant home with the banner, the status read and no console entry', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    const backend = makeBackend()
    let phase: 'operator' | 'target' = 'operator'
    const request: RequestFn = <T,>(
      path: string,
      requestOptions?: RequestOptions,
    ) => {
      const answer = backend.fn<T>(path, requestOptions)
      if (path === '/api/v1/auth/impersonation' && requestOptions?.method === 'POST') {
        phase = 'target'
      }
      return answer
    }
    bindRequestFn(request)
    const session = scriptedSession(operatorSnapshot(), () =>
      phase === 'target' ? targetSnapshot() : operatorSnapshot(),
    )
    const user = userEvent.setup()

    renderDemoApp({ session })

    // The console page is reachable and the start form waits for its fields.
    await screen.findByText(tConsole('impersonations.current.none'))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))

    // The start settled; the host's action re-read the session and moved the
    // operator to the tenant home.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')

    // The banner names the impersonated account, and its tenant comes from
    // the status read issued under the new identity.
    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(notice).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )
    expect(
      within(notice).getByText(
        tAuth('impersonation.tenant', { tenant: 'acme' }),
      ),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
    )

    // The impersonated snapshot still holds a platform permission; the
    // console is offered on the identity, not on the set.
    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.console }),
    ).not.toBeInTheDocument()
  })

  it('keeps the banner across tenant-area pages', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })
    expect(await screen.findByRole('region', { name: tAuth('impersonation.regionLabel') })).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: demoEnUS.nav.objects }))

    await waitFor(() => expect(window.location.pathname).toBe('/objects'))
    expect(
      screen.getByRole('region', { name: tAuth('impersonation.regionLabel') }),
    ).toBeInTheDocument()
  })

  it('does not mount the tenant switcher while impersonating', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)

    renderDemoApp({
      session: scriptedSession(targetSnapshot(), () => targetSnapshot()),
    })

    // The status read settles the impersonated identity first, so the
    // absence below is the mounted shell's, not a pending render's.
    await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await waitFor(() =>
      expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
    )

    // The switcher is not in the tree at all -- neither as a live affordance
    // nor as a disabled no-current-tenant button -- and the rest of the top
    // bar renders without it.
    expect(tenantSwitcherTrigger()).toBeNull()
    expect(
      screen.getByRole('button', { name: demoEnUS.languageSwitcher.label }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Target One' }),
    ).toBeInTheDocument()
  })

  it('remounts the tenant switcher once the impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(tenantSwitcherTrigger()).toBeNull()
    const rosterBefore = backend.rosterReads()

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    // The identity change settled and the roster read ran under the
    // operator again: the switcher is back over the roster's row, naming
    // the tenant it settles as before.
    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() =>
      expect(backend.rosterReads()).toBeGreaterThan(rosterBefore),
    )
    expect(
      await screen.findByRole('button', { name: 'acme' }),
    ).toBeInTheDocument()
    expect(getCurrentTenantId()).toBe('acme')
  })

  it('keeps the switcher out of the bar while the roster settles after an impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    // The roster read is held open: the suite decides when the settled
    // tenant (and the switcher over it) may appear.
    const roster = deferred()
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      rosterGate: roster.promise,
    })
    bindRequestFn(backend.fn)
    const session = roundTripSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await waitFor(() =>
      expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
    )
    expect(tenantSwitcherTrigger()).toBeNull()

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    // The identity change has settled and the roster read is in flight. The
    // current tenant still holds the ended grant's value, so the roster list
    // has no matching row to switch to: that is a transition, not the
    // switcher's no-current-tenant state, and the switcher stays out of the
    // bar rather than stating the host has no tenant while it is only
    // reading.
    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() => expect(backend.rosterReads()).toBe(1))
    expect(tenantSwitcherTrigger()).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: tTenancy('tenantSwitcher.noCurrentTenant'),
      }),
    ).toBeNull()

    // The read lands, and the switcher returns over the roster's row.
    await act(async () => {
      roster.resolve()
    })
    expect(
      await screen.findByRole('button', { name: 'acme' }),
    ).toBeInTheDocument()
    expect(getCurrentTenantId()).toBe('acme')
  })

  it('keeps the no-current-tenant state for a viewer whose roster read has not answered', async () => {
    window.history.replaceState({}, '', '/')
    // A viewer with no settled roster read (the member without roster-read
    // access): the tenant is unknown for a reason that is not an identity
    // change. The switcher mounts and states that state -- only the
    // impersonation-end transition withholds it.
    setCurrentTenantId(null)
    const roster = deferred()
    const backend = makeBackend({ rosterGate: roster.promise })
    bindRequestFn(backend.fn)

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => expect(backend.rosterReads()).toBe(1))
    expect(
      screen.getByRole('button', {
        name: tTenancy('tenantSwitcher.noCurrentTenant'),
      }),
    ).toBeInTheDocument()
  })

  it('leaves the top bar unchanged for a member who is not impersonating', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend()
    bindRequestFn(backend.fn)

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The roster settles the current tenant and the switcher stays where
    // the frame put it; no status read is issued and no banner appears.
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(0))
    expect(
      await screen.findByRole('button', { name: 'acme' }),
    ).toBeInTheDocument()
    expect(banner()).toBeNull()
    expect(backend.statusReads()).toBe(0)
  })

  it('ends from the banner, re-reads the identity and re-registers the stream', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const { client, opened } = streamClient()
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session, api: client })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    // The subscription registered under the impersonated account once the
    // status read settled the tenant. The bench renders the production
    // stack, StrictMode included, so the mount effect runs twice: the first
    // connection is closed by the cleanup and the live one is the last.
    await waitFor(() => expect(opened.length).toBeGreaterThan(0))
    const openedBefore = opened.length
    const liveBefore = opened[openedBefore - 1]
    const rosterBefore = backend.rosterReads()

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    // The banner disappears; the confirmation stays in the live region the
    // component keeps mounted.
    await waitFor(() => expect(banner()).toBeNull())
    expect(
      screen.getByText(tAuth('impersonation.ended')),
    ).toBeInTheDocument()

    // The operator's own identity is back: the console entry returns, and
    // the identity-keyed roster read re-ran.
    expect(
      await screen.findByRole('link', { name: demoEnUS.nav.console }),
    ).toBeInTheDocument()
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(rosterBefore))

    // The page did not move.
    expect(window.location.pathname).toBe('/')

    // The stream was rebuilt for the operator's account: the connection that
    // was live before the end closed, and a new registration opened.
    await waitFor(() => expect(opened.length).toBeGreaterThan(openedBefore))
    expect(liveBefore?.closed).toBe(1)
  })

  it('shows the banner for a new impersonation after an end settles normally', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    // The second interval's status read names a grant again: the server holds
    // one in force, unlike the interval the end closed.
    const second = grantFixture(new Date(Date.now() + 3_600_000).toISOString())
    let phase: 'first' | 'second' = 'first'
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (
        phase === 'second' &&
        path === '/api/v1/auth/impersonation' &&
        (options?.method ?? 'GET') === 'GET'
      ) {
        return Promise.resolve({ grant: second } as T)
      }
      const answer = backend.fn<T>(path, options)
      if (path === '/api/v1/auth/impersonation' && options?.method === 'DELETE') {
        phase = 'second'
      }
      return answer
    }
    bindRequestFn(request)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )
    await waitFor(() => expect(banner()).toBeNull())
    await screen.findByRole('link', { name: demoEnUS.nav.console })

    // A later impersonation in the same mount renders its banner: the
    // withheld state belongs to the finished interval.
    act(() => {
      session.set(targetSnapshot())
    })
    const again = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(again).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )
    // The finished interval's confirmation does not cover the new
    // interval's announcement in the permanent live region.
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        tAuth('impersonation.actingAs', { subject: 'Target One' }),
      ),
    )
    expect(screen.getByRole('status')).not.toHaveTextContent(
      tAuth('impersonation.ended'),
    )
  })

  it('shows the banner for a new impersonation after a snapshot flip races the end write', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    const gate = deferred()
    let deletes = 0
    let phase: 'first' | 'second' = 'first'
    const second = grantFixture(new Date(Date.now() + 3_600_000).toISOString())
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (
        phase === 'second' &&
        path === '/api/v1/auth/impersonation' &&
        (options?.method ?? 'GET') === 'GET'
      ) {
        return Promise.resolve({ grant: second } as T)
      }
      if (path === '/api/v1/auth/impersonation' && options?.method === 'DELETE') {
        deletes += 1
        return gate.promise.then(() => backend.fn<T>(path, options))
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    let restores = 0
    const session = scriptedSession(targetSnapshot(), () => {
      restores += 1
      return operatorSnapshot()
    })
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )
    await waitFor(() => expect(deletes).toBe(1))

    // Another cause -- an expiry, a bare 404 elsewhere, another tab -- settles
    // the snapshot while the end write is still in flight.
    act(() => {
      session.set(operatorSnapshot())
    })
    await waitFor(() => expect(banner()).toBeNull())

    // The write settles after the flip: the release edge has already run, so
    // the banner must not be withheld again for the rest of this mount.
    await act(async () => {
      gate.resolve()
      await Promise.resolve()
    })
    await waitFor(() => expect(restores).toBeGreaterThan(0))

    phase = 'second'
    act(() => {
      session.set(targetSnapshot())
    })
    const again = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(again).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )
  })

  it('shows no finished impersonation while the console page\'s status read is pending', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    // Once the console page mounts, its status read is held open: the cached
    // answer is all the page has to render while it waits.
    const statusGate = deferred()
    let holdStatus = false
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/system/permissions') {
        return Promise.resolve({
          permissions: [
            { permission: 'tenant.read' },
            { permission: 'authn.impersonation.read' },
          ],
        } as T)
      }
      if (
        holdStatus &&
        path === '/api/v1/auth/impersonation' &&
        (options?.method ?? 'GET') === 'GET'
      ) {
        return statusGate.promise.then(() => backend.fn<T>(path, options))
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )
    await waitFor(() => expect(banner()).toBeNull())
    await screen.findByRole('link', { name: demoEnUS.nav.console })

    holdStatus = true
    act(() => {
      window.history.pushState({}, '', '/console/impersonations?tenant=acme')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })

    expect(
      await screen.findByText(tConsole('impersonations.current.title')),
    ).toBeInTheDocument()
    // The finished grant is not rendered as current while the read is out:
    // the interval's cache entry was evicted when its read was disabled.
    expect(
      screen.getByText(tConsole('impersonations.current.loading')),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(tConsole('impersonations.current.target'), {
        exact: false,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: tConsole('impersonations.current.end'),
      }),
    ).toBeNull()
  })

  it('shows the banner after a full reload while impersonating', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)

    renderDemoApp({ session: scriptedSession(targetSnapshot(), () => targetSnapshot()) })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(notice).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )
    // The tenant comes from the grant, not from the roster: a roster read
    // is a platform management read and is refused to an impersonated
    // identity, so the frame must not issue one or fall back to the
    // placeholder.
    expect(
      await within(notice).findByText(
        tAuth('impersonation.tenant', { tenant: 'acme' }),
      ),
    ).toBeInTheDocument()
    expect(backend.rosterReads()).toBe(0)
    // The snapshot's `impersonating` enabled the status read.
    expect(backend.statusReads()).toBeGreaterThan(0)
  })

  it('issues no status read and shows no banner for a visitor who is not impersonating', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend()
    bindRequestFn(backend.fn)

    renderDemoApp({ session: scriptedSession(operatorSnapshot(), () => operatorSnapshot()) })

    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    })
    // Let the mount effects settle (the roster read is the visible one).
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(0))

    expect(banner()).toBeNull()
    expect(backend.statusReads()).toBe(0)
  })

  it('runs the ended path when the status read answers a bare 404', async () => {
    window.history.replaceState({}, '', '/')
    // No grant: the status read answers the bare 404 while the snapshot
    // still says impersonating.
    const backend = makeBackend()
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())

    renderDemoApp({ session })

    // The host re-read the session, and the operator's own state returned:
    // the banner is gone and the console entry is offered again.
    await waitFor(() => expect(banner()).toBeNull())
    expect(
      await screen.findByRole('link', { name: demoEnUS.nav.console }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
  })

  it('expires: the identity change runs at the recorded time and the banner goes within the check-ttl bound', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const endsAt = new Date(Date.now() + 30_000).toISOString()
      const backend = makeBackend({ grant: grantFixture(endsAt) })
      bindRequestFn(backend.fn)
      // The first re-read after expiry still answers impersonating (the
      // server's check-ttl cache); the next one has settled back.
      const { client, opened } = streamClient()
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return restores === 1 ? targetSnapshot() : operatorSnapshot()
      })

      renderDemoApp({ session, api: client })
      expect(
        await screen.findByRole('region', {
          name: tAuth('impersonation.regionLabel'),
        }),
      ).toBeInTheDocument()
      // Let the status read's state update and the timer effect settle
      // before the clock moves.
      await act(async () => {
        await Promise.resolve()
      })
      // At the recorded expiry the host runs the identity change; the
      // session still answers impersonating, so the bounded re-reads keep
      // the banner (and its end control) in place.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000)
      })
      expect(restores).toBe(1)
      expect(banner()).not.toBeNull()

      // A later bounded re-read reports the operator, and the full identity
      // change follows -- including its own restore(), so the count reaches
      // three: the expiry change, the bounded retry, and the settled change.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6_000)
      })
      await waitFor(() => expect(banner()).toBeNull())
      expect(restores).toBe(3)

      // The settled change re-registers the live subscription under the
      // operator: the two earlier registrations (mount and expiry change)
      // were taken while the server still answered the impersonated account.
      expect(opened.length).toBeGreaterThanOrEqual(3)
      expect(opened[0]?.closed).toBe(1)
      expect(opened[1]?.closed).toBe(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('treats a bare 404 from the end request as a settled end', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      endStatus: '404',
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    // Fulfilled, not failed: the confirmation renders, the banner closes
    // through the same identity change, and no failure text appears.
    expect(
      await screen.findByText(tAuth('impersonation.ended')),
    ).toBeInTheDocument()
    await waitFor(() => expect(banner()).toBeNull())
    expect(backend.deletes()).toBe(1)
    expect(
      await screen.findByRole('link', { name: demoEnUS.nav.console }),
    ).toBeInTheDocument()
  })

  it('withholds the banner as soon as the end settles, before the session re-read', async () => {
    window.history.replaceState({}, '', '/')
    // The replica's validity cache still answers the grant after the DELETE:
    // the window the host must cover without waiting for the snapshot.
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      statusLags: true,
    })
    bindRequestFn(backend.fn)
    const gated = gatedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session: gated.session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    // The DELETE settled and the re-read is held open. The banner is gone,
    // the confirmation is in the live region that stays mounted, and the
    // end control went with the banner.
    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    await waitFor(() => expect(banner()).toBeNull())
    expect(screen.getByText(tAuth('impersonation.ended'))).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: tAuth('impersonation.end') }),
    ).toBeNull()

    // The re-read settles the operator: the null is released and the banner
    // stays absent, now because the snapshot says none is in force.
    await gated.releaseRestore()
    await waitFor(() => expect(gated.pendingRestores()).toBe(0))
    expect(banner()).toBeNull()
    expect(screen.getByText(tAuth('impersonation.ended'))).toBeInTheDocument()
  })

  it('returns the banner to the snapshot once the bounded re-read spends its check-ttl bound', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const backend = makeBackend({
        grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
        endStatus: '404',
        statusLags: true,
      })
      bindRequestFn(backend.fn)
      // Every re-read keeps naming the impersonated identity: the bound is
      // spent while the snapshot still says true.
      const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })

      renderDemoApp({ session })
      const notice = await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      })
      await user.click(
        within(notice).getByRole('button', {
          name: tAuth('impersonation.end'),
        }),
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(banner()).toBeNull()
      expect(backend.deletes()).toBe(1)

      // The bounded re-reads keep answering impersonating; short of the
      // bound the null stands.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(55_000)
      })
      expect(banner()).toBeNull()

      // The bound is spent: the null is released and the banner returns,
      // snapshot-driven.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000)
      })
      const restored = banner()
      expect(restored).not.toBeNull()

      // The end control works again: a bare 404 is a settled end, so the
      // next identity change runs and converges from there.
      await user.click(
        within(restored!).getByRole('button', {
          name: tAuth('impersonation.end'),
        }),
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(banner()).toBeNull()
      expect(backend.deletes()).toBe(2)
    } finally {
      vi.useRealTimers()
    }
  })

  it('keeps the banner withdrawn when the re-read settles within the bound', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const backend = makeBackend({
        grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
        statusLags: true,
      })
      bindRequestFn(backend.fn)
      // The identity change's own re-read still answers impersonating; the
      // first bounded retry has settled back to the operator.
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return restores === 1 ? targetSnapshot() : operatorSnapshot()
      })
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })

      renderDemoApp({ session })
      const notice = await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      })
      await user.click(
        within(notice).getByRole('button', {
          name: tAuth('impersonation.end'),
        }),
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(banner()).toBeNull()

      // The first bounded retry settles the operator; the banner never
      // reappears, and the loop stops rather than polling to its bound.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6_000)
        await Promise.resolve()
      })
      expect(banner()).toBeNull()
      const settledRestores = restores
      await act(async () => {
        await vi.advanceTimersByTimeAsync(70_000)
      })
      expect(banner()).toBeNull()
      expect(restores).toBe(settledRestores)
      expect(screen.getByText(tAuth('impersonation.ended'))).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('cancels the read in flight and holds new reads until a delayed start settles', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    // The write's round trip is slow, and the record read stays open: one
    // read is in flight at the moment the start is issued.
    const post = deferred()
    const records = deferred()
    const backend = makeBackend({
      postGate: post.promise,
      recordsGate: records.promise,
    })
    let phase: 'operator' | 'target' = 'operator'
    const request: RequestFn = <T,>(
      path: string,
      requestOptions?: RequestOptions,
    ) => {
      const answer = backend.fn<T>(path, requestOptions)
      if (path === '/api/v1/auth/impersonation' && requestOptions?.method === 'POST') {
        phase = 'target'
      }
      return answer
    }
    bindRequestFn(request)
    const gated = gatedSession(operatorSnapshot(), () =>
      phase === 'target' ? targetSnapshot() : operatorSnapshot(),
    )
    const user = userEvent.setup()

    renderDemoApp({ session: gated.session })

    // The start form waits on a settled "not impersonating" answer, and the
    // record read is in flight.
    await screen.findByText(tConsole('impersonations.current.none'))
    await waitFor(() => expect(backend.recordReads()).toBe(1))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')

    // The write is issued: the record read in flight is cancelled at that
    // moment, and the client's queries are paused.
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))
    await waitFor(() => expect(backend.postReads()).toBe(1))
    await waitFor(() => expect(backend.recordsAborted()).toBe(true))
    expect(onlineManager.isOnline()).toBe(false)

    // From the write's issue onward, no read leaves the browser: the
    // settled write's invalidations stay parked behind the pause while the
    // identity change's re-read is held open.
    const readsWhileHeld = backend.reads()
    await act(async () => {
      post.resolve()
    })
    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(backend.reads()).toBe(readsWhileHeld)

    // The re-read settles the impersonated identity and the hold is released
    // with it: the tenant home's reads run under the new session.
    await gated.releaseRestore()
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(onlineManager.isOnline()).toBe(true)
    await waitFor(() => expect(backend.unreadReads()).toBeGreaterThan(0))
  })

  it('releases the hold when the start is refused: no identity change follows', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    const backend = makeBackend({ startRefused: true })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const transitions = transitionTrackingSession(operatorSnapshot())

    renderDemoApp({ session: transitions.session })

    await screen.findByText(tConsole('impersonations.current.none'))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))
    await waitFor(() => expect(backend.postReads()).toBe(1))

    // The write changed no identity, so the session's identity-transition
    // guard is closed exactly once (the returned release, not a second
    // path) and the pause is released at once: the page's reads run again
    // (its refresh control re-reads the status).
    await waitFor(() => expect(onlineManager.isOnline()).toBe(true))
    expect(transitions.opens()).toBe(1)
    expect(transitions.releases()).toBe(1)
    const readsBefore = backend.reads()
    await user.click(screen.getByRole('button', { name: tConsole('common.refresh') }))
    await waitFor(() => expect(backend.reads()).toBeGreaterThan(readsBefore))
    expect(transitions.opens()).toBe(1)
    expect(transitions.releases()).toBe(1)
  })

  it('holds no reads while an end settles: the hold belongs to the start path', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const gated = gatedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session: gated.session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    // The DELETE settled and the identity change's re-read is held open. An
    // end does not rotate the session cookie, so nothing pauses the client's
    // reads.
    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    expect(onlineManager.isOnline()).toBe(true)

    await gated.releaseRestore()
    await waitFor(() => expect(banner()).toBeNull())
  })

  it('leaves ordinary navigation unaffected: the destination read is issued at once', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(0))

    await user.click(
      await screen.findByRole('link', { name: demoEnUS.nav.objects }),
    )

    await waitFor(() => expect(window.location.pathname).toBe('/objects'))
    await waitFor(() => expect(backend.objectsReads()).toBeGreaterThan(0))
    expect(onlineManager.isOnline()).toBe(true)
  })

  it('runs the status read\'s bare 404 once per impersonation interval and bounds its re-read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      // The status read always answers the bare 404 while the session read
      // keeps answering impersonating: the lagging replica the design
      // covers. The status query must not be evicted by the change, or its
      // own re-read would answer a new 404 and re-enter the change.
      const backend = makeBackend({ statusReadLimit: 40 })
      bindRequestFn(backend.fn)
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return targetSnapshot()
      })

      renderDemoApp({ session })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(1_000)
      })

      expect(backend.statusReads()).toBe(1)
      // The one ended path ran; its own bounded re-read continues on the
      // fixed interval.
      expect(restores).toBe(1)
      expect(banner()).not.toBeNull()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60_000)
      })
      // One identity change plus the retries the check-ttl bound allows.
      expect(restores).toBeGreaterThan(1)
      expect(restores).toBeLessThanOrEqual(13)
      // The query was never evicted, so no automatic read re-entered it.
      expect(backend.statusReads()).toBe(1)

      const settled = restores
      await act(async () => {
        await vi.advanceTimersByTimeAsync(120_000)
      })
      expect(restores).toBe(settled)
      expect(backend.statusReads()).toBe(1)
      // The bound is spent; the banner and its end control stay.
      const notice = banner()
      expect(notice).not.toBeNull()
      expect(
        within(notice!).getByRole('button', {
          name: tAuth('impersonation.end'),
        }),
      ).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('stops the expiry re-read at the check-ttl upper bound', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const endsAt = new Date(Date.now() + 30_000).toISOString()
      const backend = makeBackend({ grant: grantFixture(endsAt) })
      bindRequestFn(backend.fn)
      // Every re-read keeps answering impersonating: the bound, not the
      // server, has to stop the loop.
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return targetSnapshot()
      })

      renderDemoApp({ session })
      await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      })
      // Let the status read settle so the recorded expiry is in place
      // before the clock moves.
      await waitFor(() =>
        expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
      )
      await act(async () => {
        await Promise.resolve()
      })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000)
      })
      expect(restores).toBe(1)

      // Past the whole check-ttl upper bound from the recorded expiry: one
      // change plus the 5-second retries, never more.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(70_000)
      })
      expect(restores).toBeGreaterThan(1)
      expect(restores).toBeLessThanOrEqual(14)

      const settled = restores
      await act(async () => {
        await vi.advanceTimersByTimeAsync(120_000)
      })
      expect(restores).toBe(settled)
      const notice = banner()
      expect(notice).not.toBeNull()
      expect(
        within(notice!).getByRole('button', {
          name: tAuth('impersonation.end'),
        }),
      ).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
  })

  it('bounds the re-read when expiry answers 404 while the session read still impersonates', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const endsAt = new Date(Date.now() + 30_000).toISOString()
      const backend = makeBackend({
        grant: grantFixture(endsAt),
        // The server's own Active judgement answers the bare 404 once the
        // recorded expiry passed, while the session read keeps answering
        // impersonating for one check-ttl.
        status404AfterExpiry: true,
        statusReadLimit: 40,
      })
      bindRequestFn(backend.fn)
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return targetSnapshot()
      })

      renderDemoApp({ session })
      await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      })
      // Let the status read settle so the recorded expiry is in place
      // before the clock moves.
      await waitFor(() =>
        expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
      )
      await act(async () => {
        await Promise.resolve()
      })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000)
      })
      // The expiry change ran; the status query was not evicted, so it has
      // not been re-read yet.
      expect(restores).toBe(1)
      expect(backend.statusReads()).toBe(1)

      // The tab becoming visible again re-reads the status read under the
      // new judgement: the bare 404 is handled once, and the bounded re-read
      // starts.
      await act(async () => {
        window.dispatchEvent(new Event('visibilitychange'))
        await Promise.resolve()
      })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(70_000)
      })

      // Two identity changes (expiry, then the 404) plus the retries their
      // two bounds allow -- not the request storm a self-driving loop makes.
      expect(restores).toBeGreaterThanOrEqual(2)
      expect(restores).toBeLessThanOrEqual(30)
      expect(backend.statusReads()).toBeLessThanOrEqual(3)

      const settled = restores
      await act(async () => {
        await vi.advanceTimersByTimeAsync(120_000)
      })
      expect(restores).toBe(settled)
      expect(banner()).not.toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not restart the expiry path when the route changes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      // The recorded expiry is already past and its check-ttl bound with it,
      // so the one expiry change runs and the timer stops.
      const endsAt = new Date(Date.now() - 70_000).toISOString()
      const backend = makeBackend({ grant: grantFixture(endsAt) })
      bindRequestFn(backend.fn)
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return targetSnapshot()
      })
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })

      renderDemoApp({ session })
      await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      })
      await waitFor(() =>
        expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })
      expect(restores).toBe(1)
      const statusReads = backend.statusReads()

      await user.click(
        await screen.findByRole('link', { name: demoEnUS.nav.objects }),
      )
      await waitFor(() => expect(window.location.pathname).toBe('/objects'))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })

      // The identity change's identity is stable, so the timer effect did
      // not re-run on the navigation.
      expect(restores).toBe(1)
      expect(backend.statusReads()).toBe(statusReads)
    } finally {
      vi.useRealTimers()
    }
  })

  it.each([
    [
      'a failed feature document and an unknown set',
      { state: 'failed' as const, error: new Error('unavailable') },
      null,
    ],
    [
      'a pending feature document and an unknown set',
      { state: 'pending' as const },
      null,
    ],
    [
      'a ready document and an unknown set',
      {
        state: 'ready' as const,
        names: new Set(FEATURE_NAMES),
      },
      null,
    ],
    [
      'a ready document with no console feature and a known set',
      { state: 'ready' as const, names: new Set<string>() },
      ['tenant.read'],
    ],
  ])(
    'leaves /console/* for the tenant home before the feature state and permission set (%s)',
    async (_label, features, system) => {
      window.history.replaceState({}, '', '/console/tenants')
      const backend = makeBackend()
      bindRequestFn(backend.fn)
      const impersonated = {
        ...authenticatedSnapshot({ system, features }),
        impersonating: true,
      }
      const operator = {
        ...authenticatedSnapshot({ system, features }),
        impersonating: false,
      }
      renderDemoApp({
        session: scriptedSession(impersonated, () => operator),
      })

      // The impersonation gate runs first, so the console's own feature
      // waiting/failure views and its access placeholder never render.
      await waitFor(() => expect(window.location.pathname).toBe('/'))
      expect(screen.queryByText(demoEnUS.route.failed)).toBeNull()
      expect(
        screen.queryByRole('progressbar', { name: demoEnUS.route.pending }),
      ).toBeNull()
      expect(
        screen.queryByRole('progressbar', {
          name: demoEnUS.console.accessPending,
        }),
      ).toBeNull()
    },
  )

  it('resets the platform set to unknown immediately before re-reading it after an impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    // The catalog read is a platform-staff operation: refused while the
    // session impersonates, answered for the operator.
    let impersonating = true
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/system/permissions') {
        return impersonating
          ? Promise.reject({ status: 403, code: 'rbac.permission_denied' })
          : Promise.resolve({
              permissions: [{ permission: 'tenant.read' }],
            } as T)
      }
      const answer = backend.fn<T>(path, options)
      if (path === '/api/v1/auth/impersonation' && options?.method === 'DELETE') {
        impersonating = false
      }
      return answer
    }
    bindRequestFn(request)
    // The impersonated snapshot holds a known empty set (the refused read);
    // the operator's own set is unknown until the catalog read answers.
    const target: AuthSnapshot = {
      ...targetSnapshot(),
      permissionSets: { tenant: null, system: [] },
    }
    const operator: AuthSnapshot = {
      ...operatorSnapshot(),
      permissionSets: { tenant: null, system: null },
    }
    const base = roundTripSession(target, () => operator)
    const attached: (readonly string[] | null)[] = []
    const session = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        if (domain === 'system') {
          attached.push(perms)
        }
        base.setPermissionSet(domain, perms)
      },
    }
    const user = userEvent.setup()

    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await waitFor(() => expect(attached).toEqual([null, []]))
    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.console }),
    ).toBeNull()

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )
    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() =>
      expect(session.getSnapshot().permissionSets.system).toEqual([
        'tenant.read',
      ]),
    )

    // The set went unknown immediately before the re-read that answered it;
    // without that reset a stale set could outlive the identity it was read
    // under.
    expect(attached).toEqual([null, [], null, ['tenant.read']])
    expect(
      await screen.findByRole('link', { name: demoEnUS.nav.console }),
    ).toBeInTheDocument()
  })

  it('keeps the settled permission set when the start\'s session re-read hangs', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    const backend = makeBackend()
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/system/permissions') {
        return Promise.resolve({
          permissions: [
            { permission: 'tenant.read' },
            { permission: 'authn.impersonation.read' },
          ],
        } as T)
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    const gated = gatedSession(operatorSnapshot(), () => targetSnapshot())
    const base = gated.session
    const session = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: {
            tenant:
              domain === 'tenant' ? perms : current.permissionSets.tenant,
            system:
              domain === 'system' ? perms : current.permissionSets.system,
          },
        })
      },
    }
    const user = userEvent.setup()

    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    await screen.findByText(tConsole('impersonations.current.none'))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))

    // The write settled and the identity change is waiting on the session
    // re-read. The console must keep rendering: its permission set was not
    // cleared ahead of the re-read.
    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    expect(window.location.pathname).toBe('/console/impersonations')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.console.accessPending,
      }),
    ).toBeNull()
    expect(
      screen.getByText(tConsole('impersonations.current.title')),
    ).toBeInTheDocument()

    await gated.releaseRestore()
    await waitFor(() => expect(window.location.pathname).toBe('/'))
  })

  it('keeps the settled permission set when the start\'s session re-read is refused', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    const backend = makeBackend()
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/system/permissions') {
        return Promise.resolve({
          permissions: [
            { permission: 'tenant.read' },
            { permission: 'authn.impersonation.read' },
          ],
        } as T)
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    const base = createTestSession(operatorSnapshot())
    const session = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: {
            tenant:
              domain === 'tenant' ? perms : current.permissionSets.tenant,
            system:
              domain === 'system' ? perms : current.permissionSets.system,
          },
        })
      },
    }
    const user = userEvent.setup()

    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    await screen.findByText(tConsole('impersonations.current.none'))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))
    await waitFor(() => expect(backend.postReads()).toBe(1))

    // The re-read was refused, and the identity did not change; the console
    // keeps the set it had rather than waiting on an unknown one forever.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(window.location.pathname).toBe('/console/impersonations')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.console.accessPending,
      }),
    ).toBeNull()
    expect(session.getSnapshot().permissionSets.system).toEqual([
      'tenant.read',
      'authn.impersonation.read',
    ])
  })

  it('keeps the settled permission set when an end changes no identity', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    // Another tab started the impersonation: the server's status read has a
    // grant while this tab's session still names the operator.
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/system/permissions') {
        return Promise.resolve({
          permissions: [
            { permission: 'tenant.read' },
            { permission: 'authn.impersonation.read' },
          ],
        } as T)
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    // restore() retains the sets the host attached: the re-read names the
    // same operator and leaves the permission table as it found it.
    const base = scriptedSession(operatorSnapshot(), () => ({
      ...operatorSnapshot(),
      permissionSets: base.getSnapshot().permissionSets,
    }))
    const session = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: {
            tenant:
              domain === 'tenant' ? perms : current.permissionSets.tenant,
            system:
              domain === 'system' ? perms : current.permissionSets.system,
          },
        })
      },
    }
    const user = userEvent.setup()

    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    // The console's own status surface settles the grant; the end control
    // belongs to it.
    const end = await screen.findByRole('button', {
      name: tConsole('impersonations.current.end'),
    })
    await user.click(end)
    await waitFor(() => expect(backend.deletes()).toBe(1))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })

    // The identity did not move, so the set's read did not re-run; the set
    // must still be the known one, not a value cleared by the change.
    expect(window.location.pathname).toBe('/console/impersonations')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.console.accessPending,
      }),
    ).toBeNull()
    expect(session.getSnapshot().permissionSets.system).toEqual([
      'tenant.read',
      'authn.impersonation.read',
    ])
  })

  it('starts a newly mounted frame from an unknown tenant during an impersonation', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    // The operator's tenant is what the module-level cell holds while the
    // console has the frame unmounted.
    setCurrentTenantId('acme')
    setTenantAliases(
      new Map([
        ['acme', 'acme'],
        ['beta', 'beta'],
      ]),
    )
    const status = deferred()
    const backend = makeBackend({
      statusGateAfterStart: status.promise,
      startTenant: 'beta',
    })
    const { client, opened, tenants } = streamClient()
    let phase: 'operator' | 'target' = 'operator'
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      const answer = backend.fn<T>(path, options)
      if (path === '/api/v1/auth/impersonation' && options?.method === 'POST') {
        phase = 'target'
      }
      return answer
    }
    bindRequestFn(request)
    const session = scriptedSession(operatorSnapshot(), () =>
      phase === 'target' ? targetSnapshot() : operatorSnapshot(),
    )
    const user = userEvent.setup()

    renderDemoApp({ session, api: client })

    await screen.findByText(tConsole('impersonations.current.none'))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')
    const tenantBefore = backend.tenantReads()
    const openedBefore = opened.length
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))

    // The frame remounted while the status read is still held: the grant's
    // tenant is not known, so no tenant-scoped read and no stream may run
    // under the operator's leftover tenant.
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(getCurrentTenantId()).toBeNull()
    expect(backend.tenantReads()).toBe(tenantBefore)
    expect(opened.length).toBe(openedBefore)

    // The status read settles the grant's tenant; reads and the stream now
    // run under it.
    await act(async () => {
      status.resolve()
    })
    await waitFor(() => expect(backend.unreadReads()).toBeGreaterThan(0))
    expect(backend.unreadTenantIds().every((id) => id === 'beta')).toBe(true)
    await waitFor(() => expect(opened.length).toBeGreaterThan(openedBefore))
    expect(tenants.at(-1)).toBe('beta')
  })

  it('falls back to the account identifier when the principal has no display name', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const base = targetSnapshot()
    const noDisplayName: AuthSnapshot = {
      ...base,
      principal: { ...base.principal!, display_name: '' },
    }

    renderDemoApp({
      session: scriptedSession(noDisplayName, () => noDisplayName),
    })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(notice).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'acc-1' }),
    )
  })

  it('starts without a blanking window: the banner appears when the re-read settles', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    const backend = makeBackend()
    let phase: 'operator' | 'target' = 'operator'
    const request: RequestFn = <T,>(
      path: string,
      requestOptions?: RequestOptions,
    ) => {
      const answer = backend.fn<T>(path, requestOptions)
      if (path === '/api/v1/auth/impersonation' && requestOptions?.method === 'POST') {
        phase = 'target'
      }
      return answer
    }
    bindRequestFn(request)
    const session = scriptedSession(operatorSnapshot(), () =>
      phase === 'target' ? targetSnapshot() : operatorSnapshot(),
    )
    const user = userEvent.setup()

    renderDemoApp({ session })

    await screen.findByText(tConsole('impersonations.current.none'))
    await user.type(screen.getByLabelText(tConsole('impersonations.start.accountLabel')), 'acc-1')
    await user.click(screen.getByRole('button', { name: tConsole('impersonations.start.submit') }))

    // A start announces itself through the re-read: the banner appears with
    // the impersonated identity and no end confirmation precedes it.
    expect(
      await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      }),
    ).toBeInTheDocument()
    expect(screen.queryByText(tAuth('impersonation.ended'))).toBeNull()
  })

  it('closes the notice, keeps the live region, and ends from the account menu', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', {
        name: tAuth('impersonation.hide'),
      }),
    )

    // The visible notice is gone; the start announcement stays in the
    // permanent live region.
    expect(banner()).toBeNull()
    expect(
      document.querySelector(
        `section[aria-label="${tAuth('impersonation.regionLabel')}"]`,
      ),
    ).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )

    // The header chip is now collapsed, and it took the focus the close
    // control could no longer hold.
    const chip = screen.getByRole('button', {
      name: tAuth('impersonation.impersonating'),
    })
    expect(chip).toHaveAttribute('aria-expanded', 'false')
    expect(chip).toHaveFocus()

    // The account menu still carries the way out while the banner is
    // hidden, and it is the same write attempt.
    await user.click(screen.getByRole('button', { name: 'Target One' }))
    await user.click(
      screen.getByRole('menuitem', { name: tAuth('impersonation.end') }),
    )

    await waitFor(() => expect(banner()).toBeNull())
    expect(
      screen.queryByRole('button', {
        name: tAuth('impersonation.impersonating'),
      }),
    ).toBeNull()
    // Ending the impersonation remounts the tenant menu, whose switcher
    // mounts its own (empty) role="status" region from the start, so the
    // shell's permanent live region is no longer the only status region.
    // Locate the ended confirmation through its text and assert the region
    // that carries it.
    expect(
      screen
        .getByText(tAuth('impersonation.ended'))
        .closest('[role="status"]'),
    ).toHaveTextContent(tAuth('impersonation.ended'))
    expect(backend.deletes()).toBe(1)
  })

  it('brings a dismissed notice back to show a failed end from the account menu', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      endStatus: 'error',
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', {
        name: tAuth('impersonation.hide'),
      }),
    )
    expect(banner()).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Target One' }))
    await user.click(
      screen.getByRole('menuitem', { name: tAuth('impersonation.end') }),
    )

    // The failure text belongs to the banner, which was hidden: the host
    // resets the dismissal so the error is visible, and the chip is
    // expanded again.
    const again = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(
      within(again).getByRole('alert'),
    ).toHaveTextContent(tAuth('errors.authn.unavailable'))
    expect(
      screen.getByRole('button', {
        name: tAuth('impersonation.impersonating'),
      }),
    ).toHaveAttribute('aria-expanded', 'true')
    expect(backend.deletes()).toBe(1)
  })

  it('restores the notice from the header chip and keeps focus on it', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', {
        name: tAuth('impersonation.hide'),
      }),
    )
    const chip = screen.getByRole('button', {
      name: tAuth('impersonation.impersonating'),
    })
    await user.click(chip)

    expect(
      await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      }),
    ).toBeInTheDocument()
    expect(chip).toHaveAttribute('aria-expanded', 'true')
    expect(chip).toHaveFocus()
  })

  it('keeps the notice dismissed across a tenant-area route change', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', {
        name: tAuth('impersonation.hide'),
      }),
    )
    expect(banner()).toBeNull()

    // A tenant-area route change keeps the same frame and the same
    // dismissal: the notice does not pop back open on navigation.
    await user.click(screen.getByRole('link', { name: demoEnUS.nav.account }))
    await waitFor(() => expect(window.location.pathname).toBe('/account'))
    expect(banner()).toBeNull()
    expect(
      screen.getByRole('button', {
        name: tAuth('impersonation.impersonating'),
      }),
    ).toHaveAttribute('aria-expanded', 'false')
  })

  it('resets the dismissal when the impersonated identity changes', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', {
        name: tAuth('impersonation.hide'),
      }),
    )
    expect(banner()).toBeNull()

    // The same grant now names another impersonated account: an identity
    // change, which is a reset edge.
    const next = targetSnapshot()
    act(() => {
      session.set({
        ...next,
        principal: {
          ...next.principal!,
          account_id: 'acc-2',
          display_name: 'Target Two',
        },
      })
    })

    expect(
      await screen.findByRole('region', {
        name: tAuth('impersonation.regionLabel'),
      }),
    ).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target Two' }),
    )
  })

  it('places the notice below the app bar, with header controls before banner controls in tab order', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => targetSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    // The AppBar is the shell's banner landmark; the page may carry a
    // header of its own inside main, so take the first one.
    const header = screen.getAllByRole('banner')[0]
    if (header === undefined) {
      throw new Error('expected the shell banner landmark')
    }
    // Below the header in document order, inside the same fixed head.
    expect(
      header.compareDocumentPosition(notice) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(notice.parentElement?.parentElement).toBe(header.parentElement)

    // The skip link is the shell's first focusable element.
    await user.tab()
    expect(
      screen.getByRole('link', { name: tLayout('appShell.skipToContent') }),
    ).toHaveFocus()

    // Then the header's controls (the chip and the account trigger), then
    // the banner's own controls.
    const chip = screen.getByRole('button', {
      name: tAuth('impersonation.impersonating'),
    })
    chip.focus()
    await user.tab()
    expect(
      screen.getByRole('button', { name: 'Target One' }),
    ).toHaveFocus()
    await user.tab()
    expect(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    ).toHaveFocus()
    await user.tab()
    expect(
      within(notice).getByRole('button', {
        name: tAuth('impersonation.hide'),
      }),
    ).toHaveFocus()
  })
})
