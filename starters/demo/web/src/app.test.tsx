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
 * code.
 */

import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import authUiEnUS from '../../../../web/packages/auth-ui/src/locales/en-US.json' with { type: 'json' }
import authUiZhCN from '../../../../web/packages/auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import consoleUiEnUS from '../../../../web/packages/console-ui/src/locales/en-US.json' with { type: 'json' }
import layoutKitEnUS from '../../../../web/packages/layout-kit/src/locales/en-US.json' with { type: 'json' }
import uiKitEnUS from '../../../../web/packages/ui-kit/src/locales/en-US.json' with { type: 'json' }
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import demoZhCN from './locales/zh-CN.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import { AUTH_CALLBACK_PATH } from './routes.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'
import { DEFAULT_TENANT_ID, getCurrentTenantId, setCurrentTenantId } from './chrome.js'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  // The current tenant is module-level host state (the client's tenantProvider
  // reads it); reset it so a switch in one test does not leak into the next.
  setCurrentTenantId(DEFAULT_TENANT_ID)
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
      await screen.findByLabelText(authUiEnUS.passwordSignIn.identifierLabel),
    ).toBeInTheDocument()
    // The authenticated frame's business page is not rendered.
    expect(screen.queryByText(demoEnUS.home.heading)).not.toBeInTheDocument()
  })

  it('offers only the password channel, with no tab strip', async () => {
    renderDemoApp()
    // Wait for the surface (the discovery read resolves a document with the
    // one channel). No SMS axis is offered (the skeleton delivers no code),
    // so there is nothing to switch between and no tablist.
    await screen.findByLabelText(authUiEnUS.passwordSignIn.identifierLabel)
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
  })

  it('renders the sign-in surface in the active language', async () => {
    renderDemoApp({ language: 'zh-CN' })

    expect(
      screen.getByRole('heading', { level: 1, name: demoZhCN.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      await screen.findByLabelText(authUiZhCN.passwordSignIn.identifierLabel),
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
        name: authUiEnUS.socialCallback.retry,
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
        name: consoleUiEnUS.shell.navLabel,
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

    const brand = await screen.findByRole('heading', {
      level: 6,
      name: demoEnUS.app.title,
    })
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

    const brand = await screen.findByRole('heading', {
      level: 6,
      name: demoEnUS.app.title,
    })
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
        name: consoleUiEnUS.shell.navLabel,
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
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
    ).not.toBeInTheDocument()
  })

  it('renders an anonymous viewer at a console path the sign-in surface, not the console', () => {
    window.history.replaceState({}, '', '/console/tenants')
    renderDemoApp()

    expect(
      screen.getByRole('heading', { level: 1, name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
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
        name: consoleUiEnUS.tenants.title,
      }),
    ).toBeInTheDocument()
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
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: consoleUiEnUS.tenants.title }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText(uiKitEnUS.emptyState.noPermission.title),
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
        screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
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
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
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
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
    ).not.toBeInTheDocument()

    // A platform permission lands: the area admits the console frame.
    act(() => {
      staff.set(authenticatedSnapshot({ system: ['tenant.read'] }))
    })
    expect(
      await screen.findByRole('navigation', {
        name: consoleUiEnUS.shell.navLabel,
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
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
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
        name: consoleUiEnUS.tenants.title,
      }),
    ).toBeInTheDocument()

    // Activate another page from the console's own navigation.
    const nav = screen.getByRole('navigation', {
      name: consoleUiEnUS.shell.navLabel,
    })
    await user.click(
      within(nav).getByRole('link', { name: consoleUiEnUS.settings.title }),
    )

    // The settings page renders in place: the console frame -- and the session
    // behind it -- survived, so the click did not reload the page and sign the
    // operator out to the sign-in surface.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: consoleUiEnUS.settings.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
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
    const brand = await screen.findByRole('heading', {
      level: 6,
      name: demoEnUS.app.title,
    })
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
    // frame does: the toggle is present, and collapsing removes the permanent
    // drawer and the platform navigation with it.
    expect(
      await screen.findByRole('navigation', {
        name: consoleUiEnUS.shell.navLabel,
      }),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: layoutKitEnUS.appShell.collapseNav }),
    )
    expect(
      screen.getByRole('button', { name: layoutKitEnUS.appShell.expandNav }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('navigation', { name: consoleUiEnUS.shell.navLabel }),
    ).not.toBeInTheDocument()
  })

  it('keeps the sidebar collapse across the console/tenant switch', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: ['tenant.read'] })),
    })

    // Collapse in the console area.
    await user.click(
      screen.getByRole('button', { name: layoutKitEnUS.appShell.collapseNav }),
    )
    // Leave for the tenant area through the console banner's exit link: the
    // navigation sidebar is gone, so the exit is the way back.
    await user.click(screen.getByRole('link', { name: demoEnUS.console.backToApp }))
    // The tenant frame comes up already collapsed: one host-held state feeds
    // both areas, so the operator's collapse survives the switch rather than
    // resetting to open.
    expect(
      screen.getByRole('button', { name: layoutKitEnUS.appShell.expandNav }),
    ).toBeInTheDocument()
  })
})

describe('the tenant area header controls', () => {
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
      await screen.findByRole('heading', {
        level: 6,
        name: demoZhCN.app.title,
      }),
    ).toBeInTheDocument()
  })

  it('switches the tenant from the header switcher', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The trigger shows the host's current tenant; picking another reports
    // the choice and the trigger converges onto it.
    await user.click(screen.getByRole('button', { name: 'Acme Inc.' }))
    await user.click(screen.getByRole('menuitem', { name: 'Globex Corp.' }))
    expect(
      screen.getByRole('button', { name: 'Globex Corp.' }),
    ).toBeInTheDocument()
    // The switch writes the one host-held tenant, so the client's
    // tenantProvider announces the new tenant on the next request rather
    // than the departing one.
    expect(getCurrentTenantId()).toBe('globex')
  })

  it('collapses and expands the sidebar from the header control', async () => {
    const user = userEvent.setup()
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The desktop sidebar collapse toggle is present (the host supplies the
    // collapse props); collapsing removes the permanent drawer and the
    // toggle now offers to expand.
    await user.click(
      screen.getByRole('button', { name: layoutKitEnUS.appShell.collapseNav }),
    )
    expect(
      screen.getByRole('button', { name: layoutKitEnUS.appShell.expandNav }),
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
