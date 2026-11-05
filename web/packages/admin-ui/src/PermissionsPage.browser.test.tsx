/**
 * PermissionsPage in a real browser: the tenant-scoped authorization
 * tables at the sm/md/lg floor widths with fixed data. The tenant is
 * seeded through the query parameter the page opens on, so the case
 * exercises the settled tables rather than the selector prompt.
 */
import { afterEach, describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { PermissionsPage } from './PermissionsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import type {
  AdminBinding,
  AdminGrant,
  AdminPermission,
  AdminRole,
} from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [{ id: 'acme', alias: 'Acme', status: 'active' as const }]

const ROLES: readonly AdminRole[] = [
  {
    tenant: 'acme',
    role: 'editor',
    builtin: false,
    can_manage: true,
    description: '',
    permission_count: 0,
    created_at: '2026-10-15T12:00:00Z',
  },
  {
    tenant: 'acme',
    role: 'viewer',
    builtin: false,
    can_manage: true,
    description: '',
    permission_count: 0,
    created_at: '2026-10-15T12:00:00Z',
  },
]

const PERMISSIONS: readonly AdminPermission[] = [
  { permission: 'deal.read', description: '', group: '', dangerous: false, grantable: true },
  { permission: 'deal.write', description: '', group: '', dangerous: false, grantable: true },
]

const GRANTS: readonly AdminGrant[] = [
  {
    tenant: 'acme',
    role: 'editor',
    permission: 'deal.read',
    resource: 'deal',
    action: 'read',
    created_at: '2026-10-15T12:00:00Z',
  },
]

const BINDINGS: readonly AdminBinding[] = [
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

      const operations = createAdminOperations(
        makeAdminBackend({
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

/**
 * A role name and a permission code are identifiers with no length bound in
 * the console's contract: a tenant names its own roles, and the catalog
 * spells each permission as `namespace.resource.action`. At the sm floor
 * the roles table shows its identity, the actions, and nothing else, so an
 * unbounded identifier is what would push the row actions off the table
 * before the scroll fallback appears.
 */
const LONG_ROLE = 'tenant-operations-reviewer-with-a-long-unbroken-name-0001'
const LONG_DESCRIPTION =
  'A long free-text description that does not fit in the column on one line'
const LONG_RESOURCE = 'a-very-long-resource-name-that-keeps-going'
const LONG_ACTION = 'an-equally-long-action-name'
const LONG_PERMISSION = `${LONG_RESOURCE}.${LONG_ACTION}`

const LONG_ROLES: readonly AdminRole[] = [
  {
    tenant: 'acme',
    role: LONG_ROLE,
    builtin: false,
    can_manage: true,
    description: LONG_DESCRIPTION,
    permission_count: 0,
    created_at: '2026-10-15T12:00:00Z',
  },
]

const LONG_PERMISSIONS: readonly AdminPermission[] = [
  { permission: LONG_PERMISSION, description: '', group: '', dangerous: false, grantable: true },
]

const LONG_GRANTS: readonly AdminGrant[] = [
  {
    tenant: 'acme',
    role: LONG_ROLE,
    permission: LONG_PERMISSION,
    resource: LONG_RESOURCE,
    action: LONG_ACTION,
    created_at: '2026-10-15T12:00:00Z',
  },
]

describe('PermissionsPage unbounded identifiers in Chromium', () => {
  it('keeps every table inside each tier floor for a long role and permission', async () => {
    await page.viewport(1200, 900)
    seedTenant('acme')

    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listPermissions: () => Promise.resolve(LONG_PERMISSIONS),
        listRoles: () => Promise.resolve(LONG_ROLES),
        listGrants: () => Promise.resolve(LONG_GRANTS),
        listBindings: () => Promise.resolve([]),
      }),
    )
    renderWithProviders(<PermissionsPage />, { operations, language: 'en-US' })

    await screen.findByText(LONG_ROLE)
    const user = userEvent.setup()
    await user.click(
      screen.getByRole('button', {
        name: enUS.permissions.roles.manageGrantsAria.replace(
          '{{role}}',
          LONG_ROLE,
        ),
      }),
    )
    // The code appears in the catalog and in the selected role's grant
    // row; both tables must be settled before the widths are measured.
    await screen.findAllByText(LONG_PERMISSION)

    // The helper pins each table's own containment box to the sm, md and
    // lg floors and requires the columns visible in that tier to fit.
    await expectNoHorizontalOverflow()
  })
})
