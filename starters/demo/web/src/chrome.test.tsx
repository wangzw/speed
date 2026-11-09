/**
 * The host's two tenant layers: the switcher's key (TenantOption.id) and the
 * name the request surface announces (the request-surface name). They are
 * distinct facts -- a switch moves the announced name and leaves the key -- so
 * these tests hold
 * them apart, above all for the diverged case the demo's own seeded tenant does
 * not exhibit.
 *
 * The device's colour-mode preference is the other half: the three-valued
 * storage helpers read what the account page and the boot path mirror, treat a
 * foreign value as absent, and never throw when storage is refused. The
 * switcher's confirmation must stay announced but clipped in both trigger
 * forms, so its text reaches an assistive reader without being painted.
 */

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
import { ACCOUNT_SECTION_PATHS, NOTIFICATIONS_PATH } from './paths.js'
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

/** The shell's own strings, resolved from the framework bundle the assembly
 * registers by default rather than from a local copy. */
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
  // The suite setup installs the desktop branch once per file; restore it
  // after the narrow-viewport case.
  mockMatchMedia(true)
})

describe('the current tenant', () => {
  it('holds the name the request surface announces', () => {
    setCurrentTenant('acme-inc')
    // The switcher and the request surface read the same value: the tenant's
    // name, the only name the request surface resolves.
    expect(getCurrentTenant()).toBe('acme-inc')
    expect(getCurrentTenantName()).toBe('acme-inc')
  })

  it('announces nothing with no current tenant', () => {
    setCurrentTenant(null)
    expect(getCurrentTenant()).toBeNull()
    expect(getCurrentTenantName()).toBeNull()
  })

  it('records an operation answer’s kind and keeps it across a roster read', () => {
    // An operation that lands the viewer in a tenant can answer the name and
    // the kind; the label of a row the caller tenant read has not listed yet
    // comes from this record. A later roster read carries names only, and
    // must not drop it.
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

    // A business row renders its display name, falling back to the
    // request-surface name when the profile names none. TenantOption.id
    // carries the request-surface name here.
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
    // A personal row keeps the account's own name, never the profile's.
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

/** A browser-storage stand-in: the jsdom environment's own localStorage is
 * unavailable (the Node runtime's experimental global shadows it), so the
 * suite installs one it can read back. */
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

/** Two tenants, as a host with a membership source would offer them. */
const TWO_TENANTS: readonly DemoTenant[] = [
  { id: 'acme', name: 'Acme Inc.', kind: 'business' },
  { id: 'globex', name: 'Globex Corp.', kind: 'business' },
]

/** The one organization an account with a single membership may enter, and the
 * name its row carries. */
const SOLE_TENANT: readonly DemoTenant[] = [
  {
    id: 'acme',
    name: 'Acme Inc.',
    displayName: 'Acme Inc.',
    kind: 'business',
  },
]
const SOLE_TENANT_NAME = 'Acme Inc.'

/** The account menu over the host's own selection state, the way the tenant
 * frame composes it: the organization rows live in the menu, so the harness
 * renders the real surface rather than the rows alone. */
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

/** The account menu over an account that may enter one organization, where the
 * row is context rather than a switcher. */
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

/** Renders the switcher under the production stack and its own services. */
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
    // A row read under the tenant the viewer is about to leave. Its key
    // carries the tenant id as the outermost segment (auth-core's
    // convention), which is what makes it the departing tenant's row.
    queryClient.setQueryData(tenantQueryKey('acme', 'rows'), [{ id: 'row-1' }])

    await user.click(screen.getByTestId('user-menu-trigger'))
    // The organization row names the current one; the others appear when it
    // expands.
    await user.click(await screen.findByRole('menuitem', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    // The switch is a host action: the departing tenant's whole cache
    // subtree is gone, and the new tenant is what every later read is keyed
    // under. A row left behind would be served to the new tenant's reads --
    // or, worse, shown as the new tenant's data.
    expect(getCurrentTenant()).toBe('globex')
    expect(
      queryClient.getQueryData(tenantQueryKey('acme', 'rows')),
    ).toBeUndefined()
  })

  it('keeps the switch confirmation announced but clipped', async () => {
    const user = userEvent.setup()
    renderTenantSwitch()
    await user.click(screen.getByTestId('user-menu-trigger'))
    // The organization row names the current one; the others appear when it
    // expands.
    await user.click(await screen.findByRole('menuitem', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    // The confirmation reaches an assistive reader through the live region,
    // but the header has no room to paint it: the region stays out of flow
    // and in a 1x1 box. An inline confirmation would be the defect.
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

    // The account may enter one organization, so its row has no expansion
    // control. It is a group, not a menuitem: its accessible name states the
    // organization and the menu's focus cycle skips it.
    const row = await screen.findByRole('group', { name: SOLE_TENANT_NAME })
    expect(row).toHaveAccessibleName(SOLE_TENANT_NAME)
    expect(screen.queryByRole('menuitem', { name: SOLE_TENANT_NAME })).toBeNull()
  })
})

describe('the tenant switch permission reset', () => {
  afterEach(() => {
    setCurrentTenant(null)
    // Detach the session this suite attached, so a later case does not read
    // it through auth-core's hooks.
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
    // Every render's pair of (current tenant, tenant permission set), so the
    // case can assert no frame carries the new tenant with the old set.
    const pairings: string[] = []
    // The session's tenant set read synchronously after select returns, before
    // React has committed the new tenant.
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
              // The reset belongs to the tenant-change entry: it must already
              // be applied when select returns.
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

    // select cleared the tenant domain before announcing the new tenant.
    expect(setAtSelect).toEqual([null])
    expect(writes).toContainEqual({ domain: 'tenant', perms: null })
    // The frame after the switch reads the set as unknown, and no render ever
    // paired globex with acme's set.
    expect(pairings).not.toContain('globex|acme:read')
    expect(pairings).toContain('globex|unknown')
    expect(screen.getByTestId('tenant')).toHaveTextContent('globex')
  })
})

/** A browser-storage stand-in for the colour-mode suite (see
 * installStorageDouble for why jsdom's own is unavailable). */
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

/**
 * The two account-menu surfaces, compared side by side: the header's avatar
 * menu and the sidebar's account entry must offer one list, so a viewer
 * switching between them never loses an entry. The placement is pinned to
 * 'both' here because the comparison needs both mounted at once; the entries
 * themselves come from the one shared list in chrome.tsx.
 */
vi.mock('./account-entry.js', () => ({
  ACCOUNT_ENTRY: 'both',
  // The predicate the assembly asks instead of comparing the constant at each
  // call site: this stub stands for the real module's answer for 'both'.
  accountEntryInUserSlot: (shellLayout?: string) => shellLayout !== 'sidebar',
  accountEntryInNavFooter: () => true,
}))

/** The entries every account menu carries unconditionally, in render order.
 * The organization admin group joins the list only while the caller's admission
 * allows it. */
const ACCOUNT_MENU_LABELS = [
  demoEnUS.userMenu.messages,
  demoEnUS.account.sections.profile,
  demoEnUS.account.sections.security,
  demoEnUS.account.sections.sessions,
  demoEnUS.userMenu.preferences,
  demoEnUS.userMenu.signOut,
] as const

/** The destination each entry that navigates opens. */
const ACCOUNT_MENU_DESTINATIONS = [
  { label: demoEnUS.userMenu.messages, path: NOTIFICATIONS_PATH },
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

/** Opens one surface's menu, reads its entry labels and closes it again. */
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

    // Both surfaces carry the same unconditional entries: an entry added to
    // one cannot be missing from the other.
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

/** UserMenu with one organization entry whose link pair is route-kit's own, so a
 * plain click navigates in place exactly as it does from the app's slot. */
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

/** Renders UserMenu alone, under the production provider stack and the app
 * services it reads, so a case can supply its drawer close channel. */
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

    // In the sidebar shell layout below md this menu sits in AppShell's
    // temporary drawer: choosing a destination must close both surfaces, or
    // the drawer covers the page it just opened.
    expect(closeDrawer).toHaveBeenCalledTimes(1)
    await waitFor(() =>
      expect(window.location.pathname).toBe('/organization/admin/invitations'),
    )
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
  })

  it('closes the app\'s temporary drawer when an organization entry is chosen', async () => {
    // The case above injects the close channel into UserMenu, so it cannot
    // prove the assembled app passes the slot's own channel through. This
    // case drives the app instead: the sidebar shell below md, its temporary
    // drawer, and the account row's menu inside it -- the sidebar shape keeps
    // the entry in the row and renders no avatar menu.
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
      // The organization landing page reads its first declaration's list.
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

    // Below md the sidebar shell moves its chrome rows into the temporary
    // drawer; the minimal top bar carries only the trigger.
    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.openNav') }),
    )

    // The account row in the drawer paper is the account entry this shape
    // renders. Its organization group is the admission's entries, resolved
    // from the scripted reads.
    await user.click(screen.getByTestId('account-entry-trigger'))
    const menu = await screen.findByRole('menu')
    const group = await within(menu).findByRole('group', {
      name: demoEnUS.nav.organizationAdmin,
    })
    const firstEntry = within(group).getAllByRole('menuitem')[0]
    expect(firstEntry).toBeDefined()
    await user.click(firstEntry as HTMLElement)

    // The route-kit entry navigates in place...
    await waitFor(() =>
      expect(window.location.pathname).toBe('/organization/admin/invitations'),
    )
    // ...and the slot's closeDrawer closed the temporary drawer around it.
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: tLayout('appShell.openNav') }),
      ).toHaveAttribute('aria-expanded', 'false'),
    )
  })
})
