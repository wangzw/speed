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
} from '../test-utils/operations.js'

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
    expect(within(nav).getByRole('link', { name: '概览' })).toBeInTheDocument()
    expect(within(nav).getByRole('link', { name: '审计记录' })).toBeInTheDocument()
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
      '概览',
      '租户',
      '账户',
      '会话',
      '冒充',
      '权限',
      '邀请与成员',
      'SSO 提供方',
      '设置',
      '短信模板',
      '审计记录',
    ])
  })
})
