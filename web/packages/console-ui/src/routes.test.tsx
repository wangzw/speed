/**
 * The console page declarations.
 *
 * Each administration capability contributes one declaration with its
 * path, its permission code and a navigation label; consoleRoutes is the
 * three of them in navigation order; and rendering the declarations
 * through ConsoleShell shows the translated navigation labels (the label
 * element resolves its text in the host's language).
 */

import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import {
  auditAdminRoutes,
  consoleRoutes,
  settingsAdminRoutes,
  tenantAdminRoutes,
} from './routes.js'
import { ConsoleShell } from './ConsoleShell.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'

const operations = createConsoleOperations(makeConsoleBackend())

describe('console page declarations', () => {
  it('declares the roster page with its path and permission', () => {
    expect(tenantAdminRoutes).toHaveLength(1)
    expect(tenantAdminRoutes[0]?.path).toBe('/tenants')
    expect(tenantAdminRoutes[0]?.permission).toBe('tenant.read')
    expect(tenantAdminRoutes[0]?.label).toBeDefined()
  })

  it('declares the settings page with its path and permission', () => {
    expect(settingsAdminRoutes).toHaveLength(1)
    expect(settingsAdminRoutes[0]?.path).toBe('/settings')
    expect(settingsAdminRoutes[0]?.permission).toBe('settings.read')
  })

  it('declares the audit page with its path and permission', () => {
    expect(auditAdminRoutes).toHaveLength(1)
    expect(auditAdminRoutes[0]?.path).toBe('/audit')
    expect(auditAdminRoutes[0]?.permission).toBe('audit.read')
  })

  it('combines the three pages in navigation order', () => {
    expect(consoleRoutes.map((route) => route.path)).toEqual([
      '/tenants',
      '/settings',
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
    ).toEqual(['租户', '设置', '审计记录'])
  })
})
