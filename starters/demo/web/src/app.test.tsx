/**
 * The app composition: the tenant shell's anonymous and authenticated branches,
 * the host's console entry, and the console area's session gate and route
 * guard.
 *
 * The tenant shell fail-closes to the host's sign-in surface -- the host's
 * chrome around auth-ui's password channel, the only channel the skeleton
 * offers. Once authenticated the frame appears with the host's console entry,
 * which lands the viewer in the console area. The console area carries the same
 * session gate (an anonymous viewer meets the sign-in surface, never a console
 * whose requests cannot answer) and a route guard over each page's permission
 * code. Its three tiers read the one platform permission set: the entry
 * targets the first console page whose code the principal holds, the area gate
 * on any non-empty set, and each page's guard on its own code. A staff viewer
 * whose set holds a permission other than the dashboard's reads the three
 * apart.
 */

import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { listItemIconClasses } from '@mui/material/ListItemIcon'
import { listItemTextClasses } from '@mui/material/ListItemText'
import type { AuthDomain, AuthSession } from '@speed/auth-core'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { AUTH_UI_NAMESPACE } from '@speed/auth-ui'
import {
  CONSOLE_UI_NAMESPACE,
  consoleUiResources,
} from '@speed/console-ui'
import { LAYOUT_KIT_NAMESPACE } from '@speed/layout-kit'
import {
  NOTIFICATION_UI_NAMESPACE,
  notificationUiResources,
} from '@speed/notification-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import { UI_KIT_NAMESPACE } from '@speed/ui-kit'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import demoZhCN from './locales/zh-CN.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import { hasCoarsePointerTouchTarget } from './test-utils/emitted-css.js'
import { mockMatchMedia } from './test-utils/matchMedia.js'
import { AUTH_CALLBACK_PATH } from './routes.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'
import { DEFAULT_TENANT_ID, getCurrentTenantId, setCurrentTenantId } from './chrome.js'
import { PlatformPermissions } from './platform-permissions.js'

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
    { namespace: CONSOLE_UI_NAMESPACE, resources: consoleUiResources },
    { namespace: NOTIFICATION_UI_NAMESPACE, resources: notificationUiResources },
  ],
})
const tAuth = textI18n.getFixedT('en-US', AUTH_UI_NAMESPACE)
const tAuthZh = textI18n.getFixedT('zh-CN', AUTH_UI_NAMESPACE)
const tConsole = textI18n.getFixedT('en-US', CONSOLE_UI_NAMESPACE)
const tLayout = textI18n.getFixedT('en-US', LAYOUT_KIT_NAMESPACE)
const tLayoutZh = textI18n.getFixedT('zh-CN', LAYOUT_KIT_NAMESPACE)
const tNotifications = textI18n.getFixedT(
  'en-US',
  NOTIFICATION_UI_NAMESPACE,
)
const tNotificationsZh = textI18n.getFixedT(
  'zh-CN',
  NOTIFICATION_UI_NAMESPACE,
)
const tUiKit = textI18n.getFixedT('en-US', UI_KIT_NAMESPACE)

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  // The current tenant is module-level host state (the client's tenantProvider
  // reads it); reset it so a switch in one test does not leak into the next.
  setCurrentTenantId(DEFAULT_TENANT_ID)
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
    // nowhere: the callback route is not gated by the tenant shell.
    expect(
      await screen.findByRole('button', {
        name: tAuth('socialCallback.retry'),
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).not.toBeInTheDocument()
  })
})

describe('the tenant shell with a session', () => {
  it('offers a platform administrator a header console entry that lands in the console', async () => {
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    // The entry is not a navigation item: the sidebar carries no way into the
    // console.
    const nav = screen.getByRole('navigation')
    expect(
      within(nav).queryByRole('link', { name: demoEnUS.nav.console }),
    ).not.toBeInTheDocument()

    // It rides the header instead. A plain click routes in place to the console
    // prefix, which renders the console's dashboard: the console frame's own
    // navigation appears.
    const entry = screen.getByRole('link', { name: demoEnUS.nav.console })
    await user.click(entry)
    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
  })

  it('offers an ordinary member no console entry anywhere', () => {
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    // The member's platform permission set is known and does not hold the
    // entry's code, so neither the sidebar nor the header carries a console
    // entry: there is no way into the console to be found.
    expect(
      screen.queryByRole('link', { name: demoEnUS.nav.console }),
    ).not.toBeInTheDocument()
  })

  it('keeps the console entry hidden while the platform permission set is unknown', async () => {
    // An authenticated session whose platform set is still unknown (null): the
    // state the host leaves the session in before it has read the caller's
    // platform permissions. usePermission fails closed on an unknown set, so
    // the entry is absent -- a member never sees it for the moment before the
    // set lands.
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot()),
    })

    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header')
    expect(banner).not.toBeNull()
    expect(
      within(banner as HTMLElement).queryByRole('link', {
        name: demoEnUS.nav.console,
      }),
    ).not.toBeInTheDocument()
  })

  it('reveals the console entry once the platform permission set lands', async () => {
    // The attach-then-appear transition the host drives through
    // setPermissionSet: the set flips from unknown (null) to a known one
    // holding the entry's code, and the entry appears where it was hidden.
    const session = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session })

    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header') as HTMLElement

    expect(
      within(banner).queryByRole('link', { name: demoEnUS.nav.console }),
    ).not.toBeInTheDocument()

    act(() => {
      session.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })
    expect(
      await within(banner).findByRole('link', { name: demoEnUS.nav.console }),
    ).toBeInTheDocument()
  })
})

describe('the console area', () => {
  it('gates a request-less viewer to the sign-in surface and follows a session that ends', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    // The session holds a platform permission, so the area gate admits the
    // frame; the point under test is the session gate around it.
    const session = createTestSession(
      authenticatedSnapshot({ system: ['tenant.read'] }),
    )
    renderDemoApp({ session })

    // The frame is up at the console path while the session is authenticated.
    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()

    // The session turns anonymous (a sign-out or a refused refresh -- the same
    // flip): the console area meets the sign-in surface, never a console page
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
      screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).not.toBeInTheDocument()
  })

  it('renders an anonymous viewer at a console path the sign-in surface, not the console', () => {
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp()

    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).not.toBeInTheDocument()
  })

  it('admits a page whose permission the principal holds', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tConsole('tenants.title'),
      }),
    ).toBeInTheDocument()
  })

  it('settles a tenant for a console URL loaded directly, so the permission read answers', async () => {
    // A console URL loaded directly (a reload, a bookmark) mounts no tenant
    // frame, and the caller's permission read sits on the guarded API
    // endpoint: without a tenant name the read is refused 404 before any
    // handler runs, and the area gate would turn a platform administrator
    // away. The area settles the default tenant itself before the read.
    setCurrentTenantId(null)
    window.history.replaceState({}, '', '/console/tenants')
    const requested: string[] = []
    const tenantAtRead: (string | null)[] = []
    bindRequestFn(<T,>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/system/tenants') {
        return Promise.resolve({
          tenants: [
            { id: DEFAULT_TENANT_ID, alias: 'acme', status: 'active' },
          ],
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
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    // The read the area gate settled on carried the tenant the console
    // boundary chose from the roster.
    expect(tenantAtRead).toEqual([DEFAULT_TENANT_ID])
    expect(requested).toContain('/api/v1/system/tenants')
  })

  it('turns a member who reaches a console page directly out of the console entirely', async () => {
    // The member never gets the entry (the test above), but hiding the entry is
    // not gating: a typed URL reaches the console path space anyway. The area
    // gate -- not the hidden entry -- is what turns it away, and it does so
    // before the shell renders: the console's menu names are themselves
    // information a member must not receive.
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    // No console frame at all -- neither its navigation landmark nor any of
    // its page links -- and no no-permission surface inside a console shell: the
    // content is not swapped for a denial, the whole area is withheld.
    expect(
      screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: tConsole('tenants.title') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(tUiKit('emptyState.noPermission.title')),
    ).not.toBeInTheDocument()

    // The viewer lands in the tenant area instead: the redirect left the
    // console path space for a shell the member may use.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/')
  })

  it('turns a member away from every console path, not only the one typed', async () => {
    for (const path of ['/console', '/console/audit']) {
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
        screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
      ).not.toBeInTheDocument()
      unmount()
    }
  })

  it('shows no console to a viewer whose platform permission set is still unknown', async () => {
    // An authenticated session whose platform set is unknown (null): the state
    // the host leaves the session in before it has read the caller's platform
    // permissions. The area gate fails closed -- it neither renders the console
    // shell (which a member must never see) nor redirects (which would bounce a
    // staff member still loading) -- so the viewer meets the shell-less
    // placeholder until the set lands.
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.console.accessPending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.home.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('admits the console once a staff platform set lands', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const staff = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session: staff })

    // Unknown set: the shell-less placeholder, no console.
    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.console.accessPending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).not.toBeInTheDocument()

    // A platform permission lands: the area admits the console frame.
    act(() => {
      staff.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })
    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
  })

  it('redirects a member out once their empty set lands, never showing the shell', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const member = createTestSession(authenticatedSnapshot())
    renderDemoApp({ session: member })
    expect(
      await screen.findByRole('progressbar', {
        name: demoEnUS.console.accessPending,
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
      screen.queryByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).not.toBeInTheDocument()
  })

  it('moves between console pages without reloading, keeping the session', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({
          system: ['tenant.read', 'settings.read', 'audit.read'],
        }),
      ),
    })

    // The roster page is up inside the console frame.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tConsole('tenants.title'),
      }),
    ).toBeInTheDocument()

    // Activate another page from the console's own navigation.
    const nav = screen.getByRole('navigation', {
      name: tConsole('shell.navLabel'),
    })
    await user.click(
      within(nav).getByRole('link', { name: tConsole('settings.title') }),
    )

    // The settings page renders in place: the console frame -- and the session
    // behind it -- survived, so the click did not reload the page and sign the
    // operator out to the sign-in surface.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tConsole('settings.title'),
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: demoEnUS.signIn.heading,
      }),
    ).not.toBeInTheDocument()
  })

  it('renders the same brand and account menu in its top bar as the tenant frame', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // The console frame is a different shell (no tenant switcher), but its
    // banner is the same chrome the tenant frame carries: the brand in the
    // header's own type, and the account menu beside it. The routed page's
    // PageHeader is also a <header> element, so scope to the banner that
    // carries the brand.
    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header')
    expect(banner).not.toBeNull()

    // The avatar trigger is named for the signed-in principal; its menu
    // carries the profile / account-settings / sign-out entries.
    await user.click(
      within(banner as HTMLElement).getByRole('button', { name: 'Test User' }),
    )
    for (const name of [
      demoEnUS.userMenu.profile,
      demoEnUS.userMenu.accountSettings,
      demoEnUS.userMenu.signOut,
    ]) {
      expect(screen.getByRole('menuitem', { name })).toBeInTheDocument()
    }
  })

  it('collapses the console sidebar from the header control', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // The console frame carries the same host-controlled collapse the tenant
    // frame does: the toggle is present, and collapsing narrows the permanent
    // drawer to the icon-only rail -- the platform navigation stays mounted
    // (named) rather than disappearing.
    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.collapseNav') }),
    )
    expect(
      screen.getByRole('button', { name: tLayout('appShell.expandNav') }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('navigation', { name: tConsole('shell.navLabel') }),
    ).toBeInTheDocument()
  })

  it('keeps the sidebar collapse across the console/tenant switch', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // Collapse in the console area.
    await user.click(
      screen.getByRole('button', { name: tLayout('appShell.collapseNav') }),
    )
    // Leave for the tenant area through the console banner's exit link: the
    // sidebar is collapsed to the rail, so the exit link is the way back.
    await user.click(screen.getByRole('link', { name: demoEnUS.console.backToApp }))
    // The tenant frame comes up already collapsed: one host-held state feeds
    // both areas, so the operator's collapse survives the switch rather than
    // resetting to open.
    expect(
      screen.getByRole('button', { name: tLayout('appShell.expandNav') }),
    ).toBeInTheDocument()
  })
})

describe('a staff viewer whose platform permission is not the dashboard code', () => {
  // The three tiers of the console gate read the one platform permission set:
  // the header entry targets the first console page whose code the principal
  // holds, the area gate admits any non-empty set, and each page's route guard
  // reads its own code. A principal whose platform set holds a permission
  // other than the dashboard's pins the three apart -- the set is non-empty
  // (so the area admits it) and some page's code is held (so the entry appears)
  // while the dashboard's own code is not (so the entry cannot point at the
  // console root).

  /** A session whose platform set holds exactly these codes. */
  const staff = (...codes: readonly string[]) =>
    createTestSession(authenticatedSnapshot({ system: [...codes] }))

  it('offers an entry that targets the first console page whose code they hold', async () => {
    window.history.replaceState({}, '', '/')
    renderDemoApp({ session: staff('audit.read') })

    // The dashboard's code (tenant.read) is unheld, so the entry cannot point
    // at the console root; the audit page is the first labelled declaration
    // whose code the principal holds, and the entry targets it.
    const brand = await screen.findByText(demoEnUS.app.title)
    const banner = brand.closest('header') as HTMLElement
    expect(
      within(banner).getByRole('link', { name: demoEnUS.nav.console }),
    ).toHaveAttribute('href', '/console/audit')
  })

  it('admits the console area, which keys on holding any platform permission', async () => {
    // A platform permission that no console entry or page carries: the set is
    // non-empty, so the area gate admits the staff viewer, while every page's
    // own code stays unheld. A gate narrowed to any one code -- tenant.read or
    // a page's -- would bounce this viewer to the tenant home instead, so the
    // frame's presence is what tells the criteria apart.
    window.history.replaceState({}, '', '/console/audit')
    renderDemoApp({ session: staff('tenant.write') })

    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(window.location.pathname).toBe('/console/audit')
    // The page's own code is unheld, so the route guard hides the page content
    // inside the frame the area gate admitted.
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
  })

  it('admits the page whose own code the principal holds', async () => {
    window.history.replaceState({}, '', '/console/audit')
    renderDemoApp({ session: staff('audit.read') })

    // The area gate admits (any platform permission) and the audit page's own
    // code (audit.read) is held, so the page renders inside the frame: the two
    // tiers agree on a staff viewer who holds the page's code.
    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: tConsole('audit.title'),
      }),
    ).toBeInTheDocument()
  })

  it('turns them away from a page whose own code they lack, the frame intact', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp({ session: staff('audit.read') })

    // The area admits them (any platform permission), but the roster page's
    // code (tenant.read) is unheld, so the route guard denies that page in
    // place: the console frame stays -- this is the innermost tier, not a
    // whole-area denial -- and the page's own heading is replaced by the
    // no-permission surface.
    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: tConsole('tenants.title'),
      }),
    ).not.toBeInTheDocument()
  })

  it('turns them away from the code-less tenant detail page too', async () => {
    // The tenant detail declaration carries no code of its own; the guard
    // resolves a page's permission from the selected (labelled) entry, so a
    // URL below the roster's path is held to the roster's tenant.read. A
    // viewer without it meets the no-permission surface inside the frame,
    // never the detail page.
    window.history.replaceState({}, '', '/console/tenants/xyz')
    renderDemoApp({ session: staff('audit.read') })

    expect(
      await screen.findByRole('navigation', {
        name: tConsole('shell.navLabel'),
      }),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(tUiKit('emptyState.noPermission.title')),
    ).toBeInTheDocument()
  })
})

describe('the tenant area header controls', () => {
  it('raises the header icon controls to the 44px floor under a coarse pointer', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    // The language switch, the color-mode toggle and the account-menu
    // trigger share the header row with the notification bell; on a
    // finger-driven device each one gates up to the 44x44 floor.
    const gated = [
      await screen.findByRole('button', {
        name: demoEnUS.languageSwitcher.label,
      }),
      screen.getByRole('button', { name: demoEnUS.colorMode.toDark }),
      screen.getByRole('button', { name: 'Test User' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('switches the language from the header control', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await user.click(
      screen.getByRole('button', { name: demoEnUS.languageSwitcher.label }),
    )
    const zhName = new Intl.DisplayNames(['en-US'], { type: 'language' }).of(
      'zh-CN',
    )
    await user.click(screen.getByRole('menuitem', { name: zhName ?? 'zh-CN' }))

    // The interface relabels into the chosen language: the brand, resolved
    // from the demo namespace, now reads in Chinese.
    expect(
      await screen.findByText(demoZhCN.app.title),
    ).toBeInTheDocument()
  })

  it('offers only the tenants the seeded deployment registers', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The seeded roster registers acme alone, so the switcher must not
    // offer a second tenant: switching to one the server cannot resolve
    // would leave every tenant-scoped read refused 404.
    await user.click(screen.getByRole('button', { name: 'acme' }))
    expect(
      screen.getByRole('menuitem', { name: 'acme' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('menuitem', { name: 'Globex Corp.' }),
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

  it('toggles the color mode from the header control', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The control describes the action; after the flip it offers the other
    // mode.
    await user.click(
      screen.getByRole('button', { name: demoEnUS.colorMode.toDark }),
    )
    expect(
      screen.getByRole('button', { name: demoEnUS.colorMode.toLight }),
    ).toBeInTheDocument()
  })
})

describe('the tenant navigation', () => {
  it('leaves a modified click on an entry to the browser', async () => {
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const entry = await screen.findByRole('link', { name: demoEnUS.nav.account })

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
  const CASES = [
    {
      language: 'en-US',
      labels: [
        demoEnUS.nav.home,
        demoEnUS.nav.account,
        demoEnUS.nav.objects,
        tNotifications('inbox.title'),
      ],
      collapse: tLayout('appShell.collapseNav'),
      open: tLayout('appShell.openNav'),
    },
    {
      language: 'zh-CN',
      labels: [
        demoZhCN.nav.home,
        demoZhCN.nav.account,
        demoZhCN.nav.objects,
        tNotificationsZh('inbox.title'),
      ],
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

    for (const label of [
      demoEnUS.nav.home,
      demoEnUS.nav.account,
      demoEnUS.nav.objects,
      tNotifications('inbox.title'),
    ]) {
      const link = screen.getByRole('link', { name: label })
      expect(link.querySelector(`.${listItemIconClasses.root} svg`)).not.toBeNull()
      const textRoot = link.querySelector(`.${listItemTextClasses.root}`)
      expect(textRoot).not.toHaveStyle({ position: 'absolute' })
    }
  })
})

describe('the not-found fallback', () => {
  it('explains the dead link in the user terms and offers a way home', async () => {
    window.history.replaceState({}, '', '/console/definitely-not-a-page')
    // A platform permission admits the console frame; the path itself
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
          '/console/definitely-not-a-page',
        ),
      ),
    ).toBeInTheDocument()
  })

  it('shows the requested address verbatim, with no HTML entities', async () => {
    const path = '/console/a&b'
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
  it('mounts the account surfaces in the tenant shell at /account', async () => {
    window.history.replaceState({}, '', '/account')
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The account page's page header renders its title as the h1.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.account.title,
      }),
    ).toBeInTheDocument()
  })
})
