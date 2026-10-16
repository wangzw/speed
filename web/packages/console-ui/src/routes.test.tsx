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

  it('combines every page in navigation order', () => {
    expect(consoleRoutes.map((route) => route.path)).toEqual([
      '/tenants',
      '/accounts',
      '/sessions',
      '/impersonations',
      '/permissions',
      '/invitations',
      '/sso',
      '/settings',
      '/sms',
      '/audit',
    ])
  })

  it('renders the translated navigation labels through the shell', () => {
    renderWithProviders(
      <ConsoleShell routes={consoleRoutes} title="Console">
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
