import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import type { AdminRoute } from './route.js'
import { AdminFeatureProvider } from './internal/features.js'
import { AdminRouteTableProvider } from './internal/route-table.js'
import {
  accountsAdminRoutes,
  auditAdminRoutes,
  adminRoutes,
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
import { AdminShell } from './AdminShell.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  recordFixture,
} from '../test-utils/operations.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const operations = createAdminOperations(makeAdminBackend())

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

describe('admin page declarations', () => {
  for (const [name, path, permission] of declarations) {
    it(`declares the ${name} page with its path, permission and label`, () => {
      const route = adminRoutes.find((candidate) => candidate.path === path)
      expect(route, `no declaration at ${path}`).toBeDefined()
      expect(route?.permission).toBe(permission)
      expect(route?.label).toBeDefined()
    })
  }

  it('keeps the platform permissions page separate from the organization roles page', () => {
    const platform = permissionsAdminRoutes[0]
    expect(platform?.path).toBe('/permissions')
    expect(platform?.permission).toBe('authz.role.read')
    expect(adminRoutes.some((route) => route.id === 'organization-roles')).toBe(
      false,
    )
    expect(adminRoutes.some((route) => route.path === '/roles')).toBe(false)
  })

  it('carries the list page permission on each detail declaration', () => {
    expect(
      adminRoutes.find((route) => route.path === '/tenants/:name')?.permission,
    ).toBe('tenant.read')
    expect(
      adminRoutes.find((route) => route.path === '/audit/:id')?.permission,
    ).toBe('audit.read')
  })

  it('carries the caller-session feature on the impersonation declaration', () => {
    expect(impersonationsAdminRoutes[0]?.requires).toContain(
      'authn.impersonation',
    )
  })

  it('gives every declaration a unique stable id', () => {
    const ids = adminRoutes.map((route) => route.id)
    for (const route of adminRoutes) {
      expect(route.id, `${route.path} carries no id`).toBeTruthy()
    }
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('resolves the tenant detail sibling links through the published declarations', async () => {
    const detail = tenantAdminRoutes.find((route) => route.path === '/tenants/:name')
    expect(detail).toBeDefined()
    const view = detail?.view({
      path: '/admin/tenants/acme',
      params: { name: 'acme' },
    })
    const published: readonly AdminRoute[] = [
      { id: 'permissions', path: '/admin/permissions', view: () => null },
      { id: 'sso', path: '/admin/sso', view: () => null },
    ]
    renderWithProviders(
      <AdminFeatureProvider hasFeature={() => true}>
        <AdminRouteTableProvider routes={published}>{view}</AdminRouteTableProvider>
      </AdminFeatureProvider>,
      { operations },
    )
    expect(await screen.findAllByText('Acme')).not.toHaveLength(0)
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/admin/permissions?tenant=acme')
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.sso }),
    ).toHaveAttribute('href', '/admin/sso?tenant=acme')
  })

  it('resolves a admin-relative sibling path against the matched admin root', async () => {
    const published: readonly AdminRoute[] = [
      { id: 'permissions', path: '/permissions', view: () => null },
      { id: 'sso', path: '/sso', view: () => null },
    ]
    const detail = tenantAdminRoutes.find((route) => route.path === '/tenants/:name')
    expect(detail).toBeDefined()
    renderWithProviders(
      <AdminFeatureProvider hasFeature={() => true}>
        <AdminRouteTableProvider routes={published}>
          {detail?.view({
            path: '/admin/tenants/acme',
            params: { name: 'acme' },
          })}
        </AdminRouteTableProvider>
      </AdminFeatureProvider>,
      { operations },
    )
    expect(await screen.findAllByText('Acme')).not.toHaveLength(0)
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/admin/permissions?tenant=acme')
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.sso }),
    ).toHaveAttribute('href', '/admin/sso?tenant=acme')
  })

  it('resolves a admin-relative sibling path at the origin when the admin root is empty', async () => {
    const published: readonly AdminRoute[] = [
      { id: 'permissions', path: 'permissions', view: () => null },
    ]
    const detail = tenantAdminRoutes.find((route) => route.path === '/tenants/:name')
    expect(detail).toBeDefined()
    renderWithProviders(
      <AdminFeatureProvider hasFeature={() => true}>
        <AdminRouteTableProvider routes={published}>
          {detail?.view({ path: '/tenants/acme', params: { name: 'acme' } })}
        </AdminRouteTableProvider>
      </AdminFeatureProvider>,
      { operations },
    )
    expect(await screen.findAllByText('Acme')).not.toHaveLength(0)
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/permissions?tenant=acme')
  })

  it('gives the dashboard its matched path as the admin root for its detail links', async () => {
    const dashboard = dashboardAdminRoutes[0]
    expect(dashboard?.path).toBe('/')
    const view = dashboard?.view({ path: '/admin', params: {} })
    renderWithProviders(
      <AdminFeatureProvider hasFeature={() => true}>{view}</AdminFeatureProvider>,
      {
        operations: createAdminOperations(
          makeAdminBackend({
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
    ).toHaveAttribute('href', '/admin/audit/rec-9')
  })

  it('gates every declaration, detail routes included, on a non-empty feature list', () => {
    for (const route of adminRoutes) {
      expect(route.requires, `${route.path} carries no requires`).toBeDefined()
      expect(route.requires?.length).toBeGreaterThan(0)
    }
  })

  it('gives every navigation entry a decorative icon and leaves the detail routes icon-less', () => {
    for (const route of adminRoutes) {
      if (route.label === undefined) {
        expect(route.icon).toBeUndefined()
      } else {
        expect(route.icon).toBeDefined()
      }
    }
  })

  it('renders an aria-hidden icon for every navigation entry without changing its accessible name', () => {
    renderWithProviders(
      <AdminShell routes={adminRoutes} title="Admin" hasFeature={() => true}>
        <h1>Home</h1>
      </AdminShell>,
      { operations },
    )
    const nav = screen.getByRole('navigation')
    const links = within(nav).getAllByRole('link')
    expect(links).toHaveLength(11)
    for (const link of links) {
      const icon = link.querySelector('svg')
      expect(icon, `no icon in ${link.textContent ?? ''}`).not.toBeNull()
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
    expect(adminRoutes.map((route) => route.path)).toEqual([
      '/',
      '/tenants',
      '/tenants/new',
      '/tenants/:name',
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
    for (const path of ['/tenants/new', '/tenants/:name', '/audit/:id']) {
      expect(adminRoutes.find((route) => route.path === path)?.label).toBeUndefined()
    }
    expect(
      adminRoutes.findIndex((route) => route.path === '/tenants/new'),
    ).toBeLessThan(
      adminRoutes.findIndex((route) => route.path === '/tenants/:name'),
    )
  })

  it('renders the translated navigation labels through the shell', () => {
    renderWithProviders(
      <AdminShell routes={adminRoutes} title="Admin" hasFeature={() => true}>
        <h1>Home</h1>
      </AdminShell>,
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
