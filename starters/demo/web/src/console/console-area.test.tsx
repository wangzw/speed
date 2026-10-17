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

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
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
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider } from '@speed/route-kit'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import consoleUiEnUS from '../../../../../web/packages/console-ui/src/locales/en-US.json' with { type: 'json' }
import { AppServicesProvider } from '../app-services.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { invitationPath } from '../paths.js'
import { DEMO_NAMESPACE, demoResources } from '../resources.js'
import { consoleShellRoutes } from '../routes.js'
import { createHarnessClient } from '../test-utils/render.js'
import { createTestSession } from '../test-utils/session.js'
import { ConsoleArea } from './console-area.js'

afterEach(() => {
  window.history.replaceState({}, '', '/')
})

/**
 * Whether `element` carries a coarse-pointer rule that sets both minimum
 * edges to the 44px floor. The same probe `@speed/test-utils/emitted-css`
 * ships, kept local because the demo host declares every `@speed`
 * dependency by registry version and so cannot import the private
 * workspace test-support package.
 */
function hasCoarsePointerTouchTarget(element: Element): boolean {
  const css = Array.from(document.querySelectorAll('style'))
    .map((style) => style.textContent ?? '')
    .join('\n')
  const classes = Array.from(element.classList).filter((name) =>
    name.startsWith('css-'),
  )
  return classes.some((className) =>
    css
      .split('@media (pointer: coarse)')
      .slice(1)
      .some((block) => {
        const at = block.indexOf(`.${className}`)
        if (at < 0) return false
        const open = block.indexOf('{', at)
        const close = open < 0 ? -1 : block.indexOf('}', open)
        if (open < 0 || close < 0) return false
        const declarations = block.slice(open + 1, close)
        return (
          declarations.includes('min-width:44px') &&
          declarations.includes('min-height:44px')
        )
      }),
  )
}

function renderConsoleArea(children: ReactNode = <p>console page</p>) {
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

  // The frame's top bar carries the sign-out action (src/chrome's UserMenu),
  // which reads the assembled session -- so the area needs the services
  // provider even though the routed child here is a stand-in.
  const services: SpeedAppServices = {
    session: createTestSession(),
    api: createHarnessClient(),
    queryClient: new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    }),
  }

  // The area reads the router (route-kit's navigate) to route its navigation
  // and its exit in place, so it renders under a RouteProvider as the app
  // mounts it; the routed child is the stand-in, so the table stays empty.
  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <AppServicesProvider services={services}>
          <QueryClientProvider client={services.queryClient}>
            <RouteProvider table={[]} fallback={() => null}>
              <ConsoleArea routes={consoleShellRoutes}>
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

  it('groups the platform navigation by the console group table', () => {
    renderConsoleArea()
    const nav = screen.getByRole('navigation')
    // The group headings are the disclosure controls, in the group table's
    // order -- so the sidebar reads as sections rather than one long list.
    expect(
      within(nav)
        .getAllByRole('button')
        .map((button) => button.textContent),
    ).toEqual([
      consoleUiEnUS.nav.groups.overview,
      consoleUiEnUS.nav.groups.identity,
      consoleUiEnUS.nav.groups.tenants,
      consoleUiEnUS.nav.groups.platform,
      consoleUiEnUS.nav.groups.audit,
    ])
    // The entries gather under their groups: groups in table order, entries in
    // declaration order within each group (the identity group holds the account
    // surfaces in the order the console declares them).
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      consoleUiEnUS.dashboard.title,
      consoleUiEnUS.accounts.title,
      consoleUiEnUS.sessions.title,
      consoleUiEnUS.impersonations.title,
      consoleUiEnUS.permissions.title,
      consoleUiEnUS.invitations.title,
      consoleUiEnUS.sso.title,
      consoleUiEnUS.tenants.title,
      consoleUiEnUS.settings.title,
      consoleUiEnUS.sms.title,
      consoleUiEnUS.audit.title,
    ])
  })

  it('routes a console navigation entry in place instead of reloading', async () => {
    window.history.replaceState({}, '', '/console/tenants')
    const user = userEvent.setup()
    renderConsoleArea()

    const nav = screen.getByRole('navigation')
    await user.click(
      within(nav).getByRole('link', { name: consoleUiEnUS.settings.title }),
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
      if (path === '/tenant/admin/tenants' && method === 'GET') {
        return Promise.resolve({
          tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }],
        } as T)
      }
      if (
        path === '/auth/admin/tenants/acme/invitations' &&
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
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(requestFn)
    const user = userEvent.setup()
    renderConsoleArea(<InvitationsPage />)

    await user.click(screen.getByLabelText(consoleUiEnUS.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Acme' }))
    await user.type(
      await screen.findByLabelText(consoleUiEnUS.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', {
        name: consoleUiEnUS.invitations.issue.submit,
      }),
    )

    // The issued token is turned into the app's own landing link (the host's
    // invitationPath), not dropped: the dead-code path is wired.
    expect(
      await screen.findByText(
        consoleUiEnUS.invitations.issued.link.replace(
          '{{email}}',
          'new@example.com',
        ),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        `${window.location.origin}${invitationPath('spd_inv_x')}`,
      ),
    ).toBeInTheDocument()
  })
})
