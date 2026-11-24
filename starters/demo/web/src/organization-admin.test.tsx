import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn } from '@speed/api-client'
import type { AuthzTenantAdminRoleView } from './app-api/index.js'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
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
  readyFeatures,
} from './test-utils/session.js'
import type { TestSession } from './test-utils/session.js'

const SELF_TENANTS_PATH = '/api/v1/auth/me/tenants'
const PERMISSIONS_PATH = '/api/v1/authz/me/permissions'

const tAdmin = createSpeedI18n({
  supportedLanguages: ['en-US', 'zh-CN'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [{ namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources }],
}).getFixedT('en-US', ADMIN_UI_NAMESPACE)

const ORGANIZATION_FEATURES = readyFeatures(
  'authn.admin',
  'authn.invitations',
  'authn.admin.sso',
  'authz.tenantadmin',
  'settings.tenantadmin',
  'tenant.profile',
)

interface TenantRow {
  readonly id: string
  readonly name: string
  readonly kind: 'business' | 'personal'
}

interface PermissionAnswer {
  readonly platform: string[]
  readonly tenant: string[]
}

type TenantRoleRow = AuthzTenantAdminRoleView

function tenantRole(role: string): TenantRoleRow {
  return {
    role,
    builtin: false,
    tenant: 'acme',
    can_manage: true,
    description: '',
    member_count: null,
    permission_count: 0,
    created_at: '2026-10-01T00:00:00Z',
  }
}

type PermissionsFor = (
  name: string | null,
) => PermissionAnswer | Promise<PermissionAnswer>

function scriptReads(options: {
  readonly tenants: readonly TenantRow[]
  readonly tenantsFailed?: boolean
  readonly tenantsRead?: () => Promise<{ readonly tenants: readonly TenantRow[] }>
  readonly permissionsFor: PermissionsFor
  readonly roles?: readonly TenantRoleRow[]
  readonly impersonationGrant?: boolean
}): { readonly requested: string[] } {
  const requested: string[] = []
  const request: RequestFn = <T,>(path: string): Promise<T> => {
    requested.push(path)
    if (path === SELF_TENANTS_PATH) {
      if (options.tenantsRead !== undefined) {
        return options.tenantsRead() as Promise<T>
      }
      return options.tenantsFailed === true
        ? Promise.reject(new Error('tenant read refused'))
        : Promise.resolve({ tenants: options.tenants } as T)
    }
    if (path === PERMISSIONS_PATH) {
      return Promise.resolve(
        options.permissionsFor(getCurrentTenantName()),
      ).then((answer) => answer as T)
    }
    if (path === '/api/v1/auth/impersonation') {
      return options.impersonationGrant === true
        ? Promise.resolve({
            grant: {
              id: 'grant-1',
              target: 'account-2',
              tenant: 'acme',
              started_at: '2026-10-15T12:00:00Z',
              ends_at: new Date(Date.now() + 60_000).toISOString(),
            },
          } as T)
        : Promise.reject(new Error('no impersonation'))
    }
    if (path.endsWith('/invitations')) {
      return Promise.resolve({ invitations: [], next_cursor: '' } as T)
    }
    if (path.endsWith('/members')) {
      return Promise.resolve({ members: [], next_cursor: '' } as T)
    }
    if (path.endsWith('/sso/providers')) {
      return Promise.resolve({ providers: [], next_cursor: '' } as T)
    }
    if (path.endsWith('/grants')) {
      return Promise.resolve({ grants: [] } as T)
    }
    if (path.endsWith('/roles')) {
      return Promise.resolve({ roles: options.roles ?? [] } as T)
    }
    if (path.endsWith('/permissions')) {
      return Promise.resolve({ permissions: [] } as T)
    }
    if (path.endsWith('/bindings')) {
      return Promise.resolve({ bindings: [] } as T)
    }
    if (path.endsWith('/settings')) {
      return Promise.resolve({
        settings: [],
        writable: true,
        read_only_reason: '',
      } as T)
    }
    if (path.endsWith('/profile')) {
      return Promise.resolve({
        profile: {
          id: 'acme',
          name: 'acme',
          display_name: '',
          description: '',
          contact_email: '',
          website: '',
          owner_account: '',
          owner_display_name: '',
          owner_username: '',
          status: 'active',
          created_at: '2026-01-01T00:00:00Z',
        },
      } as T)
    }
    return Promise.reject(new Error(`unexpected request: ${path}`))
  }
  bindRequestFn(request)
  return { requested }
}

async function flushReads(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

function mountApp(options: {
  readonly path?: string
  readonly tenants: readonly TenantRow[]
  readonly tenantsFailed?: boolean
  readonly tenantsRead?: () => Promise<{ readonly tenants: readonly TenantRow[] }>
  readonly permissionsFor: PermissionsFor
  readonly roles?: readonly TenantRoleRow[]
  readonly impersonating?: boolean
  readonly impersonationGrant?: boolean
  readonly anonymous?: boolean
  readonly features?: ReturnType<typeof authenticatedSnapshot>['features']
  readonly featureRetry?: () => Promise<
    ReturnType<typeof authenticatedSnapshot>['features']
  >
}): { readonly requested: string[]; readonly session: TestSession } {
  const target = options.path ?? '/'
  window.history.replaceState({}, '', target)
  const { requested } = scriptReads(options)
  const snapshot = authenticatedSnapshot({
    features: options.features ?? ORGANIZATION_FEATURES,
  })
  const session =
    options.anonymous === true
      ? createTestSession()
      : createTestSession(
          options.impersonating === true
            ? { ...snapshot, impersonating: true }
            : snapshot,
          undefined,
          options.featureRetry,
        )
  renderDemoApp({
    session,
    wrap: (app) => (
      <PlatformPermissions session={session}>{app}</PlatformPermissions>
    ),
  })
  return { requested, session }
}

async function accountMenuOrganization(
  user: ReturnType<typeof userEvent.setup>,
  triggerId: string,
  waitForGroup: boolean,
): Promise<{
  readonly group: HTMLElement | null
  readonly names: readonly string[]
  readonly firstHref: string | null
}> {
  await user.click(screen.getByTestId(triggerId))
  const menu = await screen.findByRole('menu')
  const group = waitForGroup
    ? await within(menu).findByRole('group', {
        name: demoEnUS.nav.organizationAdmin,
      })
    : within(menu).queryByRole('group', {
        name: demoEnUS.nav.organizationAdmin,
      })
  const entries = group === null ? [] : within(group).getAllByRole('menuitem')
  const names = entries.map((entry) => entry.textContent ?? '')
  const firstHref = entries[0]?.getAttribute('href') ?? null
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
  )
  return { group, names, firstHref }
}

async function switchOrganization(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<void> {
  await user.click(screen.getByTestId('user-menu-trigger'))
  const menu = await screen.findByRole('menu')
  await user.click(
    within(menu).getByRole('menuitem', {
      name: getCurrentTenantName() ?? '',
    }),
  )
  await user.click(await screen.findByRole('menuitem', { name }))
}

async function organizationEntry(
  user: ReturnType<typeof userEvent.setup>,
  options: {
    readonly waitForGroup?: boolean
  } = {},
): Promise<{
  readonly header: {
    readonly group: HTMLElement | null
    readonly names: readonly string[]
    readonly firstHref: string | null
  }
  readonly sidebar: {
    readonly group: HTMLElement | null
    readonly names: readonly string[]
    readonly firstHref: string | null
  }
  readonly card: HTMLElement | null
}> {
  const waitForGroup = options.waitForGroup === true
  const header = await accountMenuOrganization(
    user,
    'user-menu-trigger',
    waitForGroup,
  )
  const sidebar = await accountMenuOrganization(
    user,
    'account-entry-trigger',
    waitForGroup,
  )
  const card = within(screen.getByRole('main')).queryByText(
    demoEnUS.home.cards.organizationAdmin.title,
  )
  return { header, sidebar, card }
}

function expectOrganizationAbsent(
  entry: Awaited<ReturnType<typeof organizationEntry>>,
): void {
  expect(entry.header.group).toBeNull()
  expect(entry.sidebar.group).toBeNull()
  expect(entry.card).toBeNull()
}

const ORGANIZATION_ENTRY_LABELS = [
  tAdmin('organization.nav.profile'),
  tAdmin('organization.nav.invitations'),
  tAdmin('organization.nav.sso'),
  tAdmin('organization.nav.roles'),
  tAdmin('organization.nav.settings'),
] as const

const ALL_ORGANIZATION_PERMISSIONS = [
  'authn.membership.manage',
  'authn.idp.manage',
  'authz.tenant.role.read',
  'settings.tenant.read',
  'tenant.profile.read',
] as const

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenant('acme')
  setTenantNames(new Map([['acme', 'acme']]))
  clearSelfTenants()
})

describe('the organization admin entry', () => {
  it('appears in both account menus for a business tenant that grants the organization permissions', async () => {
    const user = userEvent.setup()
    mountApp({
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: [...ALL_ORGANIZATION_PERMISSIONS],
      }),
    })
    await screen.findByText(demoEnUS.home.heading)

    const entry = await organizationEntry(user, { waitForGroup: true })
    expect(entry.header.group).not.toBeNull()
    expect(entry.header.names).toEqual([...ORGANIZATION_ENTRY_LABELS])
    expect(
      within(entry.header.group as HTMLElement).queryByText(
        demoEnUS.nav.organizationAdmin,
      ),
    ).toBeNull()
    expect(entry.sidebar.names).toEqual([...ORGANIZATION_ENTRY_LABELS])
    expect(entry.card).toBeInTheDocument()

    const cardAnchor = entry.card?.closest('a')
    const firstHref = entry.header.firstHref
    expect(firstHref).toBe('/organization/admin/profile')
    expect(entry.sidebar.firstHref).toBe(firstHref)
    expect(cardAnchor?.getAttribute('href')).toBe(firstHref)

    const navigation = screen.getAllByRole('navigation')
    expect(navigation.length).toBeGreaterThan(0)
    for (const landmark of navigation) {
      expect(
        within(landmark).queryByText(demoEnUS.nav.organizationAdmin),
      ).toBeNull()
    }
  })

  it('does not appear for a personal tenant', async () => {
    const user = userEvent.setup()
    setCurrentTenant('personal-account-1')
    setTenantNames(new Map([['personal-account-1', 'my-space']]))
    mountApp({
      tenants: [
        {
          id: 'personal-account-1',
          name: 'my-space',
          kind: 'personal',
        },
      ],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    await screen.findByText(demoEnUS.home.heading)
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
  })

  it('does not appear for the system tenant', async () => {
    const user = userEvent.setup()
    setCurrentTenant('system')
    setTenantNames(new Map([['system', 'system']]))
    mountApp({
      tenants: [],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    await screen.findByText(demoEnUS.home.heading)
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
  })

  it('does not appear while the session impersonates another account', async () => {
    const user = userEvent.setup()
    mountApp({
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
      impersonating: true,
      impersonationGrant: true,
    })
    await screen.findByText(demoEnUS.home.heading)
    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
  })

  it('does not appear while the tenant permission read is pending', async () => {
    const user = userEvent.setup()
    const { requested } = mountApp({
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => new Promise<PermissionAnswer>(() => undefined),
    })
    await screen.findByText(demoEnUS.home.heading)
    await waitFor(() => expect(requested).toContain(PERMISSIONS_PATH))
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
  })

  it('hides the entry while the set still belongs to the tenant just left', async () => {
    const user = userEvent.setup()
    let settleGlobex: (value: PermissionAnswer) => void = () => undefined
    const { requested } = mountApp({
      tenants: [
        { id: 'acme', name: 'acme', kind: 'business' },
        { id: 'globex', name: 'globex', kind: 'business' },
      ],
      permissionsFor: (name) =>
        name === 'acme'
          ? { platform: [], tenant: ['authn.membership.manage'] }
          : new Promise<PermissionAnswer>((resolve) => {
              settleGlobex = resolve
            }),
    })
    await screen.findByText(demoEnUS.home.heading)
    const beforeSwitch = await organizationEntry(user, { waitForGroup: true })
    expect(beforeSwitch.header.names).toEqual([
      tAdmin('organization.nav.invitations'),
    ])
    expect(beforeSwitch.sidebar.names).toEqual([
      tAdmin('organization.nav.invitations'),
    ])

    await switchOrganization(user, 'globex')

    await waitFor(() => expect(getCurrentTenant()).toBe('globex'))
    const whileStale = await organizationEntry(user)
    expect(whileStale.header.group).toBeNull()
    expect(whileStale.sidebar.group).toBeNull()

    settleGlobex({ platform: [], tenant: ['authn.membership.manage'] })
    const afterSwitch = await organizationEntry(user, { waitForGroup: true })
    expect(afterSwitch.header.names).toEqual([
      tAdmin('organization.nav.invitations'),
    ])
    expect(afterSwitch.sidebar.names).toEqual([
      tAdmin('organization.nav.invitations'),
    ])
    expect(
      requested.filter((path) => path === PERMISSIONS_PATH),
    ).toHaveLength(2)
  })

  it('navigates to each organization page from the account menu', async () => {
    const user = userEvent.setup()
    mountApp({
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: [...ALL_ORGANIZATION_PERMISSIONS],
      }),
    })
    await screen.findByText(demoEnUS.home.heading)

    const destinations = [
      [tAdmin('organization.nav.profile'), '/organization/admin/profile'],
      [tAdmin('organization.nav.invitations'), '/organization/admin/invitations'],
      [tAdmin('organization.nav.sso'), '/organization/admin/sso'],
      [tAdmin('organization.nav.roles'), '/organization/admin/roles'],
      [tAdmin('organization.nav.settings'), '/organization/admin/settings'],
    ] as const
    for (const [label, path] of destinations) {
      await user.click(screen.getByTestId('user-menu-trigger'))
      await user.click(await screen.findByRole('menuitem', { name: label }))
      await waitFor(() => expect(window.location.pathname).toBe(path))
      await waitFor(() =>
        expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
      )
    }
  })

  it('renders each organization entry as an anchor to its route', async () => {
    const user = userEvent.setup()
    mountApp({
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: [...ALL_ORGANIZATION_PERMISSIONS],
      }),
    })
    await screen.findByText(demoEnUS.home.heading)

    const destinations = [
      [tAdmin('organization.nav.profile'), '/organization/admin/profile'],
      [tAdmin('organization.nav.invitations'), '/organization/admin/invitations'],
      [tAdmin('organization.nav.sso'), '/organization/admin/sso'],
      [tAdmin('organization.nav.roles'), '/organization/admin/roles'],
      [tAdmin('organization.nav.settings'), '/organization/admin/settings'],
    ] as const
    await user.click(screen.getByTestId('user-menu-trigger'))
    const menu = await screen.findByRole('menu')
    const group = await within(menu).findByRole('group', {
      name: demoEnUS.nav.organizationAdmin,
    })
    const items = within(group).getAllByRole('menuitem')
    expect(items).toHaveLength(destinations.length)
    destinations.forEach(([label, path], index) => {
      const item = items[index]
      if (item === undefined) {
        throw new Error(`the organization group has no entry at index ${index}`)
      }
      expect(item).toHaveTextContent(label)
      expect(item.tagName).toBe('A')
      expect(item).toHaveAttribute('href', path)
    })
  })

  it('leaves a modified click on an organization entry to the browser', async () => {
    const user = userEvent.setup()
    mountApp({
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: [...ALL_ORGANIZATION_PERMISSIONS],
      }),
    })
    await screen.findByText(demoEnUS.home.heading)

    await user.click(screen.getByTestId('user-menu-trigger'))
    const item = await screen.findByRole('menuitem', {
      name: tAdmin('organization.nav.invitations'),
    })
    let preventedByTheApp = true
    const cancelBrowserNavigation = (event: Event): void => {
      preventedByTheApp = event.defaultPrevented
      event.preventDefault()
    }
    document.addEventListener('click', cancelBrowserNavigation)
    try {
      fireEvent.click(item, { metaKey: true })
    } finally {
      document.removeEventListener('click', cancelBrowserNavigation)
    }
    expect(preventedByTheApp).toBe(false)
    expect(window.location.pathname).toBe('/')
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
  })
})

describe('the organization admin route admission', () => {
  it('renders a page only with the permission scoped to the current tenant', async () => {
    const { requested } = mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/invitations')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('sends the viewer to the first page the tenant admits when the page code is unheld', async () => {
    const { requested } = mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.idp.manage'] }),
    })
    await waitFor(() =>
      expect(window.location.pathname).toBe('/organization/admin/sso'),
    )
    expect(
      await screen.findByRole('heading', { name: tAdmin('sso.title') }),
    ).toBeInTheDocument()
    expect(
      requested.some((path) => path.endsWith('/invitations')),
    ).toBe(false)
  })

  it('remounts the surface under the new tenant after a switch', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [
        { id: 'acme', name: 'acme', kind: 'business' },
        { id: 'globex', name: 'globex', kind: 'business' },
      ],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    const email = await screen.findByLabelText(
      tAdmin('invitations.issue.emailLabel'),
    )
    await user.type(email, 'draft@example.com')

    await switchOrganization(user, 'globex')
    await waitFor(() => expect(window.location.pathname).toBe('/'))

    await user.click(screen.getByTestId('user-menu-trigger'))
    await user.click(
      await screen.findByRole('menuitem', {
        name: tAdmin('organization.nav.invitations'),
      }),
    )
    expect(
      await screen.findByLabelText(tAdmin('invitations.issue.emailLabel')),
    ).toHaveValue('')
  })

  it('renders a retryable failure page with a heading when the caller tenant read fails', async () => {
    const user = userEvent.setup()
    const { requested } = mountApp({
      path: '/organization/admin/invitations',
      tenants: [],
      tenantsFailed: true,
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.organization.accessFailed.title,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(
      screen.getByRole('button', {
        name: demoEnUS.organization.accessFailed.retry,
      }),
    ).toBeInTheDocument()

    expectOrganizationAbsent(await organizationEntry(user))

    const before = requested.filter((path) => path === SELF_TENANTS_PATH).length
    await user.click(
      screen.getByRole('button', {
        name: demoEnUS.organization.accessFailed.retry,
      }),
    )
    await waitFor(() =>
      expect(
        requested.filter((path) => path === SELF_TENANTS_PATH).length,
      ).toBeGreaterThan(before),
    )
  })

  it('renders the failure page when the permission read fails instead of sending the viewer home', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => Promise.reject(new Error('permission read refused')),
    })
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.organization.accessFailed.title,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/organization/admin/invitations')
    expect(
      screen.getByRole('button', {
        name: demoEnUS.organization.accessFailed.retry,
      }),
    ).toBeInTheDocument()

    expectOrganizationAbsent(await organizationEntry(user))
  })

  it('renders the failure page when the feature document read failed', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      features: { state: 'failed', error: new Error('feature read refused') },
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.organization.accessFailed.title,
      }),
    ).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent(
      demoEnUS.organization.accessFailed.title,
    )
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
    expect(window.location.pathname).toBe('/organization/admin/invitations')
  })

  it('lands on the waiting view while the retry re-reads, then back on the retry when it fails again', async () => {
    const user = userEvent.setup()
    let settleFeatures!: (
      state: ReturnType<typeof authenticatedSnapshot>['features'],
    ) => void
    const featureRetry = new Promise<
      ReturnType<typeof authenticatedSnapshot>['features']
    >((resolve) => {
      settleFeatures = resolve
    })
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      features: { state: 'failed', error: new Error('feature read refused') },
      featureRetry: () => featureRetry,
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    const retry = await screen.findByRole('button', {
      name: demoEnUS.organization.accessFailed.retry,
    })

    await user.click(retry)

    expect(document.activeElement).not.toBe(document.body)
    expect(document.activeElement).toHaveAccessibleName(
      demoEnUS.organization.accessPending,
    )

    await act(async () => {
      settleFeatures({ state: 'failed', error: new Error('still unavailable') })
    })

    const retryAgain = await screen.findByRole('button', {
      name: demoEnUS.organization.accessFailed.retry,
    })
    await waitFor(() => expect(retryAgain).toHaveFocus())
    expect(document.activeElement).not.toBe(document.body)
  })

  it('lands on the page heading when the retry succeeds', async () => {
    const user = userEvent.setup()
    let settleFeatures!: (
      state: ReturnType<typeof authenticatedSnapshot>['features'],
    ) => void
    const featureRetry = new Promise<
      ReturnType<typeof authenticatedSnapshot>['features']
    >((resolve) => {
      settleFeatures = resolve
    })
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      features: { state: 'failed', error: new Error('feature read refused') },
      featureRetry: () => featureRetry,
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    const retry = await screen.findByRole('button', {
      name: demoEnUS.organization.accessFailed.retry,
    })

    await user.click(retry)
    expect(document.activeElement).toHaveAccessibleName(
      demoEnUS.organization.accessPending,
    )

    await act(async () => {
      settleFeatures(ORGANIZATION_FEATURES)
    })

    const heading = await screen.findByRole('heading', {
      name: tAdmin('invitations.title'),
    })
    await waitFor(() => expect(heading).toHaveFocus())
    expect(heading.closest('main')).not.toBeNull()
  })

  it('leaves focus where the operator moved it when the retry fails again', async () => {
    const user = userEvent.setup()
    let settleFeatures!: (
      state: ReturnType<typeof authenticatedSnapshot>['features'],
    ) => void
    const featureRetry = new Promise<
      ReturnType<typeof authenticatedSnapshot>['features']
    >((resolve) => {
      settleFeatures = resolve
    })
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      features: { state: 'failed', error: new Error('feature read refused') },
      featureRetry: () => featureRetry,
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    const retry = await screen.findByRole('button', {
      name: demoEnUS.organization.accessFailed.retry,
    })
    await user.click(retry)
    expect(document.activeElement).toHaveAccessibleName(
      demoEnUS.organization.accessPending,
    )

    const elsewhere = document.createElement('button')
    elsewhere.textContent = 'elsewhere'
    document.body.appendChild(elsewhere)
    try {
      elsewhere.focus()
      await act(async () => {
        settleFeatures({
          state: 'failed',
          error: new Error('still unavailable'),
        })
      })
      const retryAgain = await screen.findByRole('button', {
        name: demoEnUS.organization.accessFailed.retry,
      })
      expect(elsewhere).toHaveFocus()
      expect(retryAgain).not.toHaveFocus()
    } finally {
      elsewhere.remove()
    }
  })

  it('still sends a personal tenant direct visit to the tenant home', async () => {
    setCurrentTenant('personal-account-1')
    setTenantNames(new Map([['personal-account-1', 'my-space']]))
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [
        {
          id: 'personal-account-1',
          name: 'my-space',
          kind: 'personal',
        },
      ],
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        name: demoEnUS.organization.accessFailed.title,
      }),
    ).toBeNull()
  })

  it('still sends the system tenant direct visit to the tenant home', async () => {
    setCurrentTenant('system')
    setTenantNames(new Map([['system', 'system']]))
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [],
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        name: demoEnUS.organization.accessFailed.title,
      }),
    ).toBeNull()
  })

  it('still sends an impersonating viewer direct visit to the tenant home', async () => {
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
      impersonating: true,
      impersonationGrant: true,
    })
    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        name: demoEnUS.organization.accessFailed.title,
      }),
    ).toBeNull()
  })

  it('still denies an anonymous direct visit', async () => {
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [],
      permissionsFor: () => ({ platform: [], tenant: [] }),
      anonymous: true,
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      await screen.findByRole('heading', { name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('denies an ordinary member with no organization permission', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('denies the surface when no organization declaration is served', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      features: readyFeatures(),
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expectOrganizationAbsent(await organizationEntry(user))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('does not admit the page while the feature document is not ready', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      features: { state: 'pending' },
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    await flushReads()
    expectOrganizationAbsent(await organizationEntry(user))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('serves the organization SSO page without reading any platform asset', async () => {
    const { requested } = mountApp({
      path: '/organization/admin/sso',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.idp.manage'] }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('sso.title') }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/sso/providers')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('serves the organization roles page from the tenant namespace only', async () => {
    const { requested } = mountApp({
      path: '/organization/admin/roles',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authz.tenant.role.read'],
      }),
    })
    expect(
      await screen.findByRole('heading', {
        name: tAdmin('organization.nav.roles'),
      }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/roles')
    expect(requested).toContain('/api/v1/tenants/acme/permissions')
    expect(requested).not.toContain('/api/v1/tenants/acme/bindings')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('renders a host-configured protected role read-only', async () => {
    mountApp({
      path: '/organization/admin/roles',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      roles: [tenantRole('tenant-admin'), tenantRole('editor')],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authz.tenant.role.read'],
      }),
    })
    expect(
      await screen.findByRole('heading', {
        name: tAdmin('organization.nav.roles'),
      }),
    ).toBeInTheDocument()
    expect(await screen.findByText('tenant-admin')).toBeInTheDocument()

    expect(
      screen.getByRole('button', {
        name: tAdmin('permissions.roles.viewGrantsAria', {
          role: 'tenant-admin',
        }),
      }),
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', {
        name: tAdmin('permissions.roles.manageGrantsAria', {
          role: 'tenant-admin',
        }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: tAdmin('permissions.roles.removeAria', { role: 'tenant-admin' }),
      }),
    ).toBeNull()
    expect(
      screen.getByText(tAdmin('permissions.roles.protectedReadOnly')),
    ).toBeInTheDocument()

    const user = userEvent.setup()
    const editorActions = screen.getByRole('button', {
      name: tAdmin('permissions.roles.actionsAria', { role: 'editor' }),
    })
    expect(editorActions).toBeEnabled()
    expect(editorActions).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(editorActions)
    expect(
      await screen.findByRole('menuitem', {
        name: tAdmin('permissions.roles.manageGrants'),
      }),
    ).toBeEnabled()
    expect(
      screen.getByRole('menuitem', {
        name: tAdmin('permissions.roles.remove'),
      }),
    ).toBeEnabled()
  })

  it('serves the organization settings page from the tenant namespace only', async () => {
    const { requested } = mountApp({
      path: '/organization/admin/settings',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['settings.tenant.read'],
      }),
    })
    expect(
      await screen.findByRole('heading', {
        name: tAdmin('organization.nav.settings'),
      }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/settings')
    expect(requested).not.toContain('/api/v1/system/settings')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('sends a viewer without the roles code to the first page they may open', async () => {
    const { requested } = mountApp({
      path: '/organization/admin/roles',
      tenants: [{ id: 'acme', name: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['settings.tenant.read'],
      }),
    })
    await waitFor(() =>
      expect(window.location.pathname).toBe('/organization/admin/settings'),
    )
    expect(
      await screen.findByRole('heading', {
        name: tAdmin('organization.nav.settings'),
      }),
    ).toBeInTheDocument()
    expect(requested).not.toContain('/api/v1/tenants/acme/roles')
  })

  it('names the organization first in the breadcrumb without linking it', async () => {
    const organizationName = 'acme'
    mountApp({
      path: '/organization/admin/invitations',
      tenants: [{ id: 'acme', name: organizationName, kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    await screen.findByRole('heading', { name: tAdmin('invitations.title') })
    const trail = within(
      screen.getByRole('navigation', { name: 'Breadcrumb' }),
    )
    expect(
      trail.queryByRole('link', { name: demoEnUS.nav.home }),
    ).toBeNull()
    expect(trail.getByText(organizationName)).toBeInTheDocument()
    expect(
      trail.queryByRole('link', { name: organizationName }),
    ).toBeNull()
    expect(trail.getByText(organizationName)).not.toHaveAttribute('aria-current')
  })
})
