/**
 * OrganizationRolesPage in a real browser.
 *
 * The page stacks the role catalog, the selected role's grants, its
 * read-only usage and the collapsed permission catalog in one column. Their
 * geometry is settled only by a browser: every table declares
 * container-query column priorities and content caps, the role actions are
 * pinned to the trailing edge, and the catalog's content region is hidden
 * rather than unmounted. The cases below cover those facts at the sm/md/lg
 * floors:
 *
 *   - the page and every table keep their content inside their own width at
 *     600, 900 and 1200px, with a role selected so the grants and usage
 *     tables are mounted as well;
 *   - over-long role names, descriptions, permission codes and account
 *     identifiers stay inside the same floors;
 *   - the permission catalog starts collapsed, expands into its grouped
 *     reference tables and collapses again from its own action;
 *   - the dark theme paints the page's surface and keeps the same columns.
 */

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useTheme } from '@mui/material/styles'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { OrganizationPermissionsPage } from './organization-pages.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import type { AdminBackend } from '../test-utils/operations.js'
import {
  ANONYMOUS_SNAPSHOT,
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import {
  expectNoHorizontalOverflow,
  TIER_FLOORS,
} from '../test-utils/browser-overflow.js'
import type {
  AdminTenantGrant,
  AdminTenantPermission,
  AdminTenantRole,
  AdminTenantRoleBindingPage,
} from './organization-operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const TENANT = 'acme'

const ROLE_AUDITOR: AdminTenantRole = {
  tenant: TENANT,
  role: 'auditor',
  builtin: false,
  can_manage: true,
  description: 'Reads audit records.',
  permission_count: 1,
  member_count: 1,
  created_at: '2026-01-01T00:00:00Z',
}

const ROLE_BUILTIN: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-admin',
  builtin: true,
  can_manage: true,
  description: 'Tenant administrator',
  permission_count: 1,
  member_count: 1,
  created_at: '2026-01-01T00:00:00Z',
}

const CATALOG: readonly AdminTenantPermission[] = [
  {
    permission: 'authz.tenant.role.read',
    description: 'Read roles',
    group: 'authorization',
    dangerous: false,
    grantable: true,
  },
  {
    permission: 'authn.membership.manage',
    description: 'Manage members',
    group: 'membership',
    dangerous: true,
    grantable: true,
  },
  {
    permission: 'settings.tenant.write',
    description: 'Write settings',
    group: 'settings',
    dangerous: true,
    grantable: true,
  },
]

const GRANT_READ: AdminTenantGrant = {
  tenant: TENANT,
  role: ROLE_AUDITOR.role,
  permission: 'authz.tenant.role.read',
  resource: 'tenant_role',
  action: 'read',
  created_at: '2026-01-01T00:00:00Z',
}

const HOLDERS: AdminTenantRoleBindingPage = {
  bindings: [
    {
      account: 'acc-1',
      display_name: 'Ada Lovelace',
      active: true,
      granted_at: '2026-10-15T12:00:00Z',
    },
  ],
  next_cursor: '',
}

function route(roleName: string): readonly AdminTenantGrant[] {
  return roleName === ROLE_AUDITOR.role ? [GRANT_READ] : []
}

/** The scripted backend the page runs against: two roles, the governed
 * catalog, one grant for the auditor and one holder page. A case swaps the
 * role list and catalog to place over-long values in the same tables. */
function backend(
  roles: readonly AdminTenantRole[] = [ROLE_AUDITOR, ROLE_BUILTIN],
  catalog: readonly AdminTenantPermission[] = CATALOG,
  holders: AdminTenantRoleBindingPage = HOLDERS,
): AdminBackend {
  return makeAdminBackend({
    listTenantRoles: () => Promise.resolve(roles),
    listTenantPermissions: () => Promise.resolve(catalog),
    listTenantGrants: (_tenant, roleName) => Promise.resolve(route(roleName)),
    listTenantRoleBindings: () => Promise.resolve(holders),
  })
}

/** A probe that reports the assembled theme's color mode and default
 * surface, so a dark-mode assertion compares against the theme rather than
 * against a color written into this test. */
function ThemeProbe() {
  const theme = useTheme()
  return (
    <div
      data-testid="theme-probe"
      data-mode={theme.palette.mode}
      style={{ backgroundColor: theme.palette.background.default }}
    />
  )
}

function renderPage(
  scripted: AdminBackend = backend(),
  options: { readonly defaultMode?: 'light' | 'dark' | 'system' } = {},
  probe?: ReactNode,
) {
  return renderWithProviders(
    <>
      {probe}
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(scripted)}
        hasFeature={() => true}
      >
        <OrganizationPermissionsPage />
      </OrganizationAdminProvider>
    </>,
    { language: 'en-US', ...options },
  )
}

/** The selected role's grants and usage tables only mount once a role is
 * chosen, so a geometry case selects the first role before measuring. */
async function selectRole(roleName: string): Promise<void> {
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', {
      name: enUS.permissions.roles.manageGrantsAria.replace('{{role}}', roleName),
    }),
  )
  await screen.findByText(
    enUS.permissions.grants.forRole.replace('{{role}}', roleName),
  )
}

beforeEach(() => {
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      permissionSets: {
        tenant: [
          'authz.tenant.role.read',
          'authz.tenant.role.manage',
          'authn.membership.manage',
        ],
        system: null,
      },
    }),
  )
})

afterEach(() => {
  attachSession(createStepUpSession(ANONYMOUS_SNAPSHOT))
})

/** Expands the collapsed permission catalog so its grouped reference tables
 * are laid out. A hidden subtree still mounts its MUI table containers, and
 * those have no width to measure; the geometry cases open the catalog first
 * so every rendered table is part of the check. */
async function expandCatalog(): Promise<void> {
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('button', { name: enUS.permissions.catalog.expand }),
  )
  await waitFor(() =>
    expect(
      screen.getByLabelText(enUS.permissions.catalog.search.label),
    ).toBeVisible(),
  )
}

describe('OrganizationRolesPage horizontal overflow in Chromium', () => {
  for (const width of TIER_FLOORS) {
    it(`keeps the role, grant and usage tables inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      renderPage()

      await screen.findByText(ROLE_AUDITOR.role)
      await selectRole(ROLE_AUDITOR.role)
      await screen.findByText('Ada Lovelace')
      await expandCatalog()
      await expectNoHorizontalOverflow()
    })
  }
})

describe('OrganizationRolesPage over-long values in Chromium', () => {
  const LONG_ROLE = 'tenant-operations-reviewer-with-a-long-unbroken-name-0001'
  const LONG_DESCRIPTION =
    'A long free-text description that does not fit in the description column on one line and keeps going well past it'
  const LONG_PERMISSION =
    'a-very-long-namespace.a-very-long-resource-name.an-equally-long-action'
  const LONG_ACCOUNT = 'account-identifier-with-a-long-unbroken-value-0000000001'

  const LONG_ROLES: readonly AdminTenantRole[] = [
    {
      ...ROLE_AUDITOR,
      role: LONG_ROLE,
      description: LONG_DESCRIPTION,
    },
  ]

  const LONG_CATALOG: readonly AdminTenantPermission[] = [
    {
      permission: LONG_PERMISSION,
      description:
        'A readable name that is itself long enough to wrap across the permission cell',
      group: 'authorization',
      dangerous: true,
      grantable: true,
    },
  ]

  const LONG_GRANT: AdminTenantGrant = {
    tenant: TENANT,
    role: LONG_ROLE,
    permission: LONG_PERMISSION,
    resource: 'a-very-long-resource-name',
    action: 'an-equally-long-action',
    created_at: '2026-01-01T00:00:00Z',
  }

  const LONG_HOLDERS: AdminTenantRoleBindingPage = {
    bindings: [
      {
        account: LONG_ACCOUNT,
        display_name:
          'Alexandria Cassandra Montgomery-Worthington the Third of the Long Name',
        active: true,
        granted_at: '2026-10-15T12:00:00Z',
      },
    ],
    next_cursor: '',
  }

  it('caps over-long role, permission and account values at every tier floor', async () => {
    await page.viewport(1200, 900)
    const scripted = makeAdminBackend({
      listTenantRoles: () => Promise.resolve(LONG_ROLES),
      listTenantPermissions: () => Promise.resolve(LONG_CATALOG),
      listTenantGrants: () => Promise.resolve([LONG_GRANT]),
      listTenantRoleBindings: () => Promise.resolve(LONG_HOLDERS),
    })
    renderPage(scripted)

    await screen.findByText(LONG_ROLE)
    await selectRole(LONG_ROLE)
    await screen.findAllByText(LONG_PERMISSION)
    // The binding row names its account by the display name the binding
    // carries, never by the account id.
    await screen.findAllByText(LONG_HOLDERS.bindings[0].display_name)
    await expandCatalog()

    // The helper pins each table's own containment box to the sm, md and lg
    // floors and requires the columns visible in that tier to fit.
    await expectNoHorizontalOverflow()
  })
})

describe('OrganizationRolesPage permission catalog in Chromium', () => {
  it('starts collapsed, expands into its grouped tables and collapses again', async () => {
    await page.viewport(1200, 900)
    renderPage()
    await screen.findByText(ROLE_AUDITOR.role)
    const user = userEvent.setup()

    const region = screen.getByRole('region', {
      name: enUS.permissions.catalog.title,
    })
    const expand = within(region).getByRole('button', {
      name: enUS.permissions.catalog.expand,
    })
    expect(expand).toHaveAttribute('aria-expanded', 'false')
    expect(
      within(region).getByLabelText(enUS.permissions.catalog.search.label),
    ).not.toBeVisible()

    await user.click(expand)
    expect(
      within(region).getByRole('button', {
        name: enUS.permissions.catalog.collapse,
      }),
    ).toHaveAttribute('aria-expanded', 'true')
    expect(
      await within(region).findByRole('heading', {
        name: enUS.permissions.catalog.group.authorization,
      }),
    ).toBeVisible()
    await expectNoHorizontalOverflow()

    await user.click(
      within(region).getByRole('button', {
        name: enUS.permissions.catalog.collapse,
      }),
    )
    expect(
      within(region).getByLabelText(enUS.permissions.catalog.search.label),
    ).not.toBeVisible()
  })
})

describe('OrganizationRolesPage dark mode in Chromium', () => {
  it('paints the page on the dark surface and keeps the tables inside the width', async () => {
    await page.viewport(1200, 900)
    renderPage(backend(), { defaultMode: 'dark' }, <ThemeProbe />)
    await screen.findByText(ROLE_AUDITOR.role)
    await selectRole(ROLE_AUDITOR.role)
    await screen.findByText('Ada Lovelace')
    await expandCatalog()

    const probe = screen.getByTestId('theme-probe')
    expect(probe.dataset.mode).toBe('dark')
    const surface = getComputedStyle(probe).backgroundColor
    expect(getComputedStyle(document.body).backgroundColor).toBe(surface)
    const channels = surface.match(/\d+/g)?.map(Number) ?? []
    expect(channels).toHaveLength(3)
    expect(
      Math.max(...channels),
      `the dark surface is not dark (${surface})`,
    ).toBeLessThan(128)

    await expectNoHorizontalOverflow()
    await waitFor(() =>
      expect(
        screen.getAllByRole('columnheader', {
          name: enUS.permissions.roles.column.role,
        }).length,
      ).toBeGreaterThan(0),
    )
  })
})
