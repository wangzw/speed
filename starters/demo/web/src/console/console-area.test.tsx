/**
 * ConsoleArea: the platform frame around the routed console page.
 *
 * It renders @speed/console-ui's shell with the prefixed console declarations,
 * so the frame's navigation carries one translated entry per declared page and
 * the host's title sits in the banner. The child stands in for the page the
 * router would render; the console pages' own data reads are the package's, not
 * this area's.
 *
 * The area wires the frame's navigation into the app's router (route-kit) and
 * carries an exit back to the tenant area, both routing in place rather than
 * reloading the page.
 */

import { QueryClientProvider } from '@tanstack/react-query'
import { toolbarClasses } from '@mui/material/Toolbar'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  CONSOLE_UI_NAMESPACE,
  InvitationsPage,
  consoleUiResources,
} from '@speed/console-ui'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  createQueryClient,
  createSpeedI18n,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider } from '@speed/route-kit'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { AppServicesProvider } from '../app-services.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { invitationPath } from '../paths.js'
import { DEMO_NAMESPACE, demoResources } from '../resources.js'
import { consoleShellRoutes } from '../routes.js'
import { hasCoarsePointerTouchTarget } from '../test-utils/emitted-css.js'
import { createHarnessClient } from '../test-utils/render.js'
import { createTestSession } from '../test-utils/session.js'
import { ConsoleArea } from './console-area.js'

afterEach(() => {
  window.history.replaceState({}, '', '/')
})

/**
 * The expected copy, resolved from the console package's shipped resources
 * through a production instance rather than read from a file path.
 */
const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: CONSOLE_UI_NAMESPACE, resources: consoleUiResources },
  ],
})
const tConsole = textI18n.getFixedT('en-US', CONSOLE_UI_NAMESPACE)

function renderConsoleArea(
  children: ReactNode = <p>console page</p>,
  frame: {
    readonly shellLayout?: 'topbar' | 'sidebar'
    readonly sidebarCollapsed?: boolean
  } = {},
) {
  const i18n = createI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
  })
  registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
  // The console frame is layout-kit's AppShell, whose banner strings speak
  // the layout-kit namespace.
  registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
  registerNamespace(i18n, CONSOLE_UI_NAMESPACE, consoleUiResources)
  registerNamespace(i18n, DEMO_NAMESPACE, demoResources)

  // The account entry (src/chrome's AccountEntry) reads the assembled
  // session -- so the area needs the services provider even though the routed
  // child here is a stand-in.
  const services: SpeedAppServices = {
    session: createTestSession(),
    api: createHarnessClient(),
    queryClient: createQueryClient(),
  }

  // The area reads the router (route-kit's navigate) to route its navigation
  // and its exit in place, so it renders under a RouteProvider as the app
  // mounts it. The table is the console's own declarations: the area derives
  // its selected entry from the nav items that table yields, exactly as the
  // app does with its full table. The routed child stays the stand-in.
  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <AppServicesProvider services={services}>
          <QueryClientProvider client={services.queryClient}>
            <RouteProvider
              table={consoleShellRoutes}
              fallback={() => null}
              // Every console declaration is gated, so the provider needs the
              // host's presence answer: this suite serves every feature.
              presence={{ state: 'ready', has: () => true }}
              pending={() => null}
              failed={() => null}
            >
              <ConsoleArea
                routes={consoleShellRoutes}
                hasFeature={() => true}
                shellLayout={frame.shellLayout}
                sidebarCollapsed={frame.sidebarCollapsed}
                onSidebarCollapsedChange={() => undefined}
              >
                {children}
              </ConsoleArea>
            </RouteProvider>
          </QueryClientProvider>
        </AppServicesProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
}

describe('ConsoleArea', () => {
  it('renders the console frame with its title and the routed page', () => {
    renderConsoleArea()
    expect(screen.getByText(demoEnUS.app.title)).toBeInTheDocument()
    expect(screen.getByText('console page')).toBeInTheDocument()
  })

  it('renders the exit as an icon-only control in the collapsed rail, under the same name', () => {
    renderConsoleArea(undefined, {
      shellLayout: 'sidebar',
      sidebarCollapsed: true,
    })
    // The rail has no room for the exit text; the control keeps the label
    // as its accessible name and its href, so it stays a real link.
    const exit = screen.getByRole('link', {
      name: demoEnUS.console.backToApp,
    })
    expect(exit.textContent?.trim()).toBe('')
    expect(exit.getAttribute('href')).not.toBeNull()
  })

  it('hands the console header controls to the shell as siblings, so a collapsed rail can stack them', () => {
    renderConsoleArea()
    const back = screen.getByRole('link', {
      name: demoEnUS.console.backToApp,
    })
    const toolbar = screen
      .getByRole('banner')
      .querySelector(`.${toolbarClasses.root}`)
    expect(toolbar).not.toBeNull()
    // The controls are the actions slot's own children, not a wrapper's: the
    // shell lays a slot's children out in a row on the top bar and in a
    // column in the collapsed rail, so a wrapper would leave the rail one
    // row of controls wider than the paper.
    expect(back.parentElement?.parentElement).toBe(toolbar)
  })

  it('renders the account entry in the navigation column and no header avatar menu', async () => {
    renderConsoleArea()

    // ACCOUNT_ENTRY's default places the entry at the bottom of the column,
    // after the navigation, and leaves the header without an avatar menu --
    // the tenant frame's placement carried into the console.
    const trigger = await screen.findByTestId('account-entry-trigger')
    const nav = screen.getByRole('navigation')
    expect(nav.contains(trigger)).toBe(false)
    expect(
      nav.compareDocumentPosition(trigger) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.queryByTestId('user-menu-trigger')).toBeNull()
  })

  it('groups the platform navigation by the console group table', () => {
    renderConsoleArea()
    const nav = screen.getByRole('navigation')
    // The group headings are the disclosure controls, in the group table's
    // order: identity & access and platform configuration are the groups with
    // more than one entry here (the platform group holds settings and the SMS
    // templates). Overview, tenants and audit hold a lone entry each and
    // render no heading over it, so they are asserted absent below.
    expect(
      within(nav)
        .getAllByRole('button')
        .map((button) => button.textContent),
    ).toEqual([
      tConsole('nav.groups.identity'),
      tConsole('nav.groups.platform'),
    ])
    // A single-entry group is promoted: its one page stands as a top-level
    // link at the position the group would have taken, with no disclosure
    // control of its own.
    for (const loneGroup of [
      tConsole('nav.groups.overview'),
      tConsole('nav.groups.tenants'),
      tConsole('nav.groups.audit'),
    ]) {
      expect(
        within(nav).queryByRole('button', { name: loneGroup }),
      ).toBeNull()
    }
    // The entries gather under their groups: groups in table order, entries in
    // declaration order within each group (the identity group holds the account
    // surfaces in the order the console declares them).
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      tConsole('dashboard.title'),
      tConsole('accounts.title'),
      tConsole('sessions.title'),
      tConsole('impersonations.title'),
      tConsole('permissions.title'),
      tConsole('invitations.title'),
      tConsole('sso.title'),
      tConsole('tenants.title'),
      tConsole('settings.title'),
      tConsole('sms.title'),
      tConsole('audit.title'),
    ])
  })

  it('marks the current console page in the navigation', () => {
    window.history.replaceState({}, '', '/console/settings')
    renderConsoleArea()
    const nav = screen.getByRole('navigation')

    // The shell does no path matching: the host derives the selection from
    // the console nav items and hands it in, so exactly the current page's
    // entry is marked.
    expect(
      within(nav).getByRole('link', { name: tConsole('settings.title') }),
    ).toHaveAttribute('aria-current', 'page')
    expect(
      within(nav).getByRole('link', { name: tConsole('tenants.title') }),
    ).not.toHaveAttribute('aria-current')
  })

  it('routes a console navigation entry in place instead of reloading', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderConsoleArea()

    const nav = screen.getByRole('navigation')
    await user.click(
      within(nav).getByRole('link', { name: tConsole('settings.title') }),
    )
    // The entry went through the router: the URL moved without a page load.
    expect(window.location.pathname).toBe('/console/settings')
  })

  it('offers an exit back to the tenant area that routes in place', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderConsoleArea()

    const exit = screen.getByRole('link', { name: demoEnUS.console.backToApp })
    await user.click(exit)
    expect(window.location.pathname).toBe('/')
  })

  it('raises the exit link to the 44px touch floor under a coarse pointer', () => {
    window.history.replaceState({}, '', '/console/tenants')
    renderConsoleArea()

    // The exit is a text link: on a fine pointer it is a 24px-tall inline
    // box, below the finger floor. The theme's coarse rule makes it an
    // inline-flex box of at least 44x44 -- no per-control gate here.
    const exit = screen.getByRole('link', { name: demoEnUS.console.backToApp })
    expect(hasCoarsePointerTouchTarget(exit)).toBe(true)
  })

  it("turns an issued invitation's token into this app's landing link", async () => {
    const requestFn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/system/tenants' && method === 'GET') {
        return Promise.resolve({
          tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }],
        } as T)
      }
      if (
        path === '/api/v1/tenants/acme/invitations' &&
        method === 'POST'
      ) {
        return Promise.resolve({
          invitation: {
            id: 'inv-1',
            tenant: 'acme',
            email: 'new@example.com',
            status: 'pending',
            invited_by: 'staff-1',
            expires_at: '2026-10-20T00:00:00Z',
            accepted_at: '0001-01-01T00:00:00Z',
            created_at: '2026-10-15T12:00:00Z',
          },
          token: 'spd_inv_x',
        } as T)
      }
      // The page's list reads: answered so react-query never receives an
      // undefined document (it logs "Query data cannot be undefined").
      if (
        path === '/api/v1/tenants/acme/invitations' &&
        method === 'GET'
      ) {
        return Promise.resolve({ invitations: [], next_cursor: '' } as T)
      }
      if (path === '/api/v1/tenants/acme/members' && method === 'GET') {
        return Promise.resolve({ members: [], next_cursor: '' } as T)
      }
      // A path this suite has not scripted is a hole, not an empty answer:
      // fail loudly instead of letting react-query log a warning.
      return Promise.reject(new Error(`unanswered ${method} ${path}`))
    }
    bindRequestFn(requestFn)
    const user = userEvent.setup()
    renderConsoleArea(<InvitationsPage />)

    await user.click(screen.getByLabelText(tConsole('common.tenant')))
    await user.click(await screen.findByRole('option', { name: 'Acme' }))
    await user.type(
      await screen.findByLabelText(tConsole('invitations.issue.emailLabel')),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', {
        name: tConsole('invitations.issue.submit'),
      }),
    )

    // The issued token is turned into the app's own landing link (the host's
    // invitationPath), not dropped: the dead-code path is wired.
    expect(
      await screen.findByText(
        tConsole('invitations.issued.link', { email: 'new@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        `${window.location.origin}${invitationPath('spd_inv_x')}`,
      ),
    ).toBeInTheDocument()
  })
})
