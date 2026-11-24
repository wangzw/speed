import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { attachSession, tenantQueryKey, useAuthState } from '@speed/auth-core'
import type { AuthDomain } from '@speed/auth-core'
import { LAYOUT_KIT_NAMESPACE, type AppShellNavItem } from '@speed/layout-kit'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider, useRouteLinkProps } from '@speed/route-kit'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from '@speed/tenancy-ui'
import { AppServicesProvider } from './app-services.js'
import {
  COLOR_MODE_STORAGE_KEY,
  getCurrentTenantName,
  getCurrentTenant,
  getRecordedTenantDisplayName,
  getRecordedTenantKind,
  initialColorModePreference,
  persistColorModePreference,
  readStoredColorModePreference,
  setCurrentTenant,
  setTenantName,
  setTenantNames,
  tenantDisplayName,
  UserMenu,
  useTenantSelection,
} from './chrome.js'
import type { DemoTenant } from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import {
  ACCOUNT_SECTION_PATHS,
  NOTIFICATIONS_PATH,
  TRANSFER_REQUESTS_PATH,
} from './paths.js'
import { PlatformPermissions } from './platform-permissions.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { mockMatchMedia } from './test-utils/matchMedia.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'
import type { TestSession } from './test-utils/session.js'

const tLayout = createSpeedI18n({
  supportedLanguages: ['en-US', 'zh-CN'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
}).getFixedT('en-US', LAYOUT_KIT_NAMESPACE)

afterEach(() => {
  setTenantNames(new Map())
  setCurrentTenant(null)
  mockMatchMedia(true)
})

describe('the current tenant', () => {
  it('holds the name the request surface announces', () => {
    setCurrentTenant('acme-inc')
    expect(getCurrentTenant()).toBe('acme-inc')
    expect(getCurrentTenantName()).toBe('acme-inc')
  })

  it('announces nothing with no current tenant', () => {
    setCurrentTenant(null)
    expect(getCurrentTenant()).toBeNull()
    expect(getCurrentTenantName()).toBeNull()
  })

  it('records an operation answer’s kind and keeps it across a roster read', () => {
    setTenantName('acme', 'business')
    expect(getRecordedTenantKind('acme')).toBe('business')

    setTenantNames(new Map([['acme', 'Acme Inc.']]))
    expect(getRecordedTenantKind('acme')).toBe('business')
    expect(getRecordedTenantKind('unknown')).toBeNull()
  })

  it('records the roster display name and renders it in preference to the name', () => {
    setTenantNames(new Map([['acme', 'Acme Incorporated']]))
    expect(getRecordedTenantDisplayName('acme')).toBe('Acme Incorporated')
    expect(getRecordedTenantDisplayName('unknown')).toBeNull()

    expect(
      tenantDisplayName(
        {
          id: 'acme-inc',
          name: 'Acme Incorporated',
          displayName: 'Acme Incorporated',
        },
        'Organization',
      ),
    ).toBe('Acme Incorporated')
    expect(
      tenantDisplayName({ id: 'acme-inc', name: 'acme-inc' }, 'Organization'),
    ).toBe('acme-inc')
    expect(
      tenantDisplayName(
        {
          id: 'my-space',
          name: 'my-space',
          kind: 'personal',
          displayName: 'The account space',
        },
        'Ada Lovelace',
      ),
    ).toBe('Ada Lovelace')
  })
})

function installStorageDouble(): Map<string, string> {
  const entries = new Map<string, string>()
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => entries.get(key) ?? null,
    setItem: (key: string, value: string) => {
      entries.set(key, value)
    },
    removeItem: (key: string) => {
      entries.delete(key)
    },
    clear: () => entries.clear(),
  })
  return entries
}

const TWO_TENANTS: readonly DemoTenant[] = [
  { id: 'acme', name: 'Acme Inc.', kind: 'business' },
  { id: 'globex', name: 'Globex Corp.', kind: 'business' },
]

const SOLE_TENANT: readonly DemoTenant[] = [
  {
    id: 'acme',
    name: 'Acme Inc.',
    displayName: 'Acme Inc.',
    kind: 'business',
  },
]
const SOLE_TENANT_NAME = 'Acme Inc.'

function TenantSwitchHarness(): ReactElement {
  const [tenantName, setTenantName] = useTenantSelection('acme')
  return (
    <UserMenu
      organizations={{
        tenants: TWO_TENANTS,
        tenantName,
        onSelect: setTenantName,
      }}
    />
  )
}

function SingleOrganizationSwitchHarness(): ReactElement {
  const [tenantName, setTenantName] = useTenantSelection('acme')
  return (
    <UserMenu
      organizations={{
        tenants: SOLE_TENANT,
        tenantName,
        onSelect: setTenantName,
      }}
    />
  )
}

function renderTenantSwitch(harness: ReactElement = <TenantSwitchHarness />) {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: [
      { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
      { namespace: DEMO_NAMESPACE, resources: demoResources },
    ],
  })
  const queryClient = createQueryClient()
  const session = createTestSession(authenticatedSnapshot())
  attachSession(session)
  const services: SpeedAppServices = {
    session,
    api: createHarnessClient(),
    queryClient,
  }
  const result = render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>
        <RouteProvider table={[]} fallback={() => null}>
          {harness}
        </RouteProvider>
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
  return { ...result, queryClient }
}

describe('the tenant switch', () => {
  afterEach(() => {
    setCurrentTenant(null)
  })

  it("evicts the departing tenant's cached rows when the menu switches", async () => {
    const user = userEvent.setup()
    const { queryClient } = renderTenantSwitch()
    queryClient.setQueryData(tenantQueryKey('acme', 'rows'), [{ id: 'row-1' }])

    await user.click(screen.getByTestId('user-menu-trigger'))
    await user.click(await screen.findByRole('menuitem', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    expect(getCurrentTenant()).toBe('globex')
    expect(
      queryClient.getQueryData(tenantQueryKey('acme', 'rows')),
    ).toBeUndefined()
  })

  it('keeps the switch confirmation announced but clipped', async () => {
    const user = userEvent.setup()
    renderTenantSwitch()
    await user.click(screen.getByTestId('user-menu-trigger'))
    await user.click(await screen.findByRole('menuitem', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    const switchedTo = (
      tenancyUiResources['en-US'] as {
        readonly tenantSwitcher: { readonly switchedTo: string }
      }
    ).tenantSwitcher.switchedTo.replace('{{tenant}}', 'globex')
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(switchedTo)
    const style = getComputedStyle(status)
    expect(style.position).toBe('absolute')
    expect(parseFloat(style.width)).toBeLessThanOrEqual(1)
    expect(parseFloat(style.height)).toBeLessThanOrEqual(1)
  })

  it('names a lone organization row by the organization, as a menu group rather than a menuitem', async () => {
    const user = userEvent.setup()
    renderTenantSwitch(<SingleOrganizationSwitchHarness />)
    await user.click(screen.getByTestId('user-menu-trigger'))

    const row = await screen.findByRole('group', { name: SOLE_TENANT_NAME })
    expect(row).toHaveAccessibleName(SOLE_TENANT_NAME)
    expect(screen.queryByRole('menuitem', { name: SOLE_TENANT_NAME })).toBeNull()
  })
})

describe('the tenant switch permission reset', () => {
  afterEach(() => {
    setCurrentTenant(null)
    attachSession(createTestSession())
  })

  it('clears the tenant set synchronously, so no frame pairs the new tenant with it', async () => {
    const base = createTestSession(
      authenticatedSnapshot({ tenant: ['acme:read'], system: ['audit.read'] }),
    )
    const writes: { domain: AuthDomain; perms: readonly string[] | null }[] = []
    const session: TestSession = {
      ...base,
      setPermissionSet: (domain, perms) => {
        writes.push({ domain, perms })
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    }
    attachSession(session)

    const i18n = createSpeedI18n({
      supportedLanguages: ['zh-CN', 'en-US'],
      defaultLanguage: 'en-US',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
      namespaces: [
        { namespace: TENANCY_UI_NAMESPACE, resources: tenancyUiResources },
        { namespace: DEMO_NAMESPACE, resources: demoResources },
      ],
    })
    const queryClient = createQueryClient()
    const services: SpeedAppServices = {
      session,
      api: createHarnessClient(),
      queryClient,
    }
    const pairings: string[] = []
    const setAtSelect: (readonly string[] | null)[] = []

    function Harness() {
      const [tenantIdState, select] = useTenantSelection('acme')
      const snapshot = useAuthState()
      const current = getCurrentTenant()
      const set = snapshot.permissionSets.tenant
      pairings.push(
        `${current ?? 'none'}|${set === null ? 'unknown' : set.join(',')}`,
      )
      return (
        <div>
          <span data-testid="tenant">{tenantIdState ?? 'none'}</span>
          <button
            type="button"
            onClick={() => {
              select('globex')
              setAtSelect.push(session.getSnapshot().permissionSets.tenant)
            }}
          >
            switch
          </button>
        </div>
      )
    }

    const user = userEvent.setup()
    render(
      <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
        <AppServicesProvider services={services}>
          <Harness />
        </AppServicesProvider>
      </SpeedAppProviders>,
    )

    expect(screen.getByTestId('tenant')).toHaveTextContent('acme')
    await user.click(screen.getByRole('button', { name: 'switch' }))

    expect(setAtSelect).toEqual([null])
    expect(writes).toContainEqual({ domain: 'tenant', perms: null })
    expect(pairings).not.toContain('globex|acme:read')
    expect(pairings).toContain('globex|unknown')
    expect(screen.getByTestId('tenant')).toHaveTextContent('globex')
  })
})

describe('the colour mode preference', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('reads back a stored explicit choice', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'light')
    expect(readStoredColorModePreference()).toBe('light')
    expect(initialColorModePreference()).toBe('light')

    entries.set(COLOR_MODE_STORAGE_KEY, 'dark')
    expect(readStoredColorModePreference()).toBe('dark')
    expect(initialColorModePreference()).toBe('dark')
  })

  it('reads back the system preference', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'system')
    expect(readStoredColorModePreference()).toBe('system')
    expect(initialColorModePreference()).toBe('system')
  })

  it('treats an unrecognised stored value as absent and boots to system', () => {
    const entries = installStorageDouble()
    entries.set(COLOR_MODE_STORAGE_KEY, 'sepia')
    expect(readStoredColorModePreference()).toBeNull()
    expect(initialColorModePreference()).toBe('system')
  })

  it('boots to system when nothing was stored', () => {
    installStorageDouble()
    expect(readStoredColorModePreference()).toBeNull()
    expect(initialColorModePreference()).toBe('system')
  })

  it('does not throw when storage refuses reads or writes', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('denied')
      },
      setItem: () => {
        throw new Error('denied')
      },
      removeItem: () => {
        throw new Error('denied')
      },
      clear: () => {
        throw new Error('denied')
      },
    })

    expect(() => readStoredColorModePreference()).not.toThrow()
    expect(readStoredColorModePreference()).toBeNull()
    expect(() => persistColorModePreference('dark')).not.toThrow()
    expect(initialColorModePreference()).toBe('system')
  })
})

vi.mock('./account-entry.js', () => ({
  ACCOUNT_ENTRY: 'both',
  accountEntryInUserSlot: (shellLayout?: string) => shellLayout !== 'sidebar',
  accountEntryInNavFooter: () => true,
}))

const ACCOUNT_MENU_LABELS = [
  demoEnUS.userMenu.messages,
  demoEnUS.userMenu.transferRequests,
  demoEnUS.account.sections.profile,
  demoEnUS.account.sections.security,
  demoEnUS.account.sections.sessions,
  demoEnUS.userMenu.preferences,
  demoEnUS.userMenu.signOut,
] as const

const ACCOUNT_MENU_DESTINATIONS = [
  { label: demoEnUS.userMenu.messages, path: NOTIFICATIONS_PATH },
  {
    label: demoEnUS.userMenu.transferRequests,
    path: TRANSFER_REQUESTS_PATH,
  },
  {
    label: demoEnUS.account.sections.profile,
    path: ACCOUNT_SECTION_PATHS.profile,
  },
  {
    label: demoEnUS.account.sections.security,
    path: ACCOUNT_SECTION_PATHS.security,
  },
  {
    label: demoEnUS.account.sections.sessions,
    path: ACCOUNT_SECTION_PATHS.sessions,
  },
  {
    label: demoEnUS.userMenu.preferences,
    path: ACCOUNT_SECTION_PATHS.preferences,
  },
] as const

const ACCOUNT_MENU_SURFACES = [
  { name: 'header', trigger: 'user-menu-trigger' },
  { name: 'sidebar', trigger: 'account-entry-trigger' },
] as const

async function accountMenuLabels(
  user: ReturnType<typeof userEvent.setup>,
  triggerId: string,
): Promise<string[]> {
  await user.click(screen.getByTestId(triggerId))
  const labels = screen
    .getAllByRole('menuitem')
    .map((item) => item.textContent ?? '')
  await user.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
  )
  return labels
}

describe('the account menus', () => {
  it('offers the same entries from the header and the sidebar', async () => {
    window.history.replaceState({}, '', '/')
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const header = await accountMenuLabels(user, 'user-menu-trigger')
    const sidebar = await accountMenuLabels(user, 'account-entry-trigger')

    expect(header).toEqual([...ACCOUNT_MENU_LABELS])
    expect(new Set(header)).toEqual(new Set(sidebar))
  })

  for (const surface of ACCOUNT_MENU_SURFACES) {
    it(`navigates every entry from the ${surface.name} menu`, async () => {
      const user = userEvent.setup()
      renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

      for (const { label, path } of ACCOUNT_MENU_DESTINATIONS) {
        await user.click(screen.getByTestId(surface.trigger))
        await user.click(screen.getByRole('menuitem', { name: label }))
        await waitFor(() => expect(window.location.pathname).toBe(path))
      }
    })

    it(`signs out from the ${surface.name} menu`, async () => {
      const user = userEvent.setup()
      const session = createTestSession(authenticatedSnapshot())
      const logout = vi.spyOn(session, 'logout')
      renderDemoApp({ session })

      await user.click(screen.getByTestId(surface.trigger))
      await user.click(
        screen.getByRole('menuitem', { name: demoEnUS.userMenu.signOut }),
      )
      await waitFor(() => expect(logout).toHaveBeenCalled())
    })
  }
})

function UserMenuHarness({
  closeDrawer,
  target,
}: {
  readonly closeDrawer?: () => void
  readonly target: string
}): ReactElement {
  const link = useRouteLinkProps(target)
  const items: readonly AppShellNavItem[] = [
    {
      id: target,
      label: 'Invitations',
      href: link.href,
      onClick: link.onClick,
    },
  ]
  return <UserMenu closeDrawer={closeDrawer} organizationItems={items} />
}

function renderUserMenu(options: {
  readonly closeDrawer?: () => void
  readonly target: string
}) {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: [{ namespace: DEMO_NAMESPACE, resources: demoResources }],
  })
  const queryClient = createQueryClient()
  const session = createTestSession(authenticatedSnapshot())
  attachSession(session)
  const services: SpeedAppServices = {
    session,
    api: createHarnessClient(),
    queryClient,
  }
  return render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>
        <RouteProvider table={[]} fallback={() => null}>
          <UserMenuHarness
            closeDrawer={options.closeDrawer}
            target={options.target}
          />
        </RouteProvider>
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
}

describe('the header account menu in the temporary drawer', () => {
  it('closes the drawer as well as the menu when an organization entry is chosen', async () => {
    window.history.replaceState({}, '', '/')
    const user = userEvent.setup()
    const closeDrawer = vi.fn()
    renderUserMenu({ closeDrawer, target: '/organization/admin/invitations' })

    await user.click(screen.getByTestId('user-menu-trigger'))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Invitations' }),
    )

    expect(closeDrawer).toHaveBeenCalledTimes(1)
    await waitFor(() =>
      expect(window.location.pathname).toBe('/organization/admin/invitations'),
    )
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
  })

  it('closes the app\'s temporary drawer when an organization entry is chosen', async () => {
    mockMatchMedia(false)
    window.history.replaceState({}, '', '/')
    setCurrentTenant('acme')
    setTenantNames(new Map([['acme', 'acme']]))
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ name: 'acme', kind: 'business' }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({
          platform: [],
          tenant: ['authn.membership.manage'],
        } as T)
      }
      if (path.endsWith('/invitations')) {
        return Promise.resolve({ invitations: [], next_cursor: '' } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const user = userEvent.setup()
    const session = createTestSession(
      authenticatedSnapshot({
        features: readyFeatures(
          'authn.admin',
          'authn.invitations',
          'authn.admin.sso',
          'authz.tenantadmin',
          'settings.tenantadmin',
          'tenant.profile',
        ),
      }),
    )
    renderDemoApp({
      session,
      shellLayout: 'sidebar',
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    await screen.findByText(demoEnUS.home.heading)

    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.openNav') }),
    )

    await user.click(screen.getByTestId('account-entry-trigger'))
    const menu = await screen.findByRole('menu')
    const group = await within(menu).findByRole('group', {
      name: demoEnUS.nav.organizationAdmin,
    })
    const firstEntry = within(group).getAllByRole('menuitem')[0]
    expect(firstEntry).toBeDefined()
    await user.click(firstEntry as HTMLElement)

    await waitFor(() =>
      expect(window.location.pathname).toBe('/organization/admin/invitations'),
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: tLayout('appShell.openNav') }),
      ).toHaveAttribute('aria-expanded', 'false'),
    )
  })
})
