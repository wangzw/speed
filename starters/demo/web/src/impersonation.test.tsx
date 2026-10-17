/**
 * The impersonation shell in the demo host: the identity-change action, the
 * banner in the tenant frame, the console's redirect while impersonating,
 * the status read's bare-404 rule and the expiry timer.
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
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthSnapshot } from '@speed/auth-core'
import type { RequestFn, RequestOptions, SpeedClient } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import authUiEnUS from '../../../../web/packages/auth-ui/src/locales/en-US.json' with { type: 'json' }
import tenancyUiEnUS from '../../../../web/packages/tenancy-ui/src/locales/en-US.json' with { type: 'json' }
import demoEnUS from './locales/en-US.json' with { type: 'json' }
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
  readonly rosterReads: () => number
  readonly statusReads: () => number
  readonly statusReadsWithGrant: () => number
  readonly recordReads: () => number
  readonly deletes: () => number
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
  readonly endStatus?: 'ok' | '404'
  /** Holds the roster read open until the suite resolves it. */
  readonly rosterGate?: Promise<void>
  /** Keeps the status read answering the grant after the DELETE settles: a
   * replica whose check-ttl cache has not expired yet. */
  readonly statusLags?: boolean
} = {}): Backend {
  let grant = options.grant
  let rosterReads = 0
  let statusReads = 0
  let statusReadsWithGrant = 0
  let recordReads = 0
  let deletes = 0
  const fn: RequestFn = <T,>(
    path: string,
    request?: RequestOptions,
  ): Promise<T> => {
    const method = request?.method ?? 'GET'
    if (path === '/tenant/admin/tenants') {
      rosterReads += 1
      if (options.rosterGate !== undefined) {
        return options.rosterGate.then(() => ({ tenants: [TENANT] }) as T)
      }
      return Promise.resolve({ tenants: [TENANT] } as T)
    }
    if (path === '/auth/impersonation') {
      if (method === 'GET') {
        statusReads += 1
        if (grant === undefined) {
          return Promise.reject({ status: 404, code: 'client.http.404' })
        }
        statusReadsWithGrant += 1
        return Promise.resolve({ grant } as T)
      }
      if (method === 'POST') {
        grant = grantFixture(new Date(Date.now() + 3_600_000).toISOString())
        return Promise.resolve({ grant } as T)
      }
      if (method === 'DELETE') {
        deletes += 1
        if (options.statusLags !== true) {
          grant = undefined
        }
        if (options.endStatus === '404') {
          return Promise.reject({ status: 404, code: 'client.http.404' })
        }
        return Promise.resolve(undefined as T)
      }
    }
    if (path.startsWith('/auth/admin/impersonations')) {
      recordReads += 1
      return Promise.resolve({ impersonations: [] } as T)
    }
    // The tenant header's bell reads the unread count; the inbox page the
    // message list. Answer both so a mounted tenant frame has no query
    // whose function returned undefined.
    if (path === '/api/v1/notifications/messages/unread-count') {
      return Promise.resolve({ unread: 0 } as T)
    }
    if (path === '/api/v1/notifications/messages') {
      return Promise.resolve({ messages: [], total: 0 } as T)
    }
    return Promise.resolve(undefined as T)
  }
  return {
    fn,
    rosterReads: () => rosterReads,
    statusReads: () => statusReads,
    statusReadsWithGrant: () => statusReadsWithGrant,
    recordReads: () => recordReads,
    deletes: () => deletes,
  }
}

/** A client whose event-stream opener records every connection. */
function streamClient(): {
  readonly client: SpeedClient
  readonly opened: { closed: number }[]
} {
  const opened: { closed: number }[] = []
  const request = (() =>
    Promise.reject(new Error('the suite answers through bindRequestFn'))) as RequestFn
  const client = Object.assign(request, {
    openEventStream: () => {
      const record = { closed: 0 }
      opened.push(record)
      return {
        close: () => {
          record.closed += 1
        },
      }
    },
  }) as SpeedClient
  return { client, opened }
}

/** The banner's named region, or null while none is in force. */
function banner(): HTMLElement | null {
  return screen.queryByRole('region', {
    name: authUiEnUS.impersonation.regionLabel,
  })
}

/**
 * The tenant switcher's trigger, or null while it is not mounted. The trigger
 * is named by the current tenant's row, or by the no-current-tenant label
 * while the host has settled none.
 */
function tenantSwitcherTrigger(): HTMLElement | null {
  return (
    screen.queryByRole('button', { name: 'Acme Inc.' }) ??
    screen.queryByRole('button', {
      name: tenancyUiEnUS.tenantSwitcher.noCurrentTenant,
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
      if (path === '/auth/impersonation' && requestOptions?.method === 'POST') {
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
    await screen.findByText('You are not impersonating any account.')
    await user.type(screen.getByLabelText('Target account'), 'acc-1')
    await user.click(screen.getByRole('button', { name: 'Start impersonation' }))

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
      name: authUiEnUS.impersonation.regionLabel,
    })
    expect(
      within(notice).getByText(
        authUiEnUS.impersonation.actingAs.replace('{{subject}}', 'Target One'),
      ),
    ).toBeInTheDocument()
    expect(
      within(notice).getByText(
        authUiEnUS.impersonation.tenant.replace('{{tenant}}', 'acme'),
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
    expect(await screen.findByRole('region', { name: authUiEnUS.impersonation.regionLabel })).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: demoEnUS.nav.objects }))

    await waitFor(() => expect(window.location.pathname).toBe('/objects'))
    expect(
      screen.getByRole('region', { name: authUiEnUS.impersonation.regionLabel }),
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
      name: authUiEnUS.impersonation.regionLabel,
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
      name: authUiEnUS.impersonation.regionLabel,
    })
    expect(tenantSwitcherTrigger()).toBeNull()
    const rosterBefore = backend.rosterReads()

    await user.click(
      within(notice).getByRole('button', { name: authUiEnUS.impersonation.end }),
    )

    // The identity change settled and the roster read ran under the
    // operator again: the switcher is back over the roster's row, naming
    // the tenant it settles as before.
    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() =>
      expect(backend.rosterReads()).toBeGreaterThan(rosterBefore),
    )
    expect(
      await screen.findByRole('button', { name: 'Acme Inc.' }),
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
      name: authUiEnUS.impersonation.regionLabel,
    })
    await waitFor(() =>
      expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
    )
    expect(tenantSwitcherTrigger()).toBeNull()

    await user.click(
      within(notice).getByRole('button', { name: authUiEnUS.impersonation.end }),
    )

    // The identity change has settled and the roster read is in flight, so
    // the host holds no current tenant yet. That is a transition, not the
    // switcher's no-current-tenant state: the switcher stays out of the bar
    // rather than stating the host has no tenant while it is only reading.
    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() => expect(backend.rosterReads()).toBe(1))
    expect(tenantSwitcherTrigger()).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: tenancyUiEnUS.tenantSwitcher.noCurrentTenant,
      }),
    ).toBeNull()

    // The read lands, and the switcher returns over the roster's row.
    await act(async () => {
      roster.resolve()
    })
    expect(
      await screen.findByRole('button', { name: 'Acme Inc.' }),
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
        name: tenancyUiEnUS.tenantSwitcher.noCurrentTenant,
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
      await screen.findByRole('button', { name: 'Acme Inc.' }),
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
      name: authUiEnUS.impersonation.regionLabel,
    })
    // The subscription registered under the impersonated account once the
    // status read settled the tenant.
    await waitFor(() => expect(opened.length).toBe(1))
    const rosterBefore = backend.rosterReads()

    await user.click(
      within(notice).getByRole('button', { name: authUiEnUS.impersonation.end }),
    )

    // The banner disappears; the confirmation stays in the live region the
    // component keeps mounted.
    await waitFor(() => expect(banner()).toBeNull())
    expect(
      screen.getByText(authUiEnUS.impersonation.ended),
    ).toBeInTheDocument()

    // The operator's own identity is back: the console entry returns, and
    // the identity-keyed roster read re-ran.
    expect(
      await screen.findByRole('link', { name: demoEnUS.nav.console }),
    ).toBeInTheDocument()
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(rosterBefore))

    // The page did not move.
    expect(window.location.pathname).toBe('/')

    // The stream was rebuilt for the operator's account: the old connection
    // closed and a new registration opened.
    await waitFor(() => expect(opened.length).toBeGreaterThanOrEqual(2))
    expect(opened[0]?.closed).toBe(1)
  })

  it('shows the banner after a full reload while impersonating', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    bindRequestFn(backend.fn)

    renderDemoApp({ session: scriptedSession(targetSnapshot(), () => targetSnapshot()) })

    const notice = await screen.findByRole('region', {
      name: authUiEnUS.impersonation.regionLabel,
    })
    expect(
      within(notice).getByText(
        authUiEnUS.impersonation.actingAs.replace('{{subject}}', 'Target One'),
      ),
    ).toBeInTheDocument()
    // The tenant comes from the grant, not from the roster: a roster read
    // is a platform management read and is refused to an impersonated
    // identity, so the frame must not issue one or fall back to the
    // placeholder.
    expect(
      await within(notice).findByText(
        authUiEnUS.impersonation.tenant.replace('{{tenant}}', 'acme'),
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
      let restores = 0
      const session = scriptedSession(targetSnapshot(), () => {
        restores += 1
        return restores === 1 ? targetSnapshot() : operatorSnapshot()
      })

      renderDemoApp({ session })
      expect(
        await screen.findByRole('region', {
          name: authUiEnUS.impersonation.regionLabel,
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
      // change follows.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(6_000)
      })
      await waitFor(() => expect(banner()).toBeNull())
      expect(restores).toBe(2)
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
      name: authUiEnUS.impersonation.regionLabel,
    })
    await user.click(
      within(notice).getByRole('button', { name: authUiEnUS.impersonation.end }),
    )

    // Fulfilled, not failed: the confirmation renders, the banner closes
    // through the same identity change, and no failure text appears.
    expect(
      await screen.findByText(authUiEnUS.impersonation.ended),
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
      name: authUiEnUS.impersonation.regionLabel,
    })
    await user.click(
      within(notice).getByRole('button', { name: authUiEnUS.impersonation.end }),
    )

    // The DELETE settled and the re-read is held open. The banner is gone,
    // the confirmation is in the live region that stays mounted, and the
    // end control went with the banner.
    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    await waitFor(() => expect(banner()).toBeNull())
    expect(screen.getByText(authUiEnUS.impersonation.ended)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: authUiEnUS.impersonation.end }),
    ).toBeNull()

    // The re-read settles the operator: the null is released and the banner
    // stays absent, now because the snapshot says none is in force.
    await gated.releaseRestore()
    await waitFor(() => expect(gated.pendingRestores()).toBe(0))
    expect(banner()).toBeNull()
    expect(screen.getByText(authUiEnUS.impersonation.ended)).toBeInTheDocument()
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
        name: authUiEnUS.impersonation.regionLabel,
      })
      await user.click(
        within(notice).getByRole('button', {
          name: authUiEnUS.impersonation.end,
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
          name: authUiEnUS.impersonation.end,
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
        name: authUiEnUS.impersonation.regionLabel,
      })
      await user.click(
        within(notice).getByRole('button', {
          name: authUiEnUS.impersonation.end,
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
      expect(screen.getByText(authUiEnUS.impersonation.ended)).toBeInTheDocument()
    } finally {
      vi.useRealTimers()
    }
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
      if (path === '/auth/impersonation' && requestOptions?.method === 'POST') {
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

    await screen.findByText('You are not impersonating any account.')
    await user.type(screen.getByLabelText('Target account'), 'acc-1')
    await user.click(screen.getByRole('button', { name: 'Start impersonation' }))

    // A start announces itself through the re-read: the banner appears with
    // the impersonated identity and no end confirmation precedes it.
    expect(
      await screen.findByRole('region', {
        name: authUiEnUS.impersonation.regionLabel,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByText(authUiEnUS.impersonation.ended)).toBeNull()
  })
})
