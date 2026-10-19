/**
 * The console page declarations.
 *
 * Each administration capability contributes one declaration with its
 * path, its permission code and a navigation label; consoleRoutes is the
 * whole set in navigation order; and rendering the declarations through
 * ConsoleShell shows the translated navigation labels (the label element
 * resolves its text in the host's language).
 */

import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import type { ConsoleRoute } from './route.js'
import { ConsoleFeatureProvider } from './internal/features.js'
import { ConsoleRouteTableProvider } from './internal/route-table.js'
import {
  accountsAdminRoutes,
  auditAdminRoutes,
  consoleRoutes,
  dashboardAdminRoutes,
  impersonationsAdminRoutes,
  invitationsAdminRoutes,
  permissionsAdminRoutes,
  sessionsAdminRoutes,
  settingsAdminRoutes,
  smsAdminRoutes,
  ssoAdminRoutes,
  tenantAdminRoutes,
} from './routes.js'
import { ConsoleShell } from './ConsoleShell.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  recordFixture,
} from '../test-utils/operations.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const operations = createConsoleOperations(makeConsoleBackend())

/** The path / permission each single-page declaration carries. */
const declarations: readonly [string, string, string][] = [
  ['dashboard', dashboardAdminRoutes[0]?.path ?? '', 'tenant.read'],
  ['tenants', tenantAdminRoutes[0]?.path ?? '', 'tenant.read'],
  ['accounts', accountsAdminRoutes[0]?.path ?? '', 'authn.account.read'],
  ['sessions', sessionsAdminRoutes[0]?.path ?? '', 'authn.session.read'],
  ['impersonations', impersonationsAdminRoutes[0]?.path ?? '', 'authn.impersonation.read'],
  ['permissions', permissionsAdminRoutes[0]?.path ?? '', 'authz.role.read'],
  ['invitations', invitationsAdminRoutes[0]?.path ?? '', 'authn.membership.manage'],
  ['sso', ssoAdminRoutes[0]?.path ?? '', 'authn.idp.manage'],
  ['settings', settingsAdminRoutes[0]?.path ?? '', 'settings.read'],
  ['sms', smsAdminRoutes[0]?.path ?? '', 'sms.template.read'],
  ['audit', auditAdminRoutes[0]?.path ?? '', 'audit.read'],
]

describe('console page declarations', () => {
  for (const [name, path, permission] of declarations) {
    it(`declares the ${name} page with its path, permission and label`, () => {
      const route = consoleRoutes.find((candidate) => candidate.path === path)
      expect(route, `no declaration at ${path}`).toBeDefined()
      expect(route?.permission).toBe(permission)
      expect(route?.label).toBeDefined()
    })
  }

  it('carries the list page permission on each detail declaration', () => {
    expect(
      consoleRoutes.find((route) => route.path === '/tenants/:id')?.permission,
    ).toBe('tenant.read')
    expect(
      consoleRoutes.find((route) => route.path === '/audit/:id')?.permission,
    ).toBe('audit.read')
  })

  it('carries the caller-session feature on the impersonation declaration', () => {
    // The page holds no page-level gate for the caller-session surface: the
    // declaration names the feature, and the host narrows the table by
    // feature presence before it reaches the shell.
    expect(impersonationsAdminRoutes[0]?.requires).toContain(
      'authn.impersonation',
    )
  })

  it('gives every declaration a unique stable id', () => {
    const ids = consoleRoutes.map((route) => route.id)
    for (const route of consoleRoutes) {
      expect(route.id, `${route.path} carries no id`).toBeTruthy()
    }
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('resolves the tenant detail sibling links through the published declarations', async () => {
    // The console may be mounted under a host prefix, so the page looks the
    // sibling up by declaration id instead of guessing a path from a
    // literal segment.
    const detail = tenantAdminRoutes.find((route) => route.path === '/tenants/:id')
    expect(detail).toBeDefined()
    const view = detail?.view({
      path: '/admin/console/tenants/acme',
      params: { id: 'acme' },
    })
    const published: readonly ConsoleRoute[] = [
      { id: 'permissions', path: '/admin/console/permissions', view: () => null },
      { id: 'sso', path: '/admin/console/sso', view: () => null },
    ]
    renderWithProviders(
      <ConsoleFeatureProvider hasFeature={() => true}>
        <ConsoleRouteTableProvider routes={published}>{view}</ConsoleRouteTableProvider>
      </ConsoleFeatureProvider>,
      { operations },
    )
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/admin/console/permissions?tenant=acme')
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.sso }),
    ).toHaveAttribute('href', '/admin/console/sso?tenant=acme')
  })

  it('resolves a console-relative sibling path against the matched console root', async () => {
    // A host that publishes console-relative patterns makes the
    // declaration's path resolve against the root the detail route computed
    // from its matched path -- here the console mounted under /admin/console.
    const published: readonly ConsoleRoute[] = [
      { id: 'permissions', path: '/permissions', view: () => null },
      { id: 'sso', path: '/sso', view: () => null },
    ]
    const detail = tenantAdminRoutes.find((route) => route.path === '/tenants/:id')
    expect(detail).toBeDefined()
    renderWithProviders(
      <ConsoleFeatureProvider hasFeature={() => true}>
        <ConsoleRouteTableProvider routes={published}>
          {detail?.view({
            path: '/admin/console/tenants/acme',
            params: { id: 'acme' },
          })}
        </ConsoleRouteTableProvider>
      </ConsoleFeatureProvider>,
      { operations },
    )
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/admin/console/permissions?tenant=acme')
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.sso }),
    ).toHaveAttribute('href', '/admin/console/sso?tenant=acme')
  })

  it('resolves a console-relative sibling path at the origin when the console root is empty', async () => {
    // A console mounted at the origin has an empty root: its last two path
    // segments are the list segment and the id, and a console-relative
    // pattern resolves to the path every host serves it at.
    const published: readonly ConsoleRoute[] = [
      { id: 'permissions', path: 'permissions', view: () => null },
    ]
    const detail = tenantAdminRoutes.find((route) => route.path === '/tenants/:id')
    expect(detail).toBeDefined()
    renderWithProviders(
      <ConsoleFeatureProvider hasFeature={() => true}>
        <ConsoleRouteTableProvider routes={published}>
          {detail?.view({ path: '/tenants/acme', params: { id: 'acme' } })}
        </ConsoleRouteTableProvider>
      </ConsoleFeatureProvider>,
      { operations },
    )
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/permissions?tenant=acme')
  })

  it('gives the dashboard its matched path as the console root for its detail links', async () => {
    const dashboard = dashboardAdminRoutes[0]
    expect(dashboard?.path).toBe('/')
    const view = dashboard?.view({ path: '/console', params: {} })
    renderWithProviders(
      <ConsoleFeatureProvider hasFeature={() => true}>{view}</ConsoleFeatureProvider>,
      {
        operations: createConsoleOperations(
          makeConsoleBackend({
            listRecords: () =>
              Promise.resolve({
                entries: [recordFixture({ id: 'rec-9', action: 'tenant.read' })],
                next: '',
              }),
          }),
        ),
      },
    )
    expect(
      await screen.findByRole('link', { name: 'tenant.read' }),
    ).toHaveAttribute('href', '/console/audit/rec-9')
  })

  it('gates every declaration, detail routes included, on a non-empty feature list', () => {
    for (const route of consoleRoutes) {
      expect(route.requires, `${route.path} carries no requires`).toBeDefined()
      expect(route.requires?.length).toBeGreaterThan(0)
    }
  })

  it('gives every navigation entry a decorative icon and leaves the detail routes icon-less', () => {
    for (const route of consoleRoutes) {
      if (route.label === undefined) {
        // A page that is reachable but not listed has no navigation entry,
        // so it carries no navigation icon.
        expect(route.icon).toBeUndefined()
      } else {
        expect(route.icon).toBeDefined()
      }
    }
  })

  it('renders an aria-hidden icon for every navigation entry without changing its accessible name', () => {
    renderWithProviders(
      <ConsoleShell routes={consoleRoutes} title="Console" hasFeature={() => true}>
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    const nav = screen.getByRole('navigation')
    const links = within(nav).getAllByRole('link')
    expect(links).toHaveLength(11)
    for (const link of links) {
      const icon = link.querySelector('svg')
      expect(icon, `no icon in ${link.textContent ?? ''}`).not.toBeNull()
      // Decorative: hidden from assistive tech, so the label alone names
      // the entry.
      expect(icon).toHaveAttribute('aria-hidden', 'true')
      expect(icon).toHaveAttribute('focusable', 'false')
    }
    expect(
      within(nav).getByRole('link', { name: zhCN.dashboard.title }),
    ).toBeInTheDocument()
    expect(
      within(nav).getByRole('link', { name: zhCN.audit.title }),
    ).toBeInTheDocument()
  })

  it('combines every page in navigation order, the detail routes unlabelled', () => {
    expect(consoleRoutes.map((route) => route.path)).toEqual([
      '/',
      '/tenants',
      '/tenants/:id',
      '/accounts',
      '/sessions',
      '/impersonations',
      '/permissions',
      '/invitations',
      '/sso',
      '/settings',
      '/sms',
      '/audit',
      '/audit/:id',
    ])
    // The detail routes are reachable but not listed in the navigation.
    for (const path of ['/tenants/:id', '/audit/:id']) {
      expect(consoleRoutes.find((route) => route.path === path)?.label).toBeUndefined()
    }
  })

  it('renders the translated navigation labels through the shell', () => {
    renderWithProviders(
      <ConsoleShell routes={consoleRoutes} title="Console" hasFeature={() => true}>
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    const nav = screen.getByRole('navigation')
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      zhCN.dashboard.title,
      zhCN.tenants.title,
      zhCN.accounts.title,
      zhCN.sessions.title,
      zhCN.impersonations.title,
      zhCN.permissions.title,
      zhCN.invitations.title,
      zhCN.sso.title,
      zhCN.settings.title,
      zhCN.sms.title,
      zhCN.audit.title,
    ])
  })
})
