import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { onlineManager } from '@tanstack/react-query'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthDomain, AuthSession, AuthSnapshot } from '@speed/auth-core'
import type { RequestFn, RequestOptions, SpeedClient } from '@speed/api-client'
import { getAuthnImpersonationStatusQueryKey } from '@speed/api-sdk'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { AUTH_UI_NAMESPACE } from '@speed/auth-ui'
import {
  ADMIN_UI_NAMESPACE,
  adminUiResources,
} from '@speed/admin-ui'
import { LAYOUT_KIT_NAMESPACE, avatarInitials } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import {
  createSpeedI18n,
} from '@speed/product-shell/bootstrap'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { PlatformPermissions } from './platform-permissions.js'
import {
  getCurrentTenantName,
  getCurrentTenant,
  setCurrentTenant,
  setTenantNames,
} from './chrome.js'
import { clearSelfTenants } from './tenant-self.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'
import { restoreRealTimers } from './test-utils/timers.js'

function accountTriggerName(displayName: string): string {
  return `${avatarInitials(displayName)} ${displayName}`
}


const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
    {
      namespace: NOTIFICATION_UI_NAMESPACE,
      resources: notificationUiResources,
    },
  ],
})
const tAuth = textI18n.getFixedT('en-US', AUTH_UI_NAMESPACE)
const tAdmin = textI18n.getFixedT('en-US', ADMIN_UI_NAMESPACE)
const tLayout = textI18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)

function announcementRegion(): HTMLElement {
  const regions = screen
    .getAllByRole('status')
    .filter(
      (region) =>
        region.getAttribute('aria-atomic') === 'true' &&
        region.getAttribute('aria-label') !==
          demoEnUS.home.notice.region,
    )
  expect(regions).toHaveLength(1)
  return regions[0]!
}

const TENANT = { name: 'acme', display_name: 'Acme', kind: 'business' as const, status: 'active' as const }

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

function targetSnapshot(): AuthSnapshot {
  const base = authenticatedSnapshot({ system: [] })
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

function restoreOutcomeSession(
  session: ReturnType<typeof createTestSession>,
  outcomes: ('settled' | 'rejected')[],
): ReturnType<typeof createTestSession> {
  return {
    ...session,
    restore: () => {
      const answer = session.restore()
      void answer.then(
        () => {
          outcomes.push('settled')
        },
        () => {
          outcomes.push('rejected')
        },
      )
      return answer
    },
  }
}

interface Backend {
  readonly fn: RequestFn
  readonly reads: () => number
  readonly rosterReads: () => number
  readonly selfReads: () => number
  readonly statusReads: () => number
  readonly statusReadsWithGrant: () => number
  readonly recordReads: () => number
  readonly recordsAborted: () => boolean
  readonly postReads: () => number
  readonly unreadReads: () => number
  readonly unreadTenantIds: () => readonly (string | null)[]
  readonly unreadNames: () => readonly (string | null)[]
  readonly messagesReads: () => number
  readonly deletes: () => number
  readonly tenantReads: () => number
}

function deferred(): { readonly promise: Promise<void>; readonly resolve: () => void } {
  let resolve!: () => void
  const promise = new Promise<void>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function makeBackend(options: {
  readonly grant?: GrantFixture
  readonly endStatus?: 'ok' | '404' | 'error'
  readonly rosterGate?: Promise<void>
  readonly selfGateAfterEnd?: Promise<void>
  readonly statusLags?: boolean
  readonly status404AfterExpiry?: boolean
  readonly statusGateAfterStart?: Promise<void>
  readonly startTenant?: string
  readonly selfTenants?: readonly {
    readonly name: string
    readonly displayName?: string
    readonly kind?: 'business' | 'personal'
  }[]
  readonly statusReadLimit?: number
  readonly postGate?: Promise<void>
  readonly recordsGate?: Promise<void>
  readonly startRefused?: boolean
} = {}): Backend {
  let grant = options.grant
  let reads = 0
  let rosterReads = 0
  let selfReads = 0
  let statusReads = 0
  let statusReadsWithGrant = 0
  let recordReads = 0
  let recordSignal: AbortSignal | undefined
  let postReads = 0
  let unreadReads = 0
  const unreadTenantIds: (string | null)[] = []
  const unreadNames: (string | null)[] = []
  let messagesReads = 0
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
    if (path.startsWith('/api/v1/notifications/')) {
      tenantReads += 1
    }
    if (path === '/api/v1/auth/me/tenants') {
      selfReads += 1
      const answered = { tenants: options.selfTenants ?? [] } as T
      if (options.selfGateAfterEnd !== undefined && deletes > 0) {
        return options.selfGateAfterEnd.then(() => answered)
      }
      return Promise.resolve(answered)
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
    if (path === '/api/v1/system/auth/accounts') {
      return Promise.resolve({
        accounts: [PICKER_ACCOUNT],
        next_cursor: '',
      } as T)
    }
    if (path === '/api/v1/system/auth/accounts/acc-1') {
      return Promise.resolve({ account: PICKER_ACCOUNT } as T)
    }
    if (path.startsWith('/api/v1/system/tenants/') && path.endsWith('/impersonations')) {
      recordReads += 1
      recordSignal = request?.signal
      if (options.recordsGate !== undefined) {
        return options.recordsGate.then(() => ({ impersonations: [] }) as T)
      }
      return Promise.resolve({ impersonations: [] } as T)
    }
    if (path === '/api/v1/notifications/messages/unread-count') {
      unreadReads += 1
      unreadTenantIds.push(getCurrentTenant())
      unreadNames.push(getCurrentTenantName())
      return Promise.resolve({ unread: 0 } as T)
    }
    if (path === '/api/v1/notifications/messages') {
      messagesReads += 1
      return Promise.resolve({ messages: [], total: 0 } as T)
    }
    if (path === '/api/v1/notifications/types') {
      return Promise.resolve({ types: [] } as T)
    }
    if (path === '/api/v1/notifications/preferences') {
      return Promise.resolve({ preferences: [] } as T)
    }
    return Promise.reject(
      new Error(`the suite does not answer ${method} ${path}`),
    )
  }
  return {
    fn,
    reads: () => reads,
    rosterReads: () => rosterReads,
    selfReads: () => selfReads,
    statusReads: () => statusReads,
    statusReadsWithGrant: () => statusReadsWithGrant,
    recordReads: () => recordReads,
    recordsAborted: () => recordSignal?.aborted ?? false,
    postReads: () => postReads,
    unreadReads: () => unreadReads,
    unreadTenantIds: () => unreadTenantIds,
    unreadNames: () => unreadNames,
    messagesReads: () => messagesReads,
    deletes: () => deletes,
    tenantReads: () => tenantReads,
  }
}

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
      tenants.push(getCurrentTenant())
      return {
        close: () => {
          record.closed += 1
        },
      }
    },
  }) as SpeedClient
  return { client, opened, tenants }
}

function banner(): HTMLElement | null {
  return screen.queryByRole('region', {
    name: tAuth('impersonation.regionLabel'),
  })
}

async function openAccountMenu(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  await user.click(screen.getByTestId('user-menu-trigger'))
  return screen.findByRole('menu')
}

async function closeAccountMenu(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
}

function organizationEntry(
  menu: HTMLElement,
  currentName: string,
): HTMLElement | null {
  return (
    within(menu).queryByRole('menuitem', { name: currentName }) ??
    within(menu).queryByText(currentName) ??
    within(menu).queryByText(demoEnUS.userMenu.organizations)
  )
}

const TENANT_ID = 'acme'

const PICKER_ACCOUNT = {
  id: 'acc-1',
  kind: 'user',
  status: 'active',
  display_name: 'Target One',
  mfa_required: false,
  protected: false,
  password_set: true,
  attributes: {},
  identifiers: [
    {
      kind: 'username',
      value: 'target-one',
      proven: false,
      sign_in: true,
      contact: false,
      default_contact: false,
    },
  ],
  created_at: '2026-10-15T12:00:00Z',
} as const

async function pickStartAccount(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  const picker = screen.getByRole('combobox', {
    name: tAdmin('impersonations.start.accountLabel'),
  })
  await user.click(picker)
  await user.type(picker, PICKER_ACCOUNT.display_name)
  await user.click(
    await screen.findByRole('option', {
      name: `${PICKER_ACCOUNT.display_name} (target-one)`,
    }),
  )
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenant(TENANT_ID)
  setTenantNames(new Map([[TENANT_ID, TENANT_ID]]))
  clearSelfTenants()
})

describe('the impersonation shell', () => {
  it('lands a start on the tenant home with the banner, the status read and no admin entry', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
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

    await screen.findByText(tAdmin('impersonations.current.none'))
    await pickStartAccount(user)
    await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')

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

    const menu = await openAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
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

    await user.click(screen.getByTestId('account-entry-trigger'))
    await user.click(
      screen.getByRole('menuitem', { name: demoEnUS.userMenu.messages }),
    )

    await waitFor(() => expect(window.location.pathname).toBe('/notifications'))
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
    const user = userEvent.setup()

    renderDemoApp({
      session: scriptedSession(targetSnapshot(), () => targetSnapshot()),
    })

    await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await waitFor(() =>
      expect(backend.statusReadsWithGrant()).toBeGreaterThan(0),
    )

    const menu = await openAccountMenu(user)
    expect(organizationEntry(menu, 'acme')).toBeNull()
    await closeAccountMenu(user)
    const menus = screen
      .getAllByRole('button')
      .filter((button) => button.getAttribute('aria-haspopup') === 'menu')
    expect(menus).toHaveLength(2)
    expect(menus).toEqual(
      expect.arrayContaining([
        screen.getByTestId('user-menu-trigger'),
        screen.getByTestId('account-entry-trigger'),
      ]),
    )
    expect(screen.getByTestId('user-menu-trigger')).toHaveAccessibleName(
      accountTriggerName('Target One'),
    )
    expect(screen.getByTestId('account-entry-trigger')).toHaveAccessibleName(
      accountTriggerName('Target One'),
    )
  })

  it('remounts the tenant switcher once the impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'acme' }],
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    const menu = await openAccountMenu(user)
    expect(organizationEntry(menu, 'acme')).toBeNull()
    await closeAccountMenu(user)
    const rosterBefore = backend.rosterReads()

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() =>
      expect(backend.rosterReads()).toBeGreaterThan(rosterBefore),
    )
    const reopened = await openAccountMenu(user)
    await waitFor(() =>
      expect(organizationEntry(reopened, 'acme')).not.toBeNull(),
    )
    expect(getCurrentTenant()).toBe('acme')
  })

  it('keeps the switcher out of the bar while the caller tenant read settles after an impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    const self = deferred()
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'acme' }],
      selfGateAfterEnd: self.promise,
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
    const menu = await openAccountMenu(user)
    expect(organizationEntry(menu, 'acme')).toBeNull()
    await closeAccountMenu(user)

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() => expect(backend.selfReads()).toBe(2))
    const gapMenu = await openAccountMenu(user)
    expect(organizationEntry(gapMenu, 'acme')).toBeNull()
    expect(
      within(gapMenu).queryByText(demoEnUS.userMenu.organizations),
    ).toBeNull()

    await act(async () => {
      self.resolve()
    })
    await waitFor(() =>
      expect(organizationEntry(gapMenu, 'acme')).not.toBeNull(),
    )
    expect(getCurrentTenant()).toBe('acme')
  })

  it('keeps the switcher out of the bar until the caller tenant read settles after an impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    const roster = deferred()
    const self = deferred()
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'acme' }],
      rosterGate: roster.promise,
      selfGateAfterEnd: self.promise,
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
    await waitFor(() => expect(backend.selfReads()).toBe(1))

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() => expect(backend.rosterReads()).toBe(1))
    await waitFor(() => expect(backend.selfReads()).toBe(2))

    const menu = await openAccountMenu(user)
    await act(async () => {
      roster.resolve()
    })
    expect(organizationEntry(menu, 'acme')).toBeNull()
    expect(
      within(menu).queryByText(demoEnUS.userMenu.organizations),
    ).toBeNull()

    await act(async () => {
      self.resolve()
    })
    await waitFor(() =>
      expect(organizationEntry(menu, 'acme')).not.toBeNull(),
    )
    expect(getCurrentTenant()).toBe('acme')
  })

  it('keeps the no-current-tenant state for a viewer whose caller tenant list settles empty', async () => {
    window.history.replaceState({}, '', '/')
    setCurrentTenant(null)
    const backend = makeBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => expect(backend.selfReads()).toBeGreaterThan(0))
    expect(backend.rosterReads()).toBe(0)
    const menu = await openAccountMenu(user)
    expect(
      organizationEntry(menu, demoEnUS.userMenu.organizations),
    ).not.toBeNull()
  })

  it('leaves the top bar unchanged for a member who is not impersonating', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      selfTenants: [{ name: 'acme' }],
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => expect(backend.selfReads()).toBeGreaterThan(0))
    const menu = await openAccountMenu(user)
    await waitFor(() =>
      expect(organizationEntry(menu, 'acme')).not.toBeNull(),
    )
    expect(banner()).toBeNull()
    expect(backend.statusReads()).toBe(0)
    expect(backend.rosterReads()).toBe(0)
  })

  it('ends from the banner, re-reads the identity and re-registers the stream', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'acme' }],
    })
    bindRequestFn(backend.fn)
    const { client, opened } = streamClient()
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()

    renderDemoApp({ session, api: client })

    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await waitFor(() => expect(opened.length).toBeGreaterThan(0))
    const openedBefore = opened.length
    const liveBefore = opened[openedBefore - 1]
    const rosterBefore = backend.rosterReads()

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )

    await waitFor(() => expect(banner()).toBeNull())
    expect(
      await screen.findByText(tAuth('impersonation.ended')),
    ).toBeInTheDocument()

    await waitFor(() =>
      expect(screen.getByTestId('user-menu-trigger')).toHaveAccessibleName(
        accountTriggerName('Staff One'),
      ),
    )
    expect(screen.getByTestId('account-entry-trigger')).toHaveAccessibleName(
      accountTriggerName('Staff One'),
    )
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(rosterBefore))

    expect(window.location.pathname).toBe('/')

    await waitFor(() => expect(opened.length).toBeGreaterThan(openedBefore))
    expect(liveBefore?.closed).toBe(1)
  })

  it('shows the banner for a new impersonation after an end settles normally', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
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
    await waitFor(() =>
      expect(screen.getByTestId('user-menu-trigger')).toHaveAccessibleName(
        accountTriggerName('Staff One'),
      ),
    )

    act(() => {
      session.set(targetSnapshot())
    })
    const again = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    expect(again).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )
    await waitFor(() =>
      expect(announcementRegion()).toHaveTextContent(
        tAuth('impersonation.actingAs', { subject: 'Target One' }),
      ),
    )
    expect(announcementRegion()).not.toHaveTextContent(
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

    act(() => {
      session.set(operatorSnapshot())
    })
    await waitFor(() => expect(banner()).toBeNull())

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

  it('shows no finished impersonation while the admin page\'s status read is pending', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    const statusGate = deferred()
    let holdStatus = false
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({
          platform: ['tenant.read', 'authn.impersonation.read'],
          tenant: [],
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
    await waitFor(() =>
      expect(screen.getByTestId('user-menu-trigger')).toHaveAccessibleName(
        accountTriggerName('Staff One'),
      ),
    )

    holdStatus = true
    act(() => {
      window.history.pushState({}, '', '/admin/impersonations?tenant=acme')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })

    expect(
      await screen.findByText(tAdmin('impersonations.current.title')),
    ).toBeInTheDocument()
    expect(
      screen.getByText(tAdmin('impersonations.current.loading')),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(tAdmin('impersonations.current.target'), {
        exact: false,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: tAdmin('impersonations.current.end'),
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
    expect(
      await within(notice).findByText(
        tAuth('impersonation.tenant', { tenant: 'acme' }),
      ),
    ).toBeInTheDocument()
    expect(backend.rosterReads()).toBe(0)
    expect(backend.statusReads()).toBeGreaterThan(0)
  })

  it('keeps the grant tenant named on a full page load while impersonating', async () => {
    window.history.replaceState({}, '', '/')
    setCurrentTenant(null)
    setTenantNames(new Map())
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'acme' }],
    })
    bindRequestFn(backend.fn)

    renderDemoApp({
      session: scriptedSession(targetSnapshot(), () => targetSnapshot()),
    })

    await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })

    await waitFor(() => expect(backend.selfReads()).toBeGreaterThan(0))

    await waitFor(() => expect(backend.unreadReads()).toBeGreaterThan(0))
    expect(backend.unreadTenantIds().every((id) => id === 'acme')).toBe(true)
    expect(backend.unreadNames().every((name) => name === 'acme')).toBe(true)
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
    await waitFor(() => expect(backend.rosterReads()).toBeGreaterThan(0))

    expect(banner()).toBeNull()
    expect(backend.statusReads()).toBe(0)
  })

  it('runs the ended path when the status read answers a bare 404', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend()
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())

    renderDemoApp({ session })

    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() =>
      expect(screen.getByTestId('user-menu-trigger')).toHaveAccessibleName(
        accountTriggerName('Staff One'),
      ),
    )
    expect(window.location.pathname).toBe('/')
  })

  it('expires: the identity change runs at the recorded time and the banner goes within the check-ttl bound', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const endsAt = new Date(Date.now() + 30_000).toISOString()
      const backend = makeBackend({ grant: grantFixture(endsAt) })
      bindRequestFn(backend.fn)
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
      await act(async () => {
        await Promise.resolve()
      })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(31_000)
      })
      expect(restores).toBe(1)
      expect(banner()).not.toBeNull()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(6_000)
      })
      await waitFor(() => expect(banner()).toBeNull())
      expect(restores).toBe(3)

      expect(opened.length).toBeGreaterThanOrEqual(3)
      expect(opened[0]?.closed).toBe(1)
      expect(opened[1]?.closed).toBe(1)
    } finally {
      await restoreRealTimers()
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

    expect(
      await screen.findByText(tAuth('impersonation.ended')),
    ).toBeInTheDocument()
    await waitFor(() => expect(banner()).toBeNull())
    expect(backend.deletes()).toBe(1)
    await waitFor(() =>
      expect(screen.getByTestId('user-menu-trigger')).toHaveAccessibleName(
        accountTriggerName('Staff One'),
      ),
    )
  })

  it('withholds the banner as soon as the end settles, before the session re-read', async () => {
    window.history.replaceState({}, '', '/')
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

    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    await waitFor(() => expect(banner()).toBeNull())
    expect(
      await screen.findByText(tAuth('impersonation.ended')),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: tAuth('impersonation.end') }),
    ).toBeNull()

    await gated.releaseRestore()
    await waitFor(() => expect(gated.pendingRestores()).toBe(0))
    expect(banner()).toBeNull()
    expect(
      await screen.findByText(tAuth('impersonation.ended')),
    ).toBeInTheDocument()
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

      await act(async () => {
        await vi.advanceTimersByTimeAsync(55_000)
      })
      expect(banner()).toBeNull()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(10_000)
      })
      const restored = banner()
      expect(restored).not.toBeNull()

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
      await restoreRealTimers()
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
      await restoreRealTimers()
    }
  })

  it('cancels the read in flight and holds new reads until a delayed start settles', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
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
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })

      const renderResult = renderDemoApp({ session: gated.session })

      await screen.findByText(tAdmin('impersonations.current.none'))
      await waitFor(() => expect(backend.recordReads()).toBe(1))
      await pickStartAccount(user)

      await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))
      await waitFor(() => expect(backend.postReads()).toBe(1))
      await waitFor(() => expect(backend.recordsAborted()).toBe(true))
      expect(onlineManager.isOnline()).toBe(false)

      const readsWhileHeld = backend.reads()
      await act(async () => {
        post.resolve()
      })
      await waitFor(() => expect(gated.pendingRestores()).toBe(1))
      await waitFor(() =>
        expect(
          renderResult.queryClient.getQueryState(
            getAuthnImpersonationStatusQueryKey(),
          )?.fetchStatus,
        ).toBe('paused'),
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000)
      })
      expect(backend.reads()).toBe(readsWhileHeld)

      await gated.releaseRestore()
      await waitFor(() => expect(window.location.pathname).toBe('/'))
      expect(onlineManager.isOnline()).toBe(true)
      await waitFor(() => expect(backend.unreadReads()).toBeGreaterThan(0))
    } finally {
      await restoreRealTimers()
    }
  })

  it('releases the hold when the start is refused: no identity change follows', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    const backend = makeBackend({ startRefused: true })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const transitions = transitionTrackingSession(operatorSnapshot())

    renderDemoApp({ session: transitions.session })

    await screen.findByText(tAdmin('impersonations.current.none'))
    await pickStartAccount(user)
    await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))
    await waitFor(() => expect(backend.postReads()).toBe(1))

    await waitFor(() => expect(onlineManager.isOnline()).toBe(true))
    expect(transitions.opens()).toBe(1)
    expect(transitions.releases()).toBe(1)
    const readsBefore = backend.reads()
    await user.click(screen.getByRole('button', { name: tAdmin('common.refresh') }))
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
    await waitFor(() => expect(backend.selfReads()).toBeGreaterThan(0))

    await user.click(await screen.findByTestId('account-entry-trigger'))
    await user.click(
      await screen.findByRole('menuitem', {
        name: demoEnUS.userMenu.messages,
      }),
    )

    await waitFor(() => expect(window.location.pathname).toBe('/notifications'))
    await waitFor(() => expect(backend.messagesReads()).toBeGreaterThan(0))
    expect(onlineManager.isOnline()).toBe(true)
  })

  it('runs the status read\'s bare 404 once per impersonation interval and bounds its re-read', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
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
      expect(restores).toBe(1)
      expect(banner()).not.toBeNull()

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60_000)
      })
      expect(restores).toBeGreaterThan(1)
      expect(restores).toBeLessThanOrEqual(13)
      expect(backend.statusReads()).toBe(1)

      const settled = restores
      await act(async () => {
        await vi.advanceTimersByTimeAsync(120_000)
      })
      expect(restores).toBe(settled)
      expect(backend.statusReads()).toBe(1)
      const notice = banner()
      expect(notice).not.toBeNull()
      expect(
        within(notice!).getByRole('button', {
          name: tAuth('impersonation.end'),
        }),
      ).toBeInTheDocument()
    } finally {
      await restoreRealTimers()
    }
  })

  it('stops the expiry re-read at the check-ttl upper bound', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const endsAt = new Date(Date.now() + 30_000).toISOString()
      const backend = makeBackend({ grant: grantFixture(endsAt) })
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
      await restoreRealTimers()
    }
  })

  it('bounds the re-read when expiry answers 404 while the session read still impersonates', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
      const endsAt = new Date(Date.now() + 30_000).toISOString()
      const backend = makeBackend({
        grant: grantFixture(endsAt),
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
      expect(backend.statusReads()).toBe(1)

      await act(async () => {
        window.dispatchEvent(new Event('visibilitychange'))
        await Promise.resolve()
      })
      await act(async () => {
        await vi.advanceTimersByTimeAsync(70_000)
      })

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
      await restoreRealTimers()
    }
  })

  it('does not restart the expiry path when the route changes', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/')
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

      await user.click(await screen.findByTestId('account-entry-trigger'))
      await user.click(
        await screen.findByRole('menuitem', {
          name: demoEnUS.userMenu.messages,
        }),
      )
      await waitFor(() =>
        expect(window.location.pathname).toBe('/notifications'),
      )
      await act(async () => {
        await vi.advanceTimersByTimeAsync(0)
      })

      expect(restores).toBe(1)
      expect(backend.statusReads()).toBe(statusReads)
    } finally {
      await restoreRealTimers()
    }
  })

  it('keeps an impersonating identity out of the admin even with a non-empty platform set', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const backend = makeBackend()
    bindRequestFn(backend.fn)
    const impersonated = {
      ...authenticatedSnapshot({ system: ['tenant.read'] }),
      impersonating: true,
    }
    renderDemoApp({
      session: scriptedSession(impersonated, () => impersonated),
    })

    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).toBeNull()
    expect(screen.queryByText(demoEnUS.admin.accessFailed)).toBeNull()
  })

  it('sends an impersonating identity with an empty platform set to the tenant home', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const backend = makeBackend()
    bindRequestFn(backend.fn)
    const impersonated = {
      ...authenticatedSnapshot({ system: [] }),
      impersonating: true,
    }
    renderDemoApp({
      session: scriptedSession(impersonated, () => impersonated),
    })

    await waitFor(() => expect(window.location.pathname).toBe('/'))
  })

  it('settles the admin tenant from the caller list when the roster is refused', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    setCurrentTenant(null)
    setTenantNames(new Map())
    const request: RequestFn = <T,>(
      path: string,
    ) => {
      if (path === '/api/v1/system/tenants') {
        return Promise.reject(new Error('the roster is refused to an impersonation')) as Promise<T>
      }
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ name: 'acme', kind: 'business' }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({ platform: [], tenant: [] } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    }
    bindRequestFn(request)
    const impersonated = {
      ...authenticatedSnapshot({ system: null }),
      impersonating: true,
    }
    const base = createTestSession(impersonated)
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

    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeNull()
  })

  it('resets the platform set to unknown immediately before re-reading it after an impersonation ends', async () => {
    window.history.replaceState({}, '', '/')
    setCurrentTenant(null)
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'system', kind: 'business' }],
    })
    let impersonating = true
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/authz/me/permissions') {
        return impersonating
          ? Promise.reject({ status: 403, code: 'rbac.permission_denied' })
          : Promise.resolve({
              platform: ['tenant.read'],
              tenant: [],
            } as T)
      }
      const answer = backend.fn<T>(path, options)
      if (path === '/api/v1/auth/impersonation' && options?.method === 'DELETE') {
        impersonating = false
      }
      return answer
    }
    bindRequestFn(request)
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
    await waitFor(() => expect(attached).toEqual([null]))
    const menu = await openAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
    await closeAccountMenu(user)

    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )
    await waitFor(() => expect(banner()).toBeNull())
    await waitFor(() =>
      expect(session.getSnapshot().permissionSets.system).toEqual([
        'tenant.read',
      ]),
    )

    expect(attached).toEqual([null, null, null, ['tenant.read']])
    const settledMenu = await openAccountMenu(user)
    expect(
      within(settledMenu).getByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeInTheDocument()
  })

  it('keeps the settled permission set when the start\'s session re-read hangs', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    const backend = makeBackend()
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({
          platform: ['tenant.read', 'authn.impersonation.read'],
          tenant: [],
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

    await screen.findByText(tAdmin('impersonations.current.none'))
    await pickStartAccount(user)
    await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))

    await waitFor(() => expect(gated.pendingRestores()).toBe(1))
    expect(window.location.pathname).toBe('/admin/impersonations')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeNull()
    expect(
      screen.getByText(tAdmin('impersonations.current.title')),
    ).toBeInTheDocument()

    await gated.releaseRestore()
    await waitFor(() => expect(window.location.pathname).toBe('/'))
  })

  it('keeps the settled permission set when the start\'s session re-read is refused', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    const backend = makeBackend()
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({
          platform: ['tenant.read', 'authn.impersonation.read'],
          tenant: [],
        } as T)
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    const base = createTestSession(operatorSnapshot())
    const restoreOutcomes: ('settled' | 'rejected')[] = []
    const session = {
      ...restoreOutcomeSession(base, restoreOutcomes),
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

    await screen.findByText(tAdmin('impersonations.current.none'))
    await pickStartAccount(user)
    await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))
    await waitFor(() => expect(backend.postReads()).toBe(1))

    await waitFor(() => expect(restoreOutcomes).toEqual(['rejected']))
    expect(window.location.pathname).toBe('/admin/impersonations')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeNull()
    expect(session.getSnapshot().permissionSets.system).toEqual([
      'tenant.read',
      'authn.impersonation.read',
    ])
  })

  it('keeps the settled permission set when an end changes no identity', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
    })
    const request: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({
          platform: ['tenant.read', 'authn.impersonation.read'],
          tenant: [],
        } as T)
      }
      return backend.fn<T>(path, options)
    }
    bindRequestFn(request)
    const base = scriptedSession(operatorSnapshot(), () => ({
      ...operatorSnapshot(),
      permissionSets: base.getSnapshot().permissionSets,
    }))
    const restoreOutcomes: ('settled' | 'rejected')[] = []
    const session = {
      ...restoreOutcomeSession(base, restoreOutcomes),
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

    const end = await screen.findByRole('button', {
      name: tAdmin('impersonations.current.end'),
    })
    await user.click(end)
    await waitFor(() => expect(backend.deletes()).toBe(1))

    await waitFor(() => expect(restoreOutcomes).toEqual(['settled']))
    expect(window.location.pathname).toBe('/admin/impersonations')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeNull()
    expect(session.getSnapshot().permissionSets.system).toEqual([
      'tenant.read',
      'authn.impersonation.read',
    ])
  })

  it('starts a newly mounted frame from an unknown tenant during an impersonation', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
      setCurrentTenant('acme')
      setTenantNames(
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
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })

      const renderResult = renderDemoApp({ session, api: client })

      await screen.findByText(tAdmin('impersonations.current.none'))
      await pickStartAccount(user)
      const tenantBefore = backend.tenantReads()
      const openedBefore = opened.length
      await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))

      await waitFor(() => expect(window.location.pathname).toBe('/'))
      await waitFor(() =>
        expect(
          renderResult.queryClient.getQueryState(
            getAuthnImpersonationStatusQueryKey(),
          )?.fetchStatus,
        ).toBe('fetching'),
      )
      expect(getCurrentTenant()).toBeNull()
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000)
      })
      expect(getCurrentTenant()).toBeNull()
      expect(backend.tenantReads()).toBe(tenantBefore)
      expect(opened.length).toBe(openedBefore)

      await act(async () => {
        status.resolve()
      })
      await waitFor(() => expect(backend.unreadReads()).toBeGreaterThan(0))
      expect(backend.unreadTenantIds().every((id) => id === 'beta')).toBe(true)
      await waitFor(() => expect(opened.length).toBeGreaterThan(openedBefore))
      expect(tenants.at(-1)).toBe('beta')
    } finally {
      await restoreRealTimers()
    }
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
      tAuth('impersonation.actingAs', {
        subject: tAuth('impersonation.accountUnavailable'),
      }),
    )
    expect(notice).not.toHaveTextContent('acc-1')
  })

  it('starts without a blanking window: the banner appears when the re-read settles', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
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

    await screen.findByText(tAdmin('impersonations.current.none'))
    await pickStartAccount(user)
    await user.click(screen.getByRole('button', { name: tAdmin('impersonations.start.submit') }))

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

    expect(banner()).toBeNull()
    expect(
      document.querySelector(
        `section[aria-label="${tAuth('impersonation.regionLabel')}"]`,
      ),
    ).toBeNull()
    expect(announcementRegion()).toHaveTextContent(
      tAuth('impersonation.actingAs', { subject: 'Target One' }),
    )

    const chip = screen.getByRole('button', {
      name: tAuth('impersonation.impersonating'),
    })
    expect(chip).toHaveAttribute('aria-expanded', 'false')
    expect(chip).toHaveFocus()

    await user.click(screen.getByTestId('account-entry-trigger'))
    await user.click(
      screen.getByRole('menuitem', { name: tAuth('impersonation.end') }),
    )

    await waitFor(() => expect(banner()).toBeNull())
    expect(
      screen.queryByRole('button', {
        name: tAuth('impersonation.impersonating'),
      }),
    ).toBeNull()
    const endedText = await screen.findByText(tAuth('impersonation.ended'))
    expect(endedText.closest('[role="status"]')).toHaveTextContent(
      tAuth('impersonation.ended'),
    )
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

    await user.click(screen.getByTestId('account-entry-trigger'))
    await user.click(
      screen.getByRole('menuitem', { name: tAuth('impersonation.end') }),
    )

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

    await user.click(screen.getByTestId('account-entry-trigger'))
    await user.click(
      screen.getByRole('menuitem', {
        name: demoEnUS.account.sections.profile,
      }),
    )
    await waitFor(() =>
      expect(window.location.pathname).toBe('/account/profile'),
    )
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
    await waitFor(() =>
      expect(announcementRegion()).toHaveTextContent(
        tAuth('impersonation.actingAs', { subject: 'Target Two' }),
      ),
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
    const header = screen.getAllByRole('banner')[0]
    if (header === undefined) {
      throw new Error('expected the shell banner landmark')
    }
    expect(
      header.compareDocumentPosition(notice) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(notice.parentElement?.parentElement).toBe(header.parentElement)

    await user.tab()
    expect(
      screen.getByRole('link', { name: tLayout('appShell.skipToContent') }),
    ).toHaveFocus()

    const chip = screen.getByRole('button', {
      name: tAuth('impersonation.impersonating'),
    })
    chip.focus()
    await user.tab()
    expect(screen.getByTestId('user-menu-trigger')).toHaveFocus()
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

describe('the admin under an impersonation', () => {
  function attachable(base: ReturnType<typeof createTestSession>): AuthSession {
    return {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    }
  }

  function bindWithPermissions(
    answer: () => Promise<unknown>,
    grant: string = new Date(Date.now() + 3_600_000).toISOString(),
  ): ReturnType<typeof makeBackend> {
    const backend = makeBackend({ grant: grantFixture(grant) })
    bindRequestFn(<T,>(path: string, options?: RequestOptions): Promise<T> => {
      if (path === '/api/v1/authz/me/permissions') {
        return answer() as Promise<T>
      }
      return backend.fn<T>(path, options)
    })
    return backend
  }

  it.each([
    [
      'a refused read',
      (): Promise<unknown> =>
        Promise.reject({ status: 403, code: 'rbac.permission_denied' }),
    ],
    [
      'an answered empty document',
      (): Promise<unknown> => Promise.resolve({ platform: [], tenant: [] }),
    ],
  ])(
    'keeps an admin URL unreachable under %s, with no failure view',
    async (_label, answer) => {
      window.history.replaceState({}, '', '/admin/tenants')
      bindWithPermissions(answer)
      const session = attachable(
        createTestSession({
          ...targetSnapshot(),
          permissionSets: { tenant: null, system: null },
        }),
      )
      renderDemoApp({
        session,
        wrap: (app) => (
          <PlatformPermissions session={session}>{app}</PlatformPermissions>
        ),
      })

      await waitFor(() => expect(window.location.pathname).toBe('/'))
      expect(screen.queryByText(demoEnUS.admin.accessFailed)).toBeNull()
      expect(
        screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
      ).toBeNull()
      expect(
        await screen.findByRole('region', {
          name: tAuth('impersonation.regionLabel'),
        }),
      ).toBeInTheDocument()
    },
  )

  it('hides the entry and keeps an in-app admin move home even for a non-empty read', async () => {
    window.history.replaceState({}, '', '/')
    bindWithPermissions(() =>
      Promise.resolve({ platform: ['tenant.read'], tenant: [] }),
    )
    const session = attachable(createTestSession(targetSnapshot()))
    const user = userEvent.setup()
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    const menu = await openAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
    await closeAccountMenu(user)

    act(() => {
      window.history.pushState({}, '', '/admin/tenants')
      window.dispatchEvent(new PopStateEvent('popstate'))
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(screen.queryByText(demoEnUS.admin.accessFailed)).toBeNull()
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).toBeNull()
  })

  it('hides the entry under an impersonation even in the system organization', async () => {
    window.history.replaceState({}, '', '/')
    const backend = makeBackend({
      grant: {
        ...grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
        tenant: 'system',
      },
      selfTenants: [{ name: 'system', kind: 'business' }],
    })
    bindRequestFn(<T,>(path: string, options?: RequestOptions): Promise<T> => {
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({ platform: ['tenant.read'], tenant: [] }) as Promise<T>
      }
      return backend.fn<T>(path, options)
    })
    const session = attachable(createTestSession(targetSnapshot()))
    const user = userEvent.setup()
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    const menu = await openAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
  })

  it('lets the admin back in once the impersonation ends', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const backend = makeBackend({
      grant: grantFixture(new Date(Date.now() + 3_600_000).toISOString()),
      selfTenants: [{ name: 'system', kind: 'business' }],
    })
    bindRequestFn(backend.fn)
    const session = scriptedSession(targetSnapshot(), () => operatorSnapshot())
    const user = userEvent.setup()
    renderDemoApp({ session })

    await waitFor(() => expect(window.location.pathname).toBe('/'))
    const notice = await screen.findByRole('region', {
      name: tAuth('impersonation.regionLabel'),
    })
    await user.click(
      within(notice).getByRole('button', { name: tAuth('impersonation.end') }),
    )
    await waitFor(() => expect(banner()).toBeNull())

    const menu = await openAccountMenu(user)
    await user.click(
      within(menu).getByRole('menuitem', { name: demoEnUS.nav.admin }),
    )
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
  })
})
