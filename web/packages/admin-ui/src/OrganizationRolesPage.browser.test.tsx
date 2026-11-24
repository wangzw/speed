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
      username: '',
      active: true,
      granted_at: '2026-10-15T12:00:00Z',
    },
  ],
  next_cursor: '',
}

function route(roleName: string): readonly AdminTenantGrant[] {
  return roleName === ROLE_AUDITOR.role ? [GRANT_READ] : []
}

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

async function selectRole(roleName: string): Promise<void> {
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('button', {
      name: enUS.permissions.roles.actionsAria.replace('{{role}}', roleName),
    }),
  )
  await user.click(
    await screen.findByRole('menuitem', {
      name: enUS.permissions.roles.manageGrants,
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

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
  readonly reject: (reason: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const role = active.getAttribute('role')
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (role !== null) {
    parts.push(`role="${role}"`)
  }
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

// A hidden subtree still mounts its MUI table containers, which have no width to
// measure, so the geometry cases open the catalog first.
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
  const LONG_HOLDER_NAME =
    'Alexandria Cassandra Montgomery-Worthington the Third of the Long Name'

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
        display_name: LONG_HOLDER_NAME,
        username: '',
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
    await screen.findAllByText(LONG_HOLDER_NAME)
    await expandCatalog()

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

describe('OrganizationRolesPage write focus in Chromium', () => {
  it('keeps focus on the description save button while the save is in flight', async () => {
    await page.viewport(1200, 900)
    const written = deferred<void>()
    const scripted = makeAdminBackend({
      listTenantRoles: () => Promise.resolve([ROLE_AUDITOR, ROLE_BUILTIN]),
      listTenantPermissions: () => Promise.resolve(CATALOG),
      listTenantGrants: (_tenant, roleName) => Promise.resolve(route(roleName)),
      listTenantRoleBindings: () => Promise.resolve(HOLDERS),
      writeTenantRole: () => written.promise,
    })
    renderPage(scripted)
    await screen.findByText(ROLE_AUDITOR.role)

    const user = userEvent.setup()
    await user.click(
      await screen.findByRole('button', {
        name: enUS.permissions.roles.actionsAria.replace(
          '{{role}}',
          ROLE_AUDITOR.role,
        ),
      }),
    )
    await user.click(
      await screen.findByRole('menuitem', {
        name: enUS.permissions.roles.editDescription,
      }),
    )
    const panel = await screen.findByRole('dialog')
    const field = within(panel).getByLabelText(
      enUS.permissions.roles.description.descriptionLabel,
    )
    await user.clear(field)
    await user.type(field, 'Reviews every audit finding.')
    const save = within(panel).getByRole('button', {
      name: enUS.permissions.roles.description.save,
    })
    await user.click(save)

    await waitFor(() =>
      expect(within(save).getByRole('progressbar')).toBeInTheDocument(),
    )
    await nextFrame()
    expect(
      save,
      `focus while the save is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
    expect(save).toHaveAttribute('aria-busy', 'true')

    written.resolve()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
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
