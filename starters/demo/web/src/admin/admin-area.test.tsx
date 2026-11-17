/**
 * AdminArea: the platform frame around the routed admin page.
 *
 * It renders @speed/admin-ui's shell with the prefixed admin declarations,
 * so the frame's navigation carries one translated entry per declared page and
 * the host's title sits in the banner. The child stands in for the page the
 * router would render; the admin pages' own data reads are the package's, not
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
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  ADMIN_UI_NAMESPACE,
  InvitationsPage,
  adminUiResources,
} from '@speed/admin-ui'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  createQueryClient,
  createSpeedI18n,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider } from '@speed/route-kit'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import { AppServicesProvider } from '../app-services.js'
import type { AuthzTenantAdminRoleView } from '../app-api/index.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { invitationPath } from '../paths.js'
import { DEMO_NAMESPACE, demoResources } from '../resources.js'
import { adminShellRoutes } from '../routes.js'
import { hasCoarsePointerTouchTarget } from '../test-utils/emitted-css.js'
import { createHarnessClient } from '../test-utils/render.js'
import { createTestSession } from '../test-utils/session.js'
import { AdminArea } from './admin-area.js'

afterEach(() => {
  window.history.replaceState({}, '', '/')
})

/**
 * Every case renders under a missing-translation guard. A fixture that omits
 * a contract-required field makes a page render an interpolated key such as
 * "invitations.issued.delivery.undefined"; i18n reports that through a
 * console warning and renders the key itself. The warning is an issue, not
 * noise: collecting it for the whole file means the case that rendered the
 * deficient fixture fails, rather than the warning scrolling past.
 */
let warnCalls: readonly unknown[][] = []
beforeEach(() => {
  warnCalls = []
  const warn = console.warn.bind(console)
  vi.spyOn(console, 'warn').mockImplementation((...args: unknown[]) => {
    warnCalls = [...warnCalls, args]
    // Keep the warning visible: the guard fails the case that emitted it,
    // and the original line stays in the run's output.
    warn(...args)
  })
})
afterEach(() => {
  const missing = warnCalls.filter(([message]) =>
    String(message).includes('[speed-i18n] missing translation key'),
  )
  vi.restoreAllMocks()
  expect(
    missing.map(([message]) => String(message)),
    'the case rendered a missing translation key',
  ).toEqual([])
})

/**
 * The expected copy, resolved from the admin package's shipped resources
 * through a production instance rather than read from a file path.
 */
const textI18n = createSpeedI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [
    { namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources },
  ],
})
const tAdmin = textI18n.getFixedT('en-US', ADMIN_UI_NAMESPACE)

/**
 * One row of the tenant roles read, in the shape the generated view carries.
 * Typed rather than asserted, so a contract field this view gains fails the
 * typecheck instead of slipping through a cast.
 */
const tenantRoleRow: AuthzTenantAdminRoleView = {
  builtin: false,
  can_manage: true,
  created_at: '2026-10-15T12:00:00Z',
  description: 'Administrator',
  member_count: null,
  permission_count: 0,
  role: 'admin',
  tenant: 'acme',
}

function renderAdminArea(
  children: ReactNode = <p>admin page</p>,
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
  // The admin frame is layout-kit's AppShell, whose banner strings speak
  // the layout-kit namespace.
  registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
  registerNamespace(i18n, ADMIN_UI_NAMESPACE, adminUiResources)
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
  // mounts it. The table is the admin's own declarations: the area derives
  // its selected entry from the nav items that table yields, exactly as the
  // app does with its full table. The routed child stays the stand-in.
  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <AppServicesProvider services={services}>
          <QueryClientProvider client={services.queryClient}>
            <RouteProvider
              table={adminShellRoutes}
              fallback={() => null}
              // Every admin declaration is gated, so the provider needs the
              // host's presence answer: this suite serves every feature.
              presence={{ state: 'ready', has: () => true }}
              pending={() => null}
              failed={() => null}
            >
              <AdminArea
                routes={adminShellRoutes}
                hasFeature={() => true}
                shellLayout={frame.shellLayout}
                sidebarCollapsed={frame.sidebarCollapsed}
                onSidebarCollapsedChange={() => undefined}
              >
                {children}
              </AdminArea>
            </RouteProvider>
          </QueryClientProvider>
        </AppServicesProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
}

describe('AdminArea', () => {
  it('renders the admin frame with its title and the routed page', () => {
    renderAdminArea()
    expect(screen.getByText(demoEnUS.app.title)).toBeInTheDocument()
    expect(screen.getByText('admin page')).toBeInTheDocument()
  })

  it('keeps the exit reachable and named in the collapsed rail', async () => {
    const user = userEvent.setup()
    renderAdminArea(undefined, {
      shellLayout: 'sidebar',
      sidebarCollapsed: true,
    })
    // The rail has room only for the account glyph; the exit is one entry of
    // the menu that glyph opens, still named by the same label and a real
    // link to the tenant home.
    await user.click(screen.getByTestId('account-entry-trigger'))
    const exit = await screen.findByRole('menuitem', {
      name: demoEnUS.admin.backToApp,
    })
    expect(exit.tagName).toBe('A')
    expect(exit.getAttribute('href')).not.toBeNull()
  })

  it('hands the admin header its account menu as the actions slot’s own control', () => {
    renderAdminArea()
    const trigger = screen.getByTestId('user-menu-trigger')
    const toolbar = screen
      .getByRole('banner')
      .querySelector(`.${toolbarClasses.root}`)
    expect(toolbar).not.toBeNull()
    // The account menu is the bar's one control and the actions slot's own
    // child: the shell lays a slot's children out in a row on the top bar and
    // in a column in the collapsed rail, so an extra wrapper would leave the
    // rail one row of controls wider than the paper. The trigger sits in the
    // menu's own box, itself the slot's child inside the toolbar.
    expect(trigger.parentElement?.parentElement?.parentElement).toBe(toolbar)
    // The exit is no longer a second bar control: it is a menu entry.
    expect(
      screen.queryByRole('link', { name: demoEnUS.admin.backToApp }),
    ).toBeNull()
  })

  it('renders the account entry in the navigation column and the header avatar menu', async () => {
    renderAdminArea()

    // ACCOUNT_ENTRY is 'both': the entry sits at the bottom of the column,
    // after the navigation, and the header carries the same account menu --
    // the tenant frame's placement carried into the admin.
    const trigger = await screen.findByTestId('account-entry-trigger')
    const nav = screen.getByRole('navigation')
    expect(nav.contains(trigger)).toBe(false)
    expect(
      nav.compareDocumentPosition(trigger) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(screen.getByTestId('user-menu-trigger')).toBeInTheDocument()
  })

  it('groups the platform navigation by the admin group table', () => {
    renderAdminArea()
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
      tAdmin('nav.groups.identity'),
      tAdmin('nav.groups.platform'),
    ])
    // A single-entry group is promoted: its one page stands as a top-level
    // link at the position the group would have taken, with no disclosure
    // control of its own.
    for (const loneGroup of [
      tAdmin('nav.groups.overview'),
      tAdmin('nav.groups.tenants'),
      tAdmin('nav.groups.audit'),
    ]) {
      expect(
        within(nav).queryByRole('button', { name: loneGroup }),
      ).toBeNull()
    }
    // The entries gather under their groups: groups in table order, entries in
    // declaration order within each group (the identity group holds the account
    // surfaces in the order the admin declares them).
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      tAdmin('dashboard.title'),
      tAdmin('accounts.title'),
      tAdmin('sessions.title'),
      tAdmin('impersonations.title'),
      tAdmin('permissions.title'),
      tAdmin('invitations.title'),
      tAdmin('sso.title'),
      tAdmin('tenants.title'),
      tAdmin('settings.title'),
      tAdmin('sms.title'),
      tAdmin('audit.title'),
    ])
  })

  it('marks the current admin page in the navigation', () => {
    window.history.replaceState({}, '', '/admin/settings')
    renderAdminArea()
    const nav = screen.getByRole('navigation')

    // The shell does no path matching: the host derives the selection from
    // the admin nav items and hands it in, so exactly the current page's
    // entry is marked.
    expect(
      within(nav).getByRole('link', { name: tAdmin('settings.title') }),
    ).toHaveAttribute('aria-current', 'page')
    expect(
      within(nav).getByRole('link', { name: tAdmin('tenants.title') }),
    ).not.toHaveAttribute('aria-current')
  })

  it('routes an admin navigation entry in place instead of reloading', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderAdminArea()

    const nav = screen.getByRole('navigation')
    await user.click(
      within(nav).getByRole('link', { name: tAdmin('settings.title') }),
    )
    // The entry went through the router: the URL moved without a page load.
    expect(window.location.pathname).toBe('/admin/settings')
  })

  it('offers an exit back to the tenant area that routes in place', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderAdminArea()

    await user.click(screen.getByTestId('user-menu-trigger'))
    const exit = await screen.findByRole('menuitem', {
      name: demoEnUS.admin.backToApp,
    })
    await user.click(exit)
    expect(window.location.pathname).toBe('/')
  })

  it('raises the exit menu entry to the 44px touch floor under a coarse pointer', async () => {
    window.history.replaceState({}, '', '/admin/tenants')
    const user = userEvent.setup()
    renderAdminArea()

    // The exit is a menu entry now: ButtonBase's coarse rule gives it the
    // same 44px floor every other menu row gets, with no per-control gate.
    await user.click(screen.getByTestId('user-menu-trigger'))
    const exit = await screen.findByRole('menuitem', {
      name: demoEnUS.admin.backToApp,
    })
    expect(hasCoarsePointerTouchTarget(exit)).toBe(true)
  })

  it("turns an issued invitation's token into this app's landing link", async () => {
    const requestFn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/system/tenants' && method === 'GET') {
        return Promise.resolve({
          tenants: [{ name: 'acme', display_name: 'Acme', kind: 'business', status: 'active' }],
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
            state: 'pending',
            invited_by: 'staff-1',
            expires_at: '2026-10-20T00:00:00Z',
            accepted_at: null,
            revoked_at: null,
            last_sent_at: '2026-10-15T12:00:00Z',
            send_count: 1,
            created_at: '2026-10-15T12:00:00Z',
          },
          token: 'spd_inv_x',
          email_delivery: 'not_configured',
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
      if (path === '/api/v1/tenants/acme/roles' && method === 'GET') {
        return Promise.resolve({ roles: [tenantRoleRow] } as T)
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
    renderAdminArea(<InvitationsPage />)

    await user.click(screen.getByLabelText(tAdmin('common.tenant')))
    await user.click(await screen.findByRole('option', { name: 'Acme' }))
    await user.type(
      await screen.findByLabelText(tAdmin('invitations.issue.emailLabel')),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', {
        name: tAdmin('invitations.issue.submit'),
      }),
    )

    // The issued token is turned into the app's own landing link (the host's
    // invitationPath), not dropped: the dead-code path is wired.
    expect(
      await screen.findByText(
        tAdmin('invitations.issued.link', { email: 'new@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        `${window.location.origin}${invitationPath('spd_inv_x')}`,
      ),
    ).toBeInTheDocument()
  })

  it('renders the delivery outcome of an issued invitation without a missing key', async () => {
    const requestFn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/system/tenants' && method === 'GET') {
        return Promise.resolve({
          tenants: [
            { name: 'acme', display_name: 'Acme', kind: 'business', status: 'active' },
          ],
        } as T)
      }
      if (path === '/api/v1/tenants/acme/invitations' && method === 'POST') {
        // Contract §1: email_delivery is required on the issue answer.
        return Promise.resolve({
          invitation: {
            id: 'inv-1',
            tenant: 'acme',
            email: 'new@example.com',
            status: 'pending',
            state: 'pending',
            invited_by: 'staff-1',
            expires_at: '2026-10-20T00:00:00Z',
            accepted_at: null,
            revoked_at: null,
            last_sent_at: '2026-10-15T12:00:00Z',
            send_count: 1,
            created_at: '2026-10-15T12:00:00Z',
          },
          token: 'spd_inv_x',
          email_delivery: 'not_configured',
        } as T)
      }
      if (path === '/api/v1/tenants/acme/invitations' && method === 'GET') {
        return Promise.resolve({ invitations: [], next_cursor: '' } as T)
      }
      if (path === '/api/v1/tenants/acme/roles' && method === 'GET') {
        return Promise.resolve({ roles: [tenantRoleRow] } as T)
      }
      if (path === '/api/v1/tenants/acme/members' && method === 'GET') {
        return Promise.resolve({ members: [], next_cursor: '' } as T)
      }
      return Promise.reject(new Error(`unanswered ${method} ${path}`))
    }
    bindRequestFn(requestFn)
    const user = userEvent.setup()
    renderAdminArea(<InvitationsPage />)

    await user.click(screen.getByLabelText(tAdmin('common.tenant')))
    await user.click(await screen.findByRole('option', { name: 'Acme' }))
    await user.type(
      await screen.findByLabelText(tAdmin('invitations.issue.emailLabel')),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', {
        name: tAdmin('invitations.issue.submit'),
      }),
    )
    // The outcome renders its own copy; the file-level afterEach guard
    // fails this case if the rendered key had a missing segment.
    expect(
      await screen.findByText(
        tAdmin('invitations.issued.delivery.not_configured'),
      ),
    ).toBeInTheDocument()
  })
})
