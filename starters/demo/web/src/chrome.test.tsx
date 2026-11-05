/**
 * The host's two tenant layers: the identity the switcher keys on (the tenant
 * id) and the name the request surface announces (the alias). They are
 * distinct -- a rename moves the alias and leaves the id -- so these tests hold
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
  getCurrentTenantAlias,
  getCurrentTenantId,
  getRecordedTenantKind,
  initialColorModePreference,
  persistColorModePreference,
  readStoredColorModePreference,
  setCurrentTenantId,
  setTenantAlias,
  setTenantAliases,
  tenantAlias,
  TenantMenu,
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
  setTenantAliases(new Map())
  setCurrentTenantId(null)
  // The suite setup installs the desktop branch once per file; restore it
  // after the narrow-viewport case.
  mockMatchMedia(true)
})

describe('the tenant alias layer', () => {
  it('resolves a tenant id to its alias, not back to the id', () => {
    const tenants = [{ id: 'acme', alias: 'acme-inc', name: 'Acme Inc.' }]
    expect(tenantAlias('acme', tenants)).toBe('acme-inc')
    // An id nobody lists, and the no-tenant case, resolve to nothing.
    expect(tenantAlias('absent', tenants)).toBeNull()
    expect(tenantAlias(null, tenants)).toBeNull()
  })

  it('announces the alias while the switcher keeps the id', () => {
    // A tenant whose alias a rename has moved away from its id: the id the
    // switcher compares a pick against and the name the server resolves are no
    // longer the same string.
    setTenantAliases(new Map([['acme', 'acme-inc']]))
    setCurrentTenantId('acme')

    // The switcher's current tenant is the id...
    expect(getCurrentTenantId()).toBe('acme')
    // ...and the value announced to the server is the alias. Reading the id
    // here would be the defect: the server's resolve layer looks the name up in
    // the roster's alias column, so the id would be refused 404.
    expect(getCurrentTenantAlias()).toBe('acme-inc')
  })

  it('announces nothing for a tenant no source names', () => {
    // The placeholder roster is retired. A current tenant that neither the
    // caller tenant list nor the roster alias cache names has no alias to
    // announce; a fallback list would send a name the server may no longer
    // resolve, so the request surface stays silent instead.
    setCurrentTenantId('acme')
    expect(getCurrentTenantAlias()).toBeNull()
  })

  it('announces nothing with no current tenant', () => {
    setCurrentTenantId(null)
    expect(getCurrentTenantId()).toBeNull()
    expect(getCurrentTenantAlias()).toBeNull()
  })

  it('records an operation answer’s kind and keeps it across a roster read', () => {
    // The personal backfill answers the tenant, its alias and its kind; the
    // label of a row the caller tenant read has not listed yet comes from this
    // record. A later roster read carries aliases only, and must not drop it.
    setTenantAlias('personal-account-1', 'personal-account-1', 'personal')
    expect(getRecordedTenantKind('personal-account-1')).toBe('personal')

    setTenantAliases(new Map([['personal-account-1', 'renamed']]))
    expect(getRecordedTenantKind('personal-account-1')).toBe('personal')
    expect(getRecordedTenantKind('unknown')).toBeNull()
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
  { id: 'acme', alias: 'acme', name: 'Acme Inc.', kind: 'business' },
  { id: 'globex', alias: 'globex', name: 'Globex Corp.', kind: 'business' },
]

/** The tenant switcher over the host's own selection state, the way the
 * tenant frame composes it. */
function TenantSwitchHarness(): ReactElement {
  const [tenantId, setTenantId] = useTenantSelection('acme')
  return (
    <TenantMenu
      tenants={TWO_TENANTS}
      tenantId={tenantId}
      onTenantSelect={setTenantId}
    />
  )
}

/** Renders the switcher under the production stack and its own services. */
function renderTenantSwitch() {
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
    session: createTestSession(),
    api: createHarnessClient(),
    queryClient,
  }
  const result = render(
    <SpeedAppProviders i18n={i18n} queryClient={queryClient}>
      <AppServicesProvider services={services}>
        <TenantSwitchHarness />
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
  return { ...result, queryClient }
}

describe('the tenant switch', () => {
  afterEach(() => {
    setCurrentTenantId(null)
  })

  it("evicts the departing tenant's cached rows when the menu switches", async () => {
    const user = userEvent.setup()
    const { queryClient } = renderTenantSwitch()
    // A row read under the tenant the viewer is about to leave. Its key
    // carries the tenant id as the outermost segment (auth-core's
    // convention), which is what makes it the departing tenant's row.
    queryClient.setQueryData(tenantQueryKey('acme', 'rows'), [{ id: 'row-1' }])

    await user.click(screen.getByRole('button', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    // The switch is a host action: the departing tenant's whole cache
    // subtree is gone, and the new tenant is what every later read is keyed
    // under. A row left behind would be served to the new tenant's reads --
    // or, worse, shown as the new tenant's data.
    expect(getCurrentTenantId()).toBe('globex')
    expect(
      queryClient.getQueryData(tenantQueryKey('acme', 'rows')),
    ).toBeUndefined()
  })

  it('keeps the switch confirmation announced but clipped', async () => {
    const user = userEvent.setup()
    renderTenantSwitch()
    await user.click(screen.getByRole('button', { name: 'acme' }))
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
})

describe('the tenant switch permission reset', () => {
  afterEach(() => {
    setCurrentTenantId(null)
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
      const current = getCurrentTenantId()
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
vi.mock('./account-entry.js', () => ({ ACCOUNT_ENTRY: 'both' }))

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
    // drawer, and the avatar menu inside it.
    mockMatchMedia(false)
    window.history.replaceState({}, '', '/')
    setCurrentTenantId('acme')
    setTenantAliases(new Map([['acme', 'acme']]))
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
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

    // The avatar menu is the drawer's userMenu slot. Its organization group is
    // the admission's entries, resolved from the scripted reads.
    await user.click(screen.getByTestId('user-menu-trigger'))
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
