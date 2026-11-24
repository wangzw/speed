import type { ReactNode } from 'react'
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
import { AUTH_CALLBACK_PATH, PASSWORD_RESET_PATH } from './routes.js'
import { profileWithVerification } from './test-utils/profile.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'
import { getAuthnProfileGetQueryKey } from './app-api/index.js'
import {
  getCurrentTenant,
  initialThemePreset,
  setCurrentTenant,
  setTenantNames,
  THEME_PRESET_STORAGE_KEY,
} from './chrome.js'
import { PlatformPermissions } from './platform-permissions.js'
import { clearSelfTenants, loadSelfTenants } from './tenant-self.js'

const displayTimeZones = vi.hoisted(() => ({
  values: [] as (string | undefined)[],
}))

vi.mock('@speed/ui-kit', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@speed/ui-kit')>()
  const { createElement } = await import('react')
  return {
    ...actual,
    DisplayTimeZoneProvider: ({
      timeZone,
      children,
    }: {
      readonly timeZone?: string
      readonly children?: ReactNode
    }) => {
      displayTimeZones.values.push(timeZone)
      return createElement(
        actual.DisplayTimeZoneProvider,
        { timeZone },
        children,
      )
    },
  }
})

const TENANT_ID = 'acme'

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

async function openHeaderAccountMenu(
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

function settlePlatformOrganization(): void {
  setCurrentTenant('system')
  setTenantNames(new Map([['system', 'system']]))
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenant(TENANT_ID)
  setTenantNames(new Map([[TENANT_ID, TENANT_ID]]))
  clearSelfTenants()
})

describe('the render harness', () => {
  it("renders under the assembly's query client, read policy included", () => {
    const { queryClient, unmount } = renderDemoApp()

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

    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      await screen.findByLabelText(tAuth('passwordSignIn.identifierLabel')),
    ).toBeInTheDocument()
    expect(screen.queryByText(demoEnUS.home.heading)).not.toBeInTheDocument()
  })

  it('carries the brand in the banner as a non-heading', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header')
    expect(banner).not.toBeNull()
    expect(brand.tagName).not.toBe('H6')
    expect(
      within(banner as HTMLElement).queryByRole('heading'),
    ).not.toBeInTheDocument()
  })

  it('offers only the password channel, with no tab strip', async () => {
    renderDemoApp()
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

    const nav = screen.getByRole('navigation')
    expect(
      within(nav).queryByRole('link', { name: demoEnUS.nav.admin }),
    ).not.toBeInTheDocument()

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

    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.admin }),
    ).not.toBeInTheDocument()
    const menu = await openHeaderAccountMenu(user)
    expect(
      within(menu).queryByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toBeNull()
  })

  it('keeps the admin entry hidden while the platform permission set is unknown', async () => {
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
    const session = createTestSession(
      authenticatedSnapshot({ system: ['tenant.read'] }),
    )
    renderDemoApp({ session })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()

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
    setCurrentTenant(null)
    window.history.replaceState({}, '', '/admin/tenants')
    const requested: string[] = []
    const tenantAtRead: (string | null)[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: TENANT_ID, name: 'acme' }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        tenantAtRead.push(getCurrentTenant())
        return Promise.resolve({
          platform: ['tenant.read'],
          tenant: [],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    const base = createTestSession(authenticatedSnapshot())
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
    expect(tenantAtRead).toEqual([TENANT_ID])
    expect(requested).toContain('/api/v1/auth/me/tenants')
    expect(
      requested.some((path) => path.includes('/orgs/personal')),
      'the app must not call the retired personal-organization ensure route',
    ).toBe(false)
  })

  it('settles the caller list when the deployment serves no roster', async () => {
    setCurrentTenant(null)
    setTenantNames(new Map())
    window.history.replaceState({}, '', '/admin/settings')
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: TENANT_ID, name: TENANT_ID }],
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
    expect(requested).not.toContain('/api/v1/system/tenants')

    clearSelfTenants()
  })

  it('turns a member who reaches an admin page directly out of the admin entirely', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: tAdmin('tenants.title') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(tUiKit('emptyState.noPermission.title')),
    ).not.toBeInTheDocument()

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
    setCurrentTenant(null)
    setTenantNames(new Map())
    window.history.replaceState({}, '', '/admin/tenants')
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: TENANT_ID, name: TENANT_ID }],
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
    const INVITED = 'invited-tenant'
    setCurrentTenant(null)
    setTenantNames(new Map())
    window.history.replaceState({}, '', '/admin')
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ id: INVITED, name: INVITED }],
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
    await waitFor(() => expect(getCurrentTenant()).toBe(INVITED))
    expect(requested).not.toContain('/api/v1/system/tenants')

    clearSelfTenants()
  })

  it('keeps a staff member whose only membership is personal in the admin', async () => {
    const PERSONAL = 'my-space'
    setCurrentTenant(null)
    setTenantNames(new Map())
    window.history.replaceState({}, '', '/admin/settings')
    const tenantAtRead: (string | null)[] = []
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [{ name: PERSONAL, kind: 'personal' }],
        } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        tenantAtRead.push(getCurrentTenant())
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

    bindRequestFn(<T,>(): Promise<T> =>
      Promise.reject(new Error('no request is scripted')),
    )
  })

  it('admits the admin once a staff platform set lands', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const staff = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session: staff })

    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.admin.accessPending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).not.toBeInTheDocument()

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

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tAdmin('tenants.title'),
      }),
    ).toBeInTheDocument()

    const nav = screen.getByRole('navigation', {
      name: tAdmin('shell.navLabel'),
    })
    await user.click(
      within(nav).getByRole('link', { name: tAdmin('settings.title') }),
    )

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

    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header')
    expect(banner).not.toBeNull()
    expect(
      within(banner as HTMLElement).getByTestId('user-menu-trigger'),
    ).toBeInTheDocument()

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

    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.collapseNav') }),
    )
    const menu = await openHeaderAccountMenu(user)
    await user.click(
      within(menu).getByRole('menuitem', { name: demoEnUS.admin.backToApp }),
    )
    expect(
      screen.getByRole('button', { name: tLayout('appShell.expandNav') }),
    ).toBeInTheDocument()
  })
})

describe('the organization admin path', () => {
  it("takes the viewer to the new tenant's home when a switch happens there", async () => {
    const user = userEvent.setup()
    window.history.replaceState({}, '', '/organization/admin')
    bindRequestFn(<T,>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({
          tenants: [
            { name: 'acme', kind: 'business' },
            { name: 'globex', kind: 'business' },
          ],
        } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    const menu = await openHeaderAccountMenu(user)
    await user.click(within(menu).getByRole('menuitem', { name: 'acme' }))
    await user.click(
      await within(menu).findByRole('menuitem', { name: 'globex' }),
    )

    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(getCurrentTenant()).toBe('globex')
  })
})

describe('a staff viewer whose platform permission is not the dashboard code', () => {

  const staff = (...codes: readonly string[]) =>
    createTestSession(authenticatedSnapshot({ system: [...codes] }))

  it('offers an entry that targets the first admin page whose code they hold', async () => {
    window.history.replaceState({}, '', '/')
    settlePlatformOrganization()
    const user = userEvent.setup()
    renderDemoApp({ session: staff('audit.read') })

    const menu = await openHeaderAccountMenu(user)
    expect(
      within(menu).getByRole('menuitem', { name: demoEnUS.nav.admin }),
    ).toHaveAttribute('href', '/admin/audit')
  })

  it('admits the admin area, which keys on holding any platform permission', async () => {
    window.history.replaceState({}, '', '/admin/audit')
    renderDemoApp({ session: staff('tenant.write') })

    expect(
      await screen.findByRole('navigation', {
        name: tAdmin('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/admin/audit')
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
  })

  it('admits the page whose own code the principal holds', async () => {
    window.history.replaceState({}, '', '/admin/audit')
    renderDemoApp({ session: staff('audit.read') })

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
    const snapshot = authenticatedSnapshot()
    renderDemoApp({ session: createTestSession(snapshot) })
    const trigger = await screen.findByTestId('account-entry-trigger')
    const displayName = (snapshot.principal?.display_name ?? '').trim()
    const label = trigger.getAttribute('aria-label') ?? ''
    expect(displayName).not.toBe('')
    expect(label.endsWith(displayName)).toBe(true)
    expect(label.length).toBeGreaterThan(displayName.length)
    expect((trigger.textContent ?? '').trim()).not.toBe('')
  })

  it('raises the header icon controls to the 44px floor under a coarse pointer', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const gated = [await screen.findByTestId('account-entry-trigger')]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('offers no language switch in the tenant header', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => {
      const [banner] = screen.getAllByRole('banner')
      const menus = within(banner as HTMLElement)
        .getAllByRole('button')
        .filter((button) => button.getAttribute('aria-haspopup') === 'menu')
      expect(menus).toHaveLength(1)
    })
    const [banner] = screen.getAllByRole('banner')
    expect(
      within(banner as HTMLElement).getByTestId('user-menu-trigger'),
    ).toBeInTheDocument()
  })

  it('offers only the tenants the seeded deployment registers', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

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
    expect(getCurrentTenant()).toBe('acme')
  })

  it('collapses and expands the sidebar from the header control', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

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

      await screen.findByRole('link', { name: labels[0] })
      await user.click(screen.getByRole('button', { name: collapse }))

      for (const label of labels) {
        const link = screen.getByRole('link', { name: label })
        expect(
          link.querySelector(`.${listItemIconClasses.root} svg`),
        ).not.toBeNull()
        const textRoot = link.querySelector(`.${listItemTextClasses.root}`)
        expect(textRoot).toHaveStyle({ position: 'absolute' })
      }

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
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.notFound.heading,
      }),
    ).toBeInTheDocument()
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

    expect(
      await screen.findByText(demoEnUS.notFound.path.replace('{{path}}', path)),
    ).toBeInTheDocument()
    expect(container.textContent).not.toContain('&#x2F;')
    expect(container.textContent).not.toContain('&amp;')
  })
})

describe('the account page', () => {
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

  function ThemeRadiusProbe() {
    const theme = useTheme()
    return <span data-testid="theme-radius">{String(theme.shape.borderRadius)}</span>
  }

  function bootThemeOptions() {
    return { presets: themePresets, defaultPreset: initialThemePreset() }
  }

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('mounts the account surfaces in the tenant shell at /account', async () => {
    window.history.replaceState({}, '', '/account')
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

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
                proven: false,
                contact: false,
                default_contact: false,
                sign_in: true,
                verified_at: '2026-01-01T00:00:00Z',
                verified_via: 'self',
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

    expect(
      await screen.findByRole('combobox', {
        name: tAccount('preferences.language.label'),
      }),
    ).toBeInTheDocument()
    await waitFor(() => {
      const [banner] = screen.getAllByRole('banner')
      const menus = within(banner as HTMLElement)
        .getAllByRole('button')
        .filter((button) => button.getAttribute('aria-haspopup') === 'menu')
      expect(menus).toHaveLength(1)
    })
    const [banner] = screen.getAllByRole('banner')
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

    expect(screen.getByTestId('theme-radius')).toHaveTextContent(
      String(glassThemePreset.tokens.shape?.borderRadius),
    )
    expect(entries.get(THEME_PRESET_STORAGE_KEY)).toBe('glass')

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
    expect(
      screen.getByRole('combobox', {
        name: tAccount('appearance.theme.label'),
      }),
    ).toHaveTextContent(demoEnUS.themePreset.glass)
  })
})

describe('the sidebar shell layout', () => {
  it('names the account entry by its glyph and identity in the side column', async () => {
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

    expect(brand.closest('header')).toBeNull()
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
    expect(brand.closest('header')).toBeNull()
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

    expect(
      await screen.findByText(tAccount('errors.authn.login_failed')),
    ).toBeInTheDocument()
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

    expect(
      await screen.findByText(tAdmin('errors.authn.login_failed')),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: dismissLabel }),
    ).toBeInTheDocument()
  })
})

describe('a failed admin access read', () => {
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
    setCurrentTenant(null)
    setTenantNames(new Map())
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
        return Promise.resolve({
          account_id: 'account-1',
          identifiers: [],
          verification: { state: 'verified', email: 'a@x.test', since: null },
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
    setCurrentTenant(null)
    setTenantNames(new Map())
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

    await waitFor(() =>
      expect(window.location.pathname).toBe('/account'),
    )
  })
})

describe('the display time zone', () => {
  beforeEach(() => {
    displayTimeZones.values.length = 0
  })

  function backendWithTimeZone(zone: string) {
    const backend = createBackend('data')
    return <T,>(path: string, options?: RequestOptions): Promise<T> => {
      if (path !== '/api/v1/auth/me') {
        return backend<T>(path, options)
      }
      return backend<T>(path, options).then(
        (body) =>
          ({
            ...(body as unknown as Record<string, unknown>),
            time_zone: zone,
          }) as T,
      )
    }
  }

  function lastDisplayTimeZone(): string | undefined {
    return displayTimeZones.values.at(-1)
  }

  it('carries the profile’s stored zone into the tenant and the admin shells', async () => {
    bindRequestFn(backendWithTimeZone('Asia/Tokyo'))

    const tenant = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })
    await waitFor(() => expect(lastDisplayTimeZone()).toBe('Asia/Tokyo'))
    tenant.unmount()

    displayTimeZones.values.length = 0
    window.history.replaceState({}, '', '/admin/tenants')
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })
    expect(
      await screen.findByRole('navigation', { name: tAdmin('shell.navLabel') }),
    ).toBeInTheDocument()
    await waitFor(() => expect(lastDisplayTimeZone()).toBe('Asia/Tokyo'))
  })

  it('re-renders when the stored zone changes, and leaves an unset zone to the browser', async () => {
    bindRequestFn(backendWithTimeZone('Asia/Tokyo'))
    const { queryClient } = renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })
    await waitFor(() => expect(lastDisplayTimeZone()).toBe('Asia/Tokyo'))

    act(() => {
      queryClient.setQueryData(getAuthnProfileGetQueryKey(), {
        ...profileWithVerification('none', ''),
        time_zone: 'Europe/Berlin',
      })
    })
    await waitFor(() => expect(lastDisplayTimeZone()).toBe('Europe/Berlin'))

    act(() => {
      queryClient.setQueryData(getAuthnProfileGetQueryKey(), {
        ...profileWithVerification('none', ''),
        time_zone: '',
      })
    })
    await waitFor(() => expect(lastDisplayTimeZone()).toBeUndefined())
  })

  it('reads no profile on an anonymous public page and leaves the zone to the browser', async () => {
    const requested: string[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    window.history.replaceState({}, '', PASSWORD_RESET_PATH)
    renderDemoApp()

    expect(displayTimeZones.values.length).toBeGreaterThan(0)
    expect(displayTimeZones.values.every((value) => value === undefined)).toBe(
      true,
    )
    expect(requested).not.toContain('/api/v1/auth/me')
  })
})
