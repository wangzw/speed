/**
 * The app composition: the tenant shell's anonymous and authenticated branches,
 * the host's admin entry, and the admin area's session gate and route
 * guard.
 *
 * The tenant shell fail-closes to the host's sign-in surface -- the host's
 * chrome around auth-ui's password channel, the only channel the skeleton
 * offers. Once authenticated the frame appears with the host's admin entry,
 * which lands the viewer in the admin area. The admin area carries the same
 * session gate (an anonymous viewer meets the sign-in surface, never an admin
 * whose requests cannot answer) and a route guard over each page's permission
 * code. Its three tiers read the one platform permission set: the entry
 * targets the first admin page whose code the principal holds, the area gate
 * on any non-empty set, and each page's guard on its own code. A staff viewer
 * whose set holds a permission other than the dashboard's reads the three
 * apart.
 */

import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { drawerClasses } from '@mui/material/Drawer'
import { useTheme } from '@mui/material/styles'
import { listItemIconClasses } from '@mui/material/ListItemIcon'
import { listItemTextClasses } from '@mui/material/ListItemText'
import { ACCOUNT_UI_NAMESPACE, accountUiResources } from '@speed/account-ui'
import type { AuthDomain, AuthSession } from '@speed/auth-core'
import type { RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { AUTH_UI_NAMESPACE } from '@speed/auth-ui'
import {
  ADMIN_UI_NAMESPACE,
  adminUiResources,
} from '@speed/admin-ui'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import { glassThemePreset, themePresets } from '@speed/theme-presets'
import { UI_KIT_NAMESPACE } from '@speed/ui-kit'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import demoZhCN from './locales/zh-CN.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import { createBackend } from './a11y/backends.js'
import { hasCoarsePointerTouchTarget } from './test-utils/emitted-css.js'
import { mockMatchMedia } from './test-utils/matchMedia.js'
import { AUTH_CALLBACK_PATH } from './routes.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'
import {
  getCurrentTenantId,
  initialThemePreset,
  setCurrentTenantId,
  setTenantAliases,
  THEME_PRESET_STORAGE_KEY,
} from './chrome.js'
import { PlatformPermissions } from './platform-permissions.js'
import { clearSelfTenants, loadSelfTenants } from './tenant-self.js'

/** The tenant the suite acts in: an identifier and the alias the host has
 * recorded for it. The host now holds no default roster, so a case that
 * starts with a current tenant must make it nameable too. */
const TENANT_ID = 'acme'

/**
 * The expected copy, resolved from the packages' shipped resources through a
 * production instance rather than read from a file path: the release the app
 * ships is what the suite asserts. The two languages are fixed per tag, so
 * the bilingual cases compare translations, not a literal.
 */
const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
    { namespace: ACCOUNT_UI_NAMESPACE, resources: accountUiResources },
    { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  ],
})
const tAuth = textI18n.getFixedT('en-US', AUTH_UI_NAMESPACE)
const tAccount = textI18n.getFixedT('en-US', ACCOUNT_UI_NAMESPACE)
const tAuthZh = textI18n.getFixedT('zh-CN', AUTH_UI_NAMESPACE)
const tAdmin = textI18n.getFixedT('en-US', ADMIN_UI_NAMESPACE)
const tLayout = textI18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)
const tLayoutZh = textI18n.getFixedT('zh-CN', LAYOUT_KIT_NAMESPACE)
const tUiKit = textI18n.getFixedT('en-US', UI_KIT_NAMESPACE)

/** Opens the header placement of the account menu, which carries the
 * organization switcher, the admin entry and the way back from the admin. */
async function openHeaderAccountMenu(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  await user.click(screen.getByTestId('user-menu-trigger'))
  return screen.findByRole('menu')
}

/** Closes the open account menu and waits for its surface to leave the tree. */
async function closeAccountMenu(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await user.keyboard('{Escape}')
  await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
}

/** Makes the platform's own organization the current tenant: the admin entry
 * is offered only there, so a case about that entry must settle it. */
function settlePlatformOrganization(): void {
  setCurrentTenantId('system')
  setTenantAliases(new Map([['system', 'system']]))
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  // The current tenant is module-level host state (the client's tenantProvider
  // reads it); reset it so a switch in one test does not leak into the next.
  // The alias cache is reset with it: a current tenant no source names would
  // not survive the frame's settlement.
  setCurrentTenantId(TENANT_ID)
  setTenantAliases(new Map([[TENANT_ID, TENANT_ID]]))
  // The caller tenant list is module state too: a case starts with no rows
  // and loads its own through the scripted backend.
  clearSelfTenants()
})

describe('the render harness', () => {
  it("renders under the assembly's query client, read policy included", () => {
    const { queryClient, unmount } = renderDemoApp()

    // The harness delegates to createQueryClient instead of building a
    // client of its own, so a view suite's re-read timing matches the
    // assembled app's: every read is stale (so a remount, the tab
    // becoming visible again or a reconnect re-reads) and nothing is
    // retried at this layer.
    expect(queryClient.getDefaultOptions().queries).toEqual({
      retry: false,
      staleTime: 0,
      refetchOnMount: true,
      refetchOnWindowFocus: true,
      refetchOnReconnect: true,
    })
    expect(queryClient.getDefaultOptions().mutations).toEqual({
      retry: false,
    })
    unmount()
  })
})

describe('the app at the root path', () => {
  it('shows the host sign-in surface to a visitor without a session', async () => {
    renderDemoApp()

    // The host's chrome frames auth-ui's screen: brand and heading above the
    // password field (which appears once the discovery read settles).
    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      await screen.findByLabelText(tAuth('passwordSignIn.identifierLabel')),
    ).toBeInTheDocument()
    // The authenticated frame's business page is not rendered.
    expect(screen.queryByText(demoEnUS.home.heading)).not.toBeInTheDocument()
  })

  it('carries the brand in the banner as a non-heading', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header')
    expect(banner).not.toBeNull()
    // The brand sits in the banner on every page, ahead of the page's own
    // h1: it must not be a heading element, or the page outline would lead
    // with the brand and invert the heading order. Asserted at the element
    // and against the whole banner, so no heading hides beside it either.
    expect(brand.tagName).not.toBe('H6')
    expect(
      within(banner as HTMLElement).queryByRole('heading'),
    ).not.toBeInTheDocument()
  })

  it('offers only the password channel, with no tab strip', async () => {
    renderDemoApp()
    // Wait for the surface (the discovery read resolves a document with the
    // one channel). No SMS axis is offered (the skeleton delivers no code),
    // so there is nothing to switch between and no tablist.
    await screen.findByLabelText(tAuth('passwordSignIn.identifierLabel'))
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
  })

  it('renders the sign-in surface in the active language', async () => {
    renderDemoApp({ language: 'zh-CN' })

    expect(
      screen.getByRole('heading', { level: 1, name: demoZhCN.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      await screen.findByLabelText(tAuthZh('passwordSignIn.identifierLabel')),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).not.toBeInTheDocument()
  })

  it('renders the auth callback route outside the tenant shell', async () => {
    window.history.replaceState({}, '', AUTH_CALLBACK_PATH)
    renderDemoApp()

    // The callback handler mounts -- the operation-less test session's flow
    // read fails, so it offers its retry -- and the shell's sign-in surface is
    // nowhere: the callback route is not gated by the tenant shell. The card
    // carries the sign-in page's heading on purpose, so the heading no longer
    // tells the two surfaces apart; the password channel's form does.
    expect(
      await screen.findByRole('button', {
        name: tAuth('socialCallback.retry'),
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(tAuth('passwordSignIn.identifierLabel')),
    ).not.toBeInTheDocument()
  })
})

describe('the tenant shell with a session', () => {
  it('offers a platform administrator a header admin entry that lands in the admin', async () => {
    settlePlatformOrganization()
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    // The entry is not a navigation item: the sidebar carries no way into the
    // admin.
    const nav = screen.getByRole('navigation')
    expect(
      within(nav).queryByRole('link', { name: demoEnUS.nav.admin }),
    ).not.toBeInTheDocument()

    // It rides the header's account menu instead. A plain click routes in
    // place to the admin prefix, which renders the admin's dashboard: the
    // admin frame's own navigation appears.
    const menu = await openHeaderAccountMenu(user)
    await user.click(
      within(menu).getByRole('menuitem', { name: demoEnUS.nav.admin }),
    )
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
  })

  it('offers an ordinary member no admin entry anywhere', async () => {
    settlePlatformOrganization()
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    // The member's platform permission set is known and does not hold the
    // entry's code, so neither the sidebar nor the header's account menu
    // carries an admin entry: there is no way into the admin to be found.
    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.admin }),
    ).not.toBeInTheDocument()
    const menu = await openHeaderAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
  })

  it('keeps the admin entry hidden while the platform permission set is unknown', async () => {
    // An authenticated session whose platform set is still unknown (null): the
    // state the host leaves the session in before it has read the caller's
    // platform permissions. usePermission fails closed on an unknown set, so
    // the entry is absent -- a member never sees it for the moment before the
    // set lands.
    settlePlatformOrganization()
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    const menu = await openHeaderAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
  })

  it('reveals the admin entry once the platform permission set lands', async () => {
    // The attach-then-appear transition the host drives through
    // setPermissionSet: the set flips from unknown (null) to a known one
    // holding the entry's code, and the entry appears where it was hidden.
    settlePlatformOrganization()
    const user = userEvent.setup()
    const session = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session })

    const menu = await openHeaderAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
    await closeAccountMenu(user)

    act(() => {
      session.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })
    const reopened = await openHeaderAccountMenu(user)
    expect(
      await within(reopened).findByRole('menuitem', {
        name: demoEnUS.nav.admin,
      }),
    ).toBeInTheDocument()
  })
})

describe('the admin area', () => {
  it('gates a request-less viewer to the sign-in surface and follows a session that ends', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    // The session holds a platform permission, so the area gate admits the
    // frame; the point under test is the session gate around it.
    const session = createTestSession(
      authenticatedSnapshot({ system: ['tenant.read'] }),
    )
    renderDemoApp({ session })

    // The frame is up at the admin path while the session is authenticated.
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()

    // The session turns anonymous (a sign-out or a refused refresh -- the same
    // flip): the admin area meets the sign-in surface, never an admin page
    // stuck loading.
    act(() => {
      session.set(ANONYMOUS_SNAPSHOT)
    })
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()
  })

  it('renders an anonymous viewer at an admin path the sign-in surface, not the admin', () => {
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp()

    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()
  })

  it('admits a page whose permission the principal holds', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('tenants.title'),
      }),
    ).toBeInTheDocument()
  })

  it('settles a tenant for an admin URL loaded directly, so the permission read answers', async () => {
    // An admin URL loaded directly (a reload, a bookmark) mounts no tenant
    // frame, and the caller's permission read sits on the guarded API
    // endpoint: without a tenant name the read is refused 404 before any
    // handler runs, and the area gate would turn a platform administrator
    // away. The area settles the caller list's default itself before the
    // read; no platform roster read is spent, because that read needs the
    // permission this settle exists to let the read answer.
    setCurrentTenantId(null)
    window.history.replaceState({}, '', '/admin/tenants')
    const requested: string[] = []
    const tenantAtRead: (string | null)[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: TENANT_ID, alias: 'acme' }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        tenantAtRead.push(getCurrentTenantId())
        return Promise.resolve({
          platform: ['tenant.read'],
          tenant: [],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const base = createTestSession(authenticatedSnapshot())
    // The harness session attaches nothing by default; this case drives the
    // production attach, so the set has to land in the snapshot the gates read.
    const session: AuthSession = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    }
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    // The read the area gate settled on carried the default the caller list
    // names. The tenants page itself reads the roster once it renders; the
    // shell spends no roster read to settle the tenant.
    expect(tenantAtRead).toEqual([TENANT_ID])
    expect(requested).toContain('/api/v1/auth/me/tenants')
  })

  it('settles the caller list when the deployment serves no roster', async () => {
    // A deployment without tenant.admin has no roster to name a default
    // tenant. The admin area must still settle the caller's own list, or the
    // guarded permission read never carries a tenant name and a staff operator
    // on an admin deep link waits forever.
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    window.history.replaceState({}, '', '/admin/settings')
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: TENANT_ID, alias: TENANT_ID }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({
          platform: ['settings.read'],
          tenant: [],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    await loadSelfTenants()
    const base = createTestSession(
      authenticatedSnapshot({
        system: null,
        features: readyFeatures('settings.admin'),
      }),
    )
    const session: AuthSession = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    }
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    // The roster is not part of this assembly, so no read is spent on it.
    expect(requested).not.toContain('/api/v1/system/tenants')

    clearSelfTenants()
  })

  it('turns a member who reaches an admin page directly out of the admin entirely', async () => {
    // The member never gets the entry (the test above), but hiding the entry is
    // not gating: a typed URL reaches the admin path space anyway. The area
    // gate -- not the hidden entry -- is what turns it away, and it does so
    // before the shell renders: the admin's menu names are themselves
    // information a member must not receive.
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    // No admin frame at all -- neither its navigation landmark nor any of
    // its page links -- and no no-permission surface inside an admin shell: the
    // content is not swapped for a denial, the whole area is withheld.
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: tAdmin('tenants.title') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(tUiKit('emptyState.noPermission.title')),
    ).not.toBeInTheDocument()

    // The viewer lands in the tenant area instead: the redirect left the
    // admin path space for a shell the member may use.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
  })

  it('turns a member away from every admin path, not only the one typed', async () => {
    for (const path of ['/admin', '/admin/audit']) {
      window.history.replaceState({}, '', path)
      const { unmount } = renderDemoApp({
        session: createTestSession(authenticatedSnapshot({ system: [] })),
      })
      expect(
        await screen.findByRole('heading', {
          level: 1,
          name: demoEnUS.home.heading,
        }),
      ).toBeInTheDocument()
      expect(
        screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
      ).not.toBeInTheDocument()
      unmount()
    }
  })

  it('shows no admin to a viewer whose platform permission set is still unknown', async () => {
    // An authenticated session whose platform set is unknown (null): the state
    // the host leaves the session in before it has read the caller's platform
    // permissions. The area gate fails closed -- it neither renders the admin
    // shell (which a member must never see) nor redirects (which would bounce a
    // staff member still loading) -- so the viewer meets the shell-less
    // placeholder until the set lands.
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('turns a member who loads an admin URL directly out of the admin, without a roster read', async () => {
    // A member's caller tenant list names their tenant, so the admin area
    // settles it and reads the platform permission set. The ANSWERED document
    // is empty -- a known empty set, unlike a failed read -- and the area gate
    // redirects: no roster read is spent, because the roster needs tenant.read
    // and the member holds nothing.
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    window.history.replaceState({}, '', '/admin/tenants')
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: TENANT_ID, alias: TENANT_ID }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({ platform: [], tenant: [] } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const base = createTestSession(authenticatedSnapshot({ system: null }))
    const session: AuthSession = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    }
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    // The empty platform set redirects the member home; no admin chrome is
    // ever rendered.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
    expect(requested).toContain('/api/v1/authz/me/permissions')
    expect(requested).not.toContain('/api/v1/system/tenants')

    clearSelfTenants()
  })

  it('lands an invited member’s admin load back in their own tenant', async () => {
    // A member carrying a fresh invitation membership loads an admin URL
    // directly: the caller list settles the invited tenant, the platform set
    // is empty, and the redirect lands in the tenant area with that tenant
    // current. No roster read is spent on the way.
    const INVITED = 'invited-tenant'
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    window.history.replaceState({}, '', '/admin')
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: INVITED, alias: INVITED }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({ platform: [], tenant: [] } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const base = createTestSession(authenticatedSnapshot({ system: null }))
    const session: AuthSession = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
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
    await waitFor(() => expect(getCurrentTenantId()).toBe(INVITED))
    expect(requested).not.toContain('/api/v1/system/tenants')

    clearSelfTenants()
  })

  it('keeps a staff member whose only membership is personal in the admin', async () => {
    // A platform staff member belongs to no business tenant: the caller list
    // holds only their personal space. The admin area settles that row, the
    // guarded permission read carries it, and the non-empty platform set
    // grants the admin. The roster decides nothing here.
    const PERSONAL = 'personal-account-1'
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    window.history.replaceState({}, '', '/admin/settings')
    const tenantAtRead: (string | null)[] = []
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: PERSONAL, alias: PERSONAL, kind: 'personal' }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        tenantAtRead.push(getCurrentTenantId())
        return Promise.resolve({
          platform: ['tenant.read'],
          tenant: [],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const base = createTestSession(authenticatedSnapshot({ system: null }))
    const session: AuthSession = {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    }
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(tenantAtRead).toEqual([PERSONAL])
    expect(requested).not.toContain('/api/v1/system/tenants')

    clearSelfTenants()
  })

  it('sends a viewer whose address is unproved to the verification page', async () => {
    // A registration that has not proved its address settles no tenant: the
    // personal tenant's ensure refuses a pending account and the roster is
    // staff-only, so the platform permission set is never read and the area
    // would wait forever. The verification page is the one place that lifts
    // the state, so the admin URL lands there instead of on the wait.
    window.history.replaceState({}, '', '/admin/tenants')
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me') {
        return Promise.resolve({
          identifiers: [],
          password_set: false,
          verification: { state: 'pending' },
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => expect(window.location.pathname).toBe('/account'))
    expect(window.location.search).toBe('?verify=1')
    expect(
      screen.queryByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).not.toBeInTheDocument()

    // The seam is one global binding: leave no answer behind for the cases
    // that mount the app without scripting one.
    bindRequestFn(<T,>(): Promise<T> =>
      Promise.reject(new Error('no request is scripted')),
    )
  })

  it('admits the admin once a staff platform set lands', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const staff = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session: staff })

    // Unknown set: the shell-less placeholder, no admin.
    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()

    // A platform permission lands: the area admits the admin frame.
    act(() => {
      staff.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
  })

  it('redirects a member out once their empty set lands, never showing the shell', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const member = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session: member })
    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeInTheDocument()

    act(() => {
      member.set(authenticatedSnapshot({ system: [] }))
    })
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()
  })

  it('moves between admin pages without reloading, keeping the session', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({
          system: ['tenant.read', 'settings.read', 'audit.read'],
        }),
      ),
    })

    // The roster page is up inside the admin frame.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('tenants.title'),
      }),
    ).toBeInTheDocument()

    // Activate another page from the admin's own navigation.
    const nav = screen.getByRole('navigation', {
      name: tAdmin('shell.navLabel'),
    })
    await user.click(
      within(nav).getByRole('link', { name: tAdmin('settings.title') }),
    )

    // The settings page renders in place: the admin frame -- and the session
    // behind it -- survived, so the click did not reload the page and sign the
    // operator out to the sign-in surface.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('settings.title'),
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('renders the same brand in its top bar and the account entry in the nav drawer', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // The admin frame is a different shell (no tenant switcher), but its
    // banner is the same chrome the tenant frame carries: the brand in the
    // header's own type. ACCOUNT_ENTRY is 'both', so the account entry is the
    // row at the bottom of the navigation column and the header carries its
    // avatar menu too. The routed page's PageHeader is also a <header>
    // element, so scope to the banner that carries the brand.
    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header')
    expect(banner).not.toBeNull()
    expect(
      within(banner as HTMLElement).getByTestId('user-menu-trigger'),
    ).toBeInTheDocument()

    // The account entry carries the signed-in principal's identity; its menu
    // carries the notification centre, one entry per account pane and
    // sign-out.
    await user.click(screen.getByTestId('account-entry-trigger'))
    for (const name of [
      demoEnUS.userMenu.messages,
      demoEnUS.account.sections.profile,
      demoEnUS.account.sections.security,
      demoEnUS.account.sections.sessions,
      demoEnUS.userMenu.preferences,
      demoEnUS.userMenu.signOut,
    ]) {
      expect(screen.getByRole('menuitem', { name })).toBeInTheDocument()
    }
  })

  it('collapses the admin sidebar from the header control', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // The admin frame carries the same host-controlled collapse the tenant
    // frame does: the toggle is present, and collapsing narrows the permanent
    // drawer to the icon-only rail -- the platform navigation stays mounted
    // (named) rather than disappearing.
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.collapseNav') }),
    )
    expect(
      screen.getByRole('button', { name: tLayout('appShell.expandNav') }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).toBeInTheDocument()
  })

  it('keeps the sidebar collapse across the admin/tenant switch', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // Collapse in the admin area.
    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.collapseNav') }),
    )
    // Leave for the tenant area through the admin banner's account menu: the
    // sidebar is collapsed to the rail, so the menu's exit entry is the way
    // back.
    const menu = await openHeaderAccountMenu(user)
    await user.click(
      within(menu).getByRole('menuitem', { name: demoEnUS.admin.backToApp }),
    )
    // The tenant frame comes up already collapsed: one host-held state feeds
    // both areas, so the operator's collapse survives the switch rather than
    // resetting to open.
    expect(
      screen.getByRole('button', { name: tLayout('appShell.expandNav') }),
    ).toBeInTheDocument()
  })
})

describe('the organization admin path', () => {
  it("takes the viewer to the new tenant's home when a switch happens there", async () => {
    const user = userEvent.setup()
    // The organization admin surface acts on the current tenant and renders
    // inside the tenant shell, so the switcher is available there.
    window.history.replaceState({}, '', '/organization/admin')
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [
            { id: 'acme', alias: 'acme', kind: 'business' },
            { id: 'globex', alias: 'globex', kind: 'business' },
          ],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The account menu names the current organization first; expanding it
    // lists the others, the current one not repeated.
    const menu = await openHeaderAccountMenu(user)
    await user.click(within(menu).getByRole('menuitem', { name: 'acme' }))
    await user.click(
      await within(menu).findByRole('menuitem', { name: 'globex' }),
    )

    // The new tenant does not necessarily grant the old page's permissions,
    // so the host leaves the management surface for the tenant home rather
    // than staying on a page the new tenant may not offer.
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(getCurrentTenantId()).toBe('globex')
  })
})

describe('a staff viewer whose platform permission is not the dashboard code', () => {
  // The three tiers of the admin gate read the one platform permission set:
  // the header entry targets the first admin page whose code the principal
  // holds, the area gate admits any non-empty set, and each page's route guard
  // reads its own code. A principal whose platform set holds a permission
  // other than the dashboard's pins the three apart -- the set is non-empty
  // (so the area admits it) and some page's code is held (so the entry appears)
  // while the dashboard's own code is not (so the entry cannot point at the
  // admin root).

  /** A session whose platform set holds exactly these codes. */
  const staff = (...codes: readonly string[]) =>
    createTestSession(authenticatedSnapshot({ system: [...codes] }))

  it('offers an entry that targets the first admin page whose code they hold', async () => {
    window.history.replaceState({}, '', '/')
    settlePlatformOrganization()
    const user = userEvent.setup()
    renderDemoApp({ session: staff('audit.read') })

    // The dashboard's code (tenant.read) is unheld, so the entry cannot point
    // at the admin root; the audit page is the first labelled declaration
    // whose code the principal holds, and the entry targets it.
    const menu = await openHeaderAccountMenu(user)
    expect(
      within(menu).getByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toHaveAttribute('href', '/admin/audit')
  })

  it('admits the admin area, which keys on holding any platform permission', async () => {
    // A platform permission that no admin entry or page carries: the set is
    // non-empty, so the area gate admits the staff viewer, while every page's
    // own code stays unheld. A gate narrowed to any one code -- tenant.read or
    // a page's -- would bounce this viewer to the tenant home instead, so the
    // frame's presence is what tells the criteria apart.
    window.history.replaceState({}, '', '/admin/audit')
    renderDemoApp({ session: staff('tenant.write') })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/admin/audit')
    // The page's own code is unheld, so the route guard hides the page content
    // inside the frame the area gate admitted.
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
  })

  it('admits the page whose own code the principal holds', async () => {
    window.history.replaceState({}, '', '/admin/audit')
    renderDemoApp({ session: staff('audit.read') })

    // The area gate admits (any platform permission) and the audit page's own
    // code (audit.read) is held, so the page renders inside the frame: the two
    // tiers agree on a staff viewer who holds the page's code.
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('audit.title'),
      }),
    ).toBeInTheDocument()
  })

  it('turns them away from a page whose own code they lack, the frame intact', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({ session: staff('audit.read') })

    // The area admits them (any platform permission), but the roster page's
    // code (tenant.read) is unheld, so the route guard denies that page in
    // place: the admin frame stays -- this is the innermost tier, not a
    // whole-area denial -- and the page's own heading is replaced by the
    // no-permission surface.
    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: tAdmin('tenants.title'),
      }),
    ).not.toBeInTheDocument()
  })

  it('turns them away from the code-less tenant detail page too', async () => {
    // The tenant detail declaration carries no code of its own; the guard
    // resolves a page's permission from the selected (labelled) entry, so a
    // URL below the roster's path is held to the roster's tenant.read. A
    // viewer without it meets the no-permission surface inside the frame,
    // never the detail page.
    window.history.replaceState({}, '', '/admin/tenants/xyz')
    renderDemoApp({ session: staff('audit.read') })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
  })
})

describe('the tenant area header controls', () => {
  it('keeps the visible avatar glyph inside the account entry name', async () => {
    // The entry's glyph is visible text, so WCAG 2.5.3 requires it inside
    // the control's accessible name, together with the identity it stands
    // for. The entry sits at the bottom of the navigation column.
    const snapshot = authenticatedSnapshot()
    renderDemoApp({ session: createTestSession(snapshot) })
    const trigger = await screen.findByTestId('account-entry-trigger')
    const displayName = (snapshot.principal?.display_name ?? '').trim()
    const label = trigger.getAttribute('aria-label') ?? ''
    expect(displayName).not.toBe('')
    // The initials lead, the identity follows.
    expect(label.endsWith(displayName)).toBe(true)
    expect(label.length).toBeGreaterThan(displayName.length)
    expect((trigger.textContent ?? '').trim()).not.toBe('')
  })

  it('raises the header icon controls to the 44px floor under a coarse pointer', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    // The account-menu trigger shares the header row with the notification
    // bell; on a finger-driven device each one gates up to the 44x44 floor.
    const gated = [await screen.findByTestId('account-entry-trigger')]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('offers no language switch in the tenant header', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The language entry lives on the account preferences page alone. The
    // header carries its account menu only -- the organization switcher rides
    // inside that menu now -- so a second menu trigger is what a removed
    // language switch would be.
    const [banner] = await screen.findAllByRole('banner')
    const menus = within(banner as HTMLElement)
      .getAllByRole('button')
      .filter((button) => button.getAttribute('aria-haspopup') === 'menu')
    expect(menus).toHaveLength(1)
    expect(
      within(banner as HTMLElement).getByTestId('user-menu-trigger'),
    ).toBeInTheDocument()
  })

  it('offers only the tenants the seeded deployment registers', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The seeded roster registers acme alone, so the switcher must not
    // offer a second tenant: switching to one the server cannot resolve
    // would leave every tenant-scoped read refused 404.
    const menu = await openHeaderAccountMenu(user)
    expect(within(menu).getByText('acme')).toBeInTheDocument()
    expect(
      within(menu).queryByRole('menuitem', { name: 'Globex Corp.' }),
    ).toBeNull()
    expect(
      within(menu).queryByRole('group', {
        name: demoEnUS.userMenu.organizations,
      }),
    ).toBeNull()
    expect(getCurrentTenantId()).toBe('acme')
  })

  it('collapses and expands the sidebar from the header control', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The desktop sidebar collapse toggle is present (the host supplies the
    // collapse props); collapsing removes the permanent drawer and the
    // toggle now offers to expand.
    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.collapseNav') }),
    )
    expect(
      screen.getByRole('button', { name: tLayout('appShell.expandNav') }),
    ).toBeInTheDocument()
  })

})

describe('the tenant navigation', () => {
  it('leaves a modified click on an entry to the browser', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const entry = await screen.findByRole('link', { name: demoEnUS.nav.home })

    // A Ctrl/⌘ click belongs to the browser (a new tab), so the app must
    // neither navigate in place nor suppress the default action. This
    // document-level listener runs after the app's handler (React listens on
    // the root container), records whether the app prevented the default,
    // then cancels the navigation jsdom cannot carry out.
    let appPreventedDefault: boolean | null = null
    const recordAndCancel = (event: MouseEvent): void => {
      appPreventedDefault = event.defaultPrevented
      event.preventDefault()
    }
    document.addEventListener('click', recordAndCancel, { once: true })
    fireEvent.click(entry, { ctrlKey: true })

    expect(appPreventedDefault).toBe(false)
    expect(window.location.pathname).toBe('/')
  })
})

describe('the tenant navigation icons', () => {
  // The navigation column carries the home entry alone; the notification
  // centre and the account settings are entries of the account menu now.
  const CASES = [
    {
      language: 'en-US',
      labels: [demoEnUS.nav.home],
      collapse: tLayout('appShell.collapseNav'),
      open: tLayout('appShell.openNav'),
    },
    {
      language: 'zh-CN',
      labels: [demoZhCN.nav.home],
      collapse: tLayoutZh('appShell.collapseNav'),
      open: tLayoutZh('appShell.openNav'),
    },
  ] as const

  afterEach(() => {
    // The default stub (the desktop branch) is installed once per file;
    // restore it after the narrow-width case.
    mockMatchMedia(true)
  })

  it.each(CASES)(
    'keeps an icon and a tooltip on every collapsed entry ($language)',
    async ({ language, labels, collapse }) => {
      mockMatchMedia(true)
      const user = userEvent.setup()
      renderDemoApp({
        session: createTestSession(authenticatedSnapshot()),
        language,
      })

      // The navigation is derived from the route table; wait for it.
      await screen.findByRole('link', { name: labels[0] })
      await user.click(screen.getByRole('button', { name: collapse }))

      for (const label of labels) {
        const link = screen.getByRole('link', { name: label })
        // The rail's contract: an entry with an icon shows the icon and
        // hides (clips) the label, which remains the accessible name.
        expect(
          link.querySelector(`.${listItemIconClasses.root} svg`),
        ).not.toBeNull()
        // The clip recipe lives on the ListItemText root.
        const textRoot = link.querySelector(`.${listItemTextClasses.root}`)
        expect(textRoot).toHaveStyle({ position: 'absolute' })
      }

      // Every clipped entry is named in full on hover.
      for (const label of labels) {
        await user.hover(screen.getByRole('link', { name: label }))
        expect(await screen.findByRole('tooltip')).toHaveTextContent(label)
      }
    },
  )

  it('leaves the temporary drawer at narrow widths with icons beside visible labels', async () => {
    mockMatchMedia(false)
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.openNav') }),
    )

    for (const label of [demoEnUS.nav.home]) {
      const link = screen.getByRole('link', { name: label })
      expect(link.querySelector(`.${listItemIconClasses.root} svg`)).not.toBeNull()
      const textRoot = link.querySelector(`.${listItemTextClasses.root}`)
      expect(textRoot).not.toHaveStyle({ position: 'absolute' })
    }
  })
})

describe('the not-found fallback', () => {
  it('renders for the object page the skeleton no longer ships', async () => {
    // The object store is a backend capability, not an end-user page: no
    // route declares /objects, so a direct visit is the same dead link any
    // unregistered path is (and no navigation entry or home card leads
    // there).
    window.history.replaceState({}, '', '/objects')
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: demoEnUS.notFound.home }),
    ).toHaveAttribute('href', '/')
  })

  it('explains the dead link in the user terms and offers a way home', async () => {
    window.history.replaceState({}, '', '/admin/definitely-not-a-page')
    // A platform permission admits the admin frame; the path itself
    // declares no page, so the frame renders the not-found fallback.
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
    // The body speaks to the user, not about the router's registration
    // table; the address stays on the page as a secondary line.
    expect(screen.getByText(demoEnUS.notFound.body)).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: demoEnUS.notFound.home }),
    ).toHaveAttribute('href', '/')
    expect(
      screen.getByText(
        demoEnUS.notFound.path.replace(
          '{{path}}',
          '/admin/definitely-not-a-page',
        ),
      ),
    ).toBeInTheDocument()
  })

  it('shows the requested address verbatim, with no HTML entities', async () => {
    const path = '/admin/a&b'
    window.history.replaceState({}, '', path)
    const { container } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // React escapes the text node; the interpolation layer must not escape
    // it a second time ("/" becomes "&#x2F;", "&" becomes "&amp;").
    expect(
      await screen.findByText(demoEnUS.notFound.path.replace('{{path}}', path)),
    ).toBeInTheDocument()
    expect(container.textContent).not.toContain('&#x2F;')
    expect(container.textContent).not.toContain('&amp;')
  })
})

describe('the account page', () => {
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

  /** Reads the theme the provider is currently applying. It renders inside
   * the provider stack (the harness's `wrap` slot), so a preset switch is
   * observable without reaching into provider internals. */
  function ThemeRadiusProbe() {
    const theme = useTheme()
    return <span data-testid="theme-radius">{String(theme.shape.borderRadius)}</span>
  }

  /** The theme options the production assembly passes (src/main.tsx): the
   * shipped shelf, starting on the device's stored choice (else the
   * deployment default, else 'default'). */
  function bootThemeOptions() {
    return { presets: themePresets, defaultPreset: initialThemePreset() }
  }

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('mounts the account surfaces in the tenant shell at /account', async () => {
    window.history.replaceState({}, '', '/account')
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The group address lands on the first pane, whose h1 names it.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.sections.profile,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/account/profile')
  })

  it('starts an account pane breadcrumb at the display name, without linking it', async () => {
    window.history.replaceState({}, '', '/account/profile')
    const session = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session })
    const displayName = session.getSnapshot().principal?.display_name ?? ''

    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.profile,
    })
    // The trail names the account first, by the same display name the
    // shell's account entry shows. It is not a destination -- there is no
    // account home page above the panes -- and the pane names the current
    // step.
    const trail = within(
      await screen.findByRole('navigation', { name: 'Breadcrumb' }),
    )
    expect(trail.getByText(displayName)).toBeInTheDocument()
    expect(trail.queryByRole('link', { name: displayName })).toBeNull()
    expect(
      trail.getByText(demoEnUS.account.sections.profile),
    ).toBeInTheDocument()
  })

  it('falls back to the username when the account has no display name', async () => {
    window.history.replaceState({}, '', '/account/profile')
    const username = 'ada'
    const base = createBackend('data')
    bindRequestFn(<T,>(url: string): Promise<T> => {
      if (!url.includes('/api/v1/auth/me')) {
        return base<T>(url)
      }
      return base<T>(url).then(
        (body) =>
          ({
            ...(body as Record<string, unknown>),
            identifiers: [
              {
                id: 'username-1',
                kind: 'username',
                value: username,
                proven: true,
                contact: false,
                default_contact: false,
                sign_in: true,
                verified_at: null,
                verified_via: null,
              },
            ],
          }) as T,
      )
    })
    const snapshot = authenticatedSnapshot()
    renderDemoApp({
      session: createTestSession({
        ...snapshot,
        principal:
          snapshot.principal === null
            ? null
            : { ...snapshot.principal, display_name: '' },
      }),
    })

    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.account.sections.profile,
    })
    const trail = within(
      await screen.findByRole('navigation', { name: 'Breadcrumb' }),
    )
    expect(trail.getByText(username)).toBeInTheDocument()
  })

  it('offers the appearance section ahead of the preferences section', async () => {
    window.history.replaceState({}, '', '/account?section=preferences')
    bindRequestFn(createBackend('data'))
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      theme: bootThemeOptions(),
    })

    // Both controls are on the pane from the start: the theme picker comes
    // before the language and time-zone writes, so a user meets the theme
    // control first.
    const theme = await screen.findByRole('combobox', {
      name: tAccount('appearance.theme.label'),
    })
    const language = await screen.findByRole('combobox', {
      name: tAccount('preferences.language.label'),
    })
    expect(
      theme.compareDocumentPosition(language) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('keeps the language dropdown on the preferences pane, not the header', async () => {
    window.history.replaceState({}, '', '/account?section=preferences')
    bindRequestFn(createBackend('data'))
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The preferences pane is the one place the language is set...
    expect(
      await screen.findByRole('combobox', {
        name: tAccount('preferences.language.label'),
      }),
    ).toBeInTheDocument()
    // ...and the tenant header carries no language switch: its one menu is
    // the account menu, with the organization switcher inside it.
    const [banner] = screen.getAllByRole('banner')
    const menus = within(banner as HTMLElement)
      .getAllByRole('button')
      .filter((button) => button.getAttribute('aria-haspopup') === 'menu')
    expect(menus).toHaveLength(1)
    expect(
      within(banner as HTMLElement).getByTestId('user-menu-trigger'),
    ).toBeInTheDocument()
  })

  it('applies a chosen preset at once, remembers it, and restores it on a remount', async () => {
    const entries = installStorageDouble()
    window.history.replaceState({}, '', '/account?section=preferences')
    const user = userEvent.setup()
    const first = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      theme: bootThemeOptions(),
      wrap: (app) => (
        <>
          {app}
          <ThemeRadiusProbe />
        </>
      ),
    })

    const select = await screen.findByRole('combobox', {
      name: tAccount('appearance.theme.label'),
    })
    await user.click(select)
    await user.click(
      await screen.findByRole('option', { name: demoEnUS.themePreset.glass }),
    )

    // The provider applies the preset immediately: the theme's radius is the
    // glass preset's, not the default one, before any reload.
    expect(screen.getByTestId('theme-radius')).toHaveTextContent(
      String(glassThemePreset.tokens.shape?.borderRadius),
    )
    // The pick is a manual choice, so it lands on the device too.
    expect(entries.get(THEME_PRESET_STORAGE_KEY)).toBe('glass')

    // A reload starts from the stored choice: remounting with the same boot
    // options (whose defaultPreset reads the storage) applies glass again.
    first.unmount()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      theme: bootThemeOptions(),
      wrap: (app) => (
        <>
          {app}
          <ThemeRadiusProbe />
        </>
      ),
    })
    expect(await screen.findByTestId('theme-radius')).toHaveTextContent(
      String(glassThemePreset.tokens.shape?.borderRadius),
    )
    // The remounted page offers the control again, already on the restored
    // choice.
    expect(
      screen.getByRole('combobox', {
        name: tAccount('appearance.theme.label'),
      }),
    ).toHaveTextContent(demoEnUS.themePreset.glass)
  })
})

describe('the sidebar shell layout', () => {
  it('names the account entry by its glyph and identity in the side column', async () => {
    // The account entry lives at the bottom of the navigation column; the
    // visible glyph is text inside the control, so WCAG 2.5.3 holds.
    const snapshot = authenticatedSnapshot()
    renderDemoApp({
      session: createTestSession(snapshot),
      shellLayout: 'sidebar',
    })
    const trigger = await screen.findByTestId('account-entry-trigger')
    const displayName = (snapshot.principal?.display_name ?? '').trim()
    const label = trigger.getAttribute('aria-label') ?? ''
    expect(displayName).not.toBe('')
    expect(label.endsWith(displayName)).toBe(true)
    expect(label.length).toBeGreaterThan(displayName.length)
  })

  it('renders the brand and the account controls in the side column, with no banner landmark', async () => {
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
      shellLayout: 'sidebar',
    })

    const brand = await screen.findByText(demoEnUS.app.title)
    const paper = document.querySelector<HTMLElement>(`.${drawerClasses.paper}`)
    expect(paper).not.toBeNull()
    const nav = screen.getByRole('navigation', {
      name: tLayout('appShell.navLabel'),
    })
    const account = screen.getByTestId('account-entry-trigger')

    // The desktop sidebar form renders no app bar: the brand sits in the
    // drawer paper rather than inside a header landmark. (The routed page
    // carries its own PageHeader element in the content region, so the
    // absence is asserted on the brand's containing element, not on every
    // header in the document.)
    expect(brand.closest('header')).toBeNull()
    // The brand and the account entry live in the drawer paper, outside the
    // nav landmark: the brand before it, the entry after it at the bottom.
    expect(paper).toContainElement(brand)
    expect(paper).toContainElement(account)
    expect(nav.contains(brand)).toBe(false)
    expect(nav.contains(account)).toBe(false)
    expect(
      brand.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      nav.compareDocumentPosition(account) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('carries the same side column into the admin area', async () => {
    // The admin is the app's second shell. Its area receives the layout
    // from AppFrame and forwards it to AdminShell; dropping either hop
    // leaves /admin on the top-bar form while the tenant area is a
    // sidebar, which only a full-route render can catch.
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
      shellLayout: 'sidebar',
    })

    const nav = await screen.findByRole('navigation', {
      name: tAdmin('shell.navLabel'),
    })
    const brand = screen.getByText(demoEnUS.app.title)
    // No app bar in the sidebar form. The routed page renders its own
    // `<header>` (its PageHeader) inside main, so the absence is asserted
    // on the brand's containing element, not on every header in the page.
    expect(brand.closest('header')).toBeNull()
    // The account entry is the row at the bottom of the column, after the
    // navigation, as in the tenant frame.
    const account = screen.getByTestId('account-entry-trigger')
    expect(nav.contains(brand)).toBe(false)
    expect(nav.contains(account)).toBe(false)
    expect(
      brand.compareDocumentPosition(nav) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      nav.compareDocumentPosition(account) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })
})

/**
 * The shell's callback-fragment fallback: a refused redirect round trip
 * lands with its problem code in the URL fragment, and the surface that
 * started the flow reads it. When no surface does -- an authenticated viewer
 * at the root after a login refusal, for one -- the tenant frame presents it
 * once instead of dropping the reason.
 */
describe('an unread callback fragment', () => {
  const LOGIN_FAILED = 'authn_error=login-failed'
  const dismissLabel = demoEnUS.callbackNotice.dismiss

  it('presents a landing no surface consumed as a one-time notice', async () => {
    window.history.replaceState({}, '', `/#${LOGIN_FAILED}`)
    bindRequestFn(createBackend('data'))
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ features: readyFeatures() }),
      ),
    })

    // The sign-in surface (which consumes the fragment) is not mounted for
    // an authenticated viewer, so the frame words the code once.
    expect(
      await screen.findByText(tAccount('errors.authn.login_failed')),
    ).toBeInTheDocument()
    // The fragment is one-shot: a reload does not repeat the notice.
    expect(window.location.hash).toBe('')

    await userEvent.setup().click(
      screen.getByRole('button', { name: dismissLabel }),
    )
    await waitFor(() =>
      expect(
        screen.queryByText(tAccount('errors.authn.login_failed')),
      ).toBeNull(),
    )
  })

  it('leaves the notice to the sign-in surface when it consumes the fragment', async () => {
    window.history.replaceState({}, '', `/#${LOGIN_FAILED}`)
    bindRequestFn(createBackend('data'))
    renderDemoApp()

    // The anonymous sign-in surface reads the code and words it in its own
    // banner; the frame adds no second notice.
    expect(
      await screen.findByText(tAuth('errors.authn.login_failed')),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: dismissLabel }),
    ).toBeNull()
  })

  it('leaves the notice to the admin area when it consumes the fragment', async () => {
    window.history.replaceState({}, '', `/admin/tenants#${LOGIN_FAILED}`)
    bindRequestFn(createBackend('data'))
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    // The admin area reads the fragment the page it hosts started, and
    // words it above the page; no tenant-frame notice exists on this path.
    expect(
      await screen.findByText(tAdmin('errors.authn.login_failed')),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: dismissLabel }),
    ).toBeInTheDocument()
  })
})

/**
 * The admin area's reads: an unknown value is never a conclusion. A failed
 * permission read or profile read leaves the decision unknown and offers a
 * retry; only an ANSWERED document (an empty permission pair included) or a
 * READ pending verification state decides.
 */
describe('a failed admin access read', () => {
  /** The attach the production assembly uses, over a session whose sets the
   * gate reads. */
  function attachedSession(base: ReturnType<typeof createTestSession>) {
    return {
      ...base,
      setPermissionSet: (domain: AuthDomain, perms: readonly string[] | null) => {
        const current = base.getSnapshot()
        base.set({
          ...current,
          permissionSets: { ...current.permissionSets, [domain]: perms },
        })
      },
    } satisfies AuthSession
  }

  it('keeps a failed permission read unknown and retries it before deciding', async () => {
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    clearSelfTenants()
    window.history.replaceState({}, '', '/admin/settings')
    const backend = createBackend('data')
    let failing = true
    let permissionReads = 0
    bindRequestFn(<T,>(path: string, options?: RequestOptions): Promise<T> => {
      if (path === '/api/v1/authz/me/permissions') {
        permissionReads += 1
        return failing
          ? Promise.reject(Object.assign(new Error('unavailable'), { status: 500 }))
          : Promise.resolve({ platform: ['tenant.read'], tenant: [] } as T)
      }
      if (path === '/api/v1/auth/me') {
        // A proved account: the gate's verification branch is not the case
        // under test here.
        return Promise.resolve({
          account_id: 'account-1',
          identifiers: [],
          verification: { state: 'verified', email: 'a@x.test', since: '' },
        } as T)
      }
      return backend(path, options) as Promise<T>
    })
    const session = attachedSession(
      createTestSession(authenticatedSnapshot({ system: null })),
    )
    renderDemoApp({
      session,
      wrap: (app) => (
        <PlatformPermissions session={session}>{app}</PlatformPermissions>
      ),
    })

    // The failure is not an empty set: the area shows the failure and a
    // retry, and does not redirect the staff member out as a member.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      demoEnUS.admin.accessFailed,
    )
    expect(window.location.pathname).toBe('/admin/settings')
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).not.toBeInTheDocument()

    // The read is retried against a recovered service; the area then decides
    // by the set.
    failing = false
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: demoEnUS.admin.accessRetry }))

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(permissionReads).toBeGreaterThanOrEqual(2)
  })

  it('does not read a failed profile as a verified account, and the retry decides', async () => {
    // The permission set stays unknown (no attach in this suite), so the area
    // reaches its verification branch. A failed profile read says nothing
    // about the verification state; the pending answer is the one that
    // redirects.
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    clearSelfTenants()
    window.history.replaceState({}, '', '/admin/settings')
    const backend = createBackend('data')
    const pendingProfile = {
      identifiers: [],
      password_set: false,
      verification: { state: 'pending' },
    }
    let profileFailing = true
    bindRequestFn(<T,>(path: string, options?: RequestOptions): Promise<T> => {
      if (path === '/api/v1/auth/me') {
        return profileFailing
          ? Promise.reject(new Error('the profile is unavailable'))
          : Promise.resolve(pendingProfile as T)
      }
      return backend(path, options) as Promise<T>
    })
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: null })),
    })

    expect(await screen.findByRole('alert')).toHaveTextContent(
      demoEnUS.admin.accessFailed,
    )
    expect(window.location.pathname).toBe('/admin/settings')

    profileFailing = false
    await userEvent
      .setup()
      .click(screen.getByRole('button', { name: demoEnUS.admin.accessRetry }))

    // The retried read answers a pending verification state: the unproved
    // account is the case the verification page exists for.
    await waitFor(() =>
      expect(window.location.pathname).toBe('/account'),
    )
  })
})
