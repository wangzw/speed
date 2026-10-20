/**
 * PermissionsPage in a real browser: the tenant-scoped authorization
 * tables at the sm/md/lg floor widths with fixed data. The tenant is
 * seeded through the query parameter the page opens on, so the case
 * exercises the settled tables rather than the selector prompt.
 */
import { afterEach, describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { PermissionsPage } from './PermissionsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createConsoleOperations, makeConsoleBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import type {
  ConsoleBinding,
  ConsoleGrant,
  ConsolePermission,
  ConsoleRole,
} from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [{ id: 'acme', alias: 'Acme', status: 'active' as const }]

const ROLES: readonly ConsoleRole[] = [
  {
    tenant: 'acme',
    role: 'editor',
    builtin: false,
    description_key: 'role.editor',
    created_at: '2026-10-15T12:00:00Z',
  },
  {
    tenant: 'acme',
    role: 'viewer',
    builtin: false,
    description_key: 'role.viewer',
    created_at: '2026-10-15T12:00:00Z',
  },
]

const PERMISSIONS: readonly ConsolePermission[] = [
  { permission: 'deal.read', description: '' },
  { permission: 'deal.write', description: '' },
]

const GRANTS: readonly ConsoleGrant[] = [
  {
    tenant: 'acme',
    role: 'editor',
    resource: 'deal',
    action: 'read',
    created_at: '2026-10-15T12:00:00Z',
  },
]

const BINDINGS: readonly ConsoleBinding[] = [
  {
    tenant: 'acme',
    account: 'account-identifier-with-a-long-value-0001',
    role: 'editor',
    granted_at: '2026-10-15T12:00:00Z',
  },
]

afterEach(clearTenant)

describe('PermissionsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the authorization tables inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createConsoleOperations(
        makeConsoleBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listPermissions: () => Promise.resolve(PERMISSIONS),
          listRoles: () => Promise.resolve(ROLES),
          listGrants: () => Promise.resolve(GRANTS),
          listBindings: () => Promise.resolve(BINDINGS),
        }),
      )
      renderWithProviders(<PermissionsPage />, { operations, language: 'en-US' })

      await screen.findAllByText('editor')
      await expectNoHorizontalOverflow()
    })
  }
})
