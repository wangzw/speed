/**
 * The organization administrator's roles and permissions page.
 *
 * The cases pin the page's product shape: the role catalog and its
 * create/edit/delete controls, the read-only tiers (builtin, host-protected
 * and no-manage), the selected role's grants with the server-reported
 * permission driving revoke, the grantable filter and the high-risk
 * confirmation, the read-only usage section with its cursor paging and its
 * permission-gated members-page entry, the collapsed permission reference
 * with grouping and search, the page-level refresh, the cleared selection
 * and both color modes' shared page shape.
 *
 * The operations bundle is the scripted admin-ui backend, so the tests
 * observe the parameters the reads carry and the exact write inputs rather
 * than stubbing the page's hooks. The session-backed permission gates are
 * driven through auth-core's attached session, the same seam the host uses.
 */

import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachSession } from '@speed/auth-core'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { OrganizationPermissionsPage } from './organization-pages.js'
import type { AdminRoute } from './route.js'
import type {
  AdminTenantGrant,
  AdminTenantGrantRef,
  AdminTenantPermission,
  AdminTenantRole,
  AdminTenantRoleBindingPage,
  AdminTenantRoleBindingsQuery,
  AdminTenantWriteRoleInput,
} from './organization-operations.js'
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
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'

/** Fills a locale template's `{{name}}` placeholders. */
function text(template: string, values: Record<string, string | number> = {}): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    String(values[key] ?? match),
  )
}

const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

function role(overrides: Partial<AdminTenantRole> = {}): AdminTenantRole {
  return {
    tenant: TENANT,
    role: 'auditor',
    builtin: false,
    can_manage: true,
    description: 'Reads audit records.',
    permission_count: 2,
    member_count: 2,
    created_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

const ROLE_AUDITOR = role()
const ROLE_BUILTIN = role({
  role: 'tenant-admin',
  builtin: true,
  description: 'Tenant administrator',
  permission_count: 1,
  member_count: 1,
})
const ROLE_PROTECTED = role({
  role: 'tenant-owner',
  description: '',
  permission_count: 0,
  member_count: 0,
})
const DEFAULT_ROLES: readonly AdminTenantRole[] = [
  ROLE_AUDITOR,
  ROLE_BUILTIN,
  ROLE_PROTECTED,
]

function permission(
  code: string,
  description: string,
  group: string,
  dangerous = false,
  grantable = true,
): AdminTenantPermission {
  return { permission: code, description, group, dangerous, grantable }
}

const CATALOG: readonly AdminTenantPermission[] = [
  permission('authz.tenant.role.read', 'Read roles', 'authorization'),
  permission('authz.tenant.role.manage', 'Manage roles', 'authorization', true),
  permission('authn.membership.manage', 'Manage members', 'membership', true),
  permission('settings.tenant.read', 'Read settings', 'settings', false, false),
  permission('settings.tenant.write', 'Write settings', 'settings', true),
  permission('legacy.read', 'Legacy read', 'not-a-group', false, false),
]

/** The default catalog plus the tenant profile permissions the server groups
 * under `profile`, on top of the deliberately unknown `not-a-group` entry. */
const PROFILE_CATALOG: readonly AdminTenantPermission[] = [
  ...CATALOG,
  permission('tenant.profile.read', 'Read the organization profile', 'profile'),
  permission('tenant.profile.write', 'Write the organization profile', 'profile'),
]

function grant(
  code: string,
  resource: string,
  action: string,
  roleName = ROLE_AUDITOR.role,
): AdminTenantGrant {
  return {
    tenant: TENANT,
    role: roleName,
    permission: code,
    resource,
    action,
    created_at: '2026-01-01T00:00:00Z',
  }
}

const GRANT_READ = grant('authz.tenant.role.read', 'tenant_role', 'read')
const GRANT_UNMAPPABLE = grant('', 'tenant_role', 'write')

const HOLDER_ADA = {
  account: 'acc-1',
  display_name: 'Ada Lovelace',
  username: '',
  active: true,
  granted_at: '2026-10-15T12:00:00Z',
}
const HOLDER_BOB = {
  account: 'acc-2',
  display_name: 'Bob Stone',
  username: '',
  active: false,
  granted_at: '2026-10-20T08:30:00Z',
}
const HOLDER_UNKNOWN = {
  account: 'acc-3',
  display_name: '',
  username: '',
  granted_at: '2026-10-21T09:00:00Z',
}

const PAGE_ONE: AdminTenantRoleBindingPage = {
  bindings: [HOLDER_ADA, HOLDER_BOB],
  next_cursor: 'acc-2',
}
const PAGE_TWO: AdminTenantRoleBindingPage = {
  bindings: [HOLDER_UNKNOWN],
  next_cursor: '',
}

interface Recorder {
  roleReads: number
  permissionReads: number
  grantReads: number
  bindingReads: Array<AdminTenantRoleBindingsQuery | undefined>
  writes: AdminTenantWriteRoleInput[]
  removes: string[]
  grants: AdminTenantGrantRef[]
  revokes: AdminTenantGrantRef[]
}

interface Scripted {
  readonly backend: AdminBackend
  readonly recorder: Recorder
}

/** The scripted backend: the default catalog, three roles, two holder pages
 * and recorded writes. A test overrides the member it exercises. */
function scripted(overrides: Partial<AdminBackend> = {}): Scripted {
  const recorder: Recorder = {
    roleReads: 0,
    permissionReads: 0,
    grantReads: 0,
    bindingReads: [],
    writes: [],
    removes: [],
    grants: [],
    revokes: [],
  }
  const backend = makeAdminBackend({
    listTenantRoles: () => {
      recorder.roleReads += 1
      return Promise.resolve(DEFAULT_ROLES)
    },
    listTenantPermissions: () => {
      recorder.permissionReads += 1
      return Promise.resolve(CATALOG)
    },
    listTenantGrants: (tenant, roleName) => {
      recorder.grantReads += 1
      return Promise.resolve(
        roleName === ROLE_AUDITOR.role
          ? [GRANT_READ, GRANT_UNMAPPABLE]
          : roleName === ROLE_BUILTIN.role
            ? [GRANT_READ]
            : [],
      )
    },
    listTenantRoleBindings: (tenant, roleName, params) => {
      recorder.bindingReads.push(params)
      return Promise.resolve(params?.after === 'acc-2' ? PAGE_TWO : PAGE_ONE)
    },
    writeTenantRole: (input) => {
      recorder.writes.push(input)
      return Promise.resolve()
    },
    removeTenantRole: (input) => {
      recorder.removes.push(input.role)
      return Promise.resolve()
    },
    grantTenantPermission: (input) => {
      recorder.grants.push(input)
      return Promise.resolve()
    },
    revokeTenantPermission: (input) => {
      recorder.revokes.push(input)
      return Promise.resolve()
    },
    ...overrides,
  })
  return { backend, recorder }
}

/** The tenant permissions the default render's session attaches: the page
 * may read roles and memberships, but every write decision still comes from
 * the server's `can_manage`. */
const FULL_TENANT_PERMISSIONS = [
  'authz.tenant.role.read',
  'authz.tenant.role.manage',
  'authn.membership.manage',
]

function attachTenantPermissions(permissions: readonly string[] | null): void {
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      permissionSets: { tenant: permissions, system: null },
    }),
  )
}

/** The host's prefixed declaration, the shape the demo publishes. */
const INVITATIONS_ROUTE: AdminRoute = {
  id: 'organization-invitations',
  path: '/organization/admin/invitations',
  view: () => null,
}

interface RenderOptions {
  readonly protectedRoles?: readonly string[]
  readonly routes?: readonly AdminRoute[]
}

/** The titled section a heading opens, which holds that section's content. */
function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

function renderRolesPage(
  backend: AdminBackend,
  options: RenderOptions = {},
): ReturnType<typeof renderWithProviders> {
  return renderWithProviders(
    <OrganizationAdminProvider
      tenant={TENANT}
      operations={createAdminOperations(backend)}
      hasFeature={() => true}
      routes={options.routes ?? []}
      protectedRoles={options.protectedRoles ?? []}
    >
      <OrganizationPermissionsPage />
    </OrganizationAdminProvider>,
  )
}

beforeEach(() => {
  attachTenantPermissions(FULL_TENANT_PERMISSIONS)
})

afterEach(() => {
  attachSession(createStepUpSession(ANONYMOUS_SNAPSHOT))
})

/** Waits past the two-step confirm's arming lockout. */
async function waitForArmedConfirm(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
}

async function selectRole(user: ReturnType<typeof userEvent.setup>, name: string): Promise<void> {
  await user.click(
    await screen.findByRole('button', {
      name: text(zhCN.permissions.roles.manageGrantsAria, { role: name }),
    }),
  )
}

describe('the role catalog', () => {
  it('renders the role name, description, counts and builtin mark', async () => {
    const { backend } = scripted()
    renderRolesPage(backend)
    expect(await screen.findByText(ROLE_AUDITOR.role)).toBeInTheDocument()
    expect(screen.getByText(ROLE_AUDITOR.description)).toBeInTheDocument()
    expect(screen.getByText('tenant-admin')).toBeInTheDocument()
    // Two counts columns plus the builtin column.
    expect(
      screen.getAllByRole('columnheader', {
        name: zhCN.permissions.roles.column.permissions,
      }).length,
    ).toBeGreaterThan(0)
    expect(
      screen.getAllByRole('columnheader', {
        name: zhCN.permissions.roles.column.members,
      }).length,
    ).toBeGreaterThan(0)
  })

  it('keeps the empty-value dash for a null member count in the role table', async () => {
    // The wire can carry an explicit null; the generated type models the
    // server's "no identity reader" as an absent member, so the fixture
    // spells the runtime value.
    const { backend } = scripted({
      listTenantRoles: () =>
        Promise.resolve([
          role({ role: 'support', member_count: null as unknown as number }),
        ]),
    })
    renderRolesPage(backend)
    const table = await screen.findByRole('table')
    const row = within(table).getByText('support').closest('tr')
    expect(row).not.toBeNull()
    // The role table's cell keeps the shared empty-value dash; the explicit
    // "member state unavailable" sentence belongs to the usage summary, not
    // to this cell.
    expect(
      within(row as HTMLElement).getByText(zhCN.common.emptyValue),
    ).toBeInTheDocument()
    expect(
      within(row as HTMLElement).queryByText(
        zhCN.permissions.usage.memberCountUnavailable,
      ),
    ).toBeNull()
  })

  it('creates a role with its free-text description', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)

    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      'reviewer',
    )
    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.descriptionLabel),
      'Reviews audit findings.',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.permissions.roles.create.submit,
      }),
    )

    await waitFor(() =>
      expect(recorder.writes).toEqual([
        {
          tenant: TENANT,
          role: 'reviewer',
          description: 'Reviews audit findings.',
        },
      ]),
    )
    expect(
      await screen.findByText(
        text(zhCN.permissions.roles.saved, { role: 'reviewer' }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
    ).toHaveValue('')
  })

  it('refuses a duplicate role name before it is submitted', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)

    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      ROLE_AUDITOR.role,
    )
    expect(
      screen.getByText(zhCN.permissions.roles.create.duplicate),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: zhCN.permissions.roles.create.submit,
      }),
    ).toBeDisabled()
    expect(recorder.writes).toEqual([])
  })

  it('edits a role description in the side panel and sends the free text', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.editDescriptionAria, {
          role: ROLE_AUDITOR.role,
        }),
      }),
    )
    const panel = await screen.findByRole('dialog')
    expect(
      within(panel).getByText(zhCN.permissions.roles.description.title),
    ).toBeInTheDocument()
    // The role name is shown read-only: it is the key bindings hold.
    expect(within(panel).getByLabelText(zhCN.permissions.roles.description.roleLabel)).toHaveValue(
      ROLE_AUDITOR.role,
    )

    const field = within(panel).getByLabelText(
      zhCN.permissions.roles.description.descriptionLabel,
    )
    await user.clear(field)
    await user.type(field, 'Reviews every audit finding.')
    expect(
      within(panel).getByText(
        text(zhCN.permissions.roles.description.counter, {
          count: 28,
          max: 500,
        }),
      ),
    ).toBeInTheDocument()
    await user.click(
      within(panel).getByRole('button', {
        name: zhCN.permissions.roles.description.save,
      }),
    )

    await waitFor(() =>
      expect(recorder.writes).toEqual([
        {
          tenant: TENANT,
          role: ROLE_AUDITOR.role,
          description: 'Reviews every audit finding.',
        },
      ]),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(
      await screen.findByText(
        text(zhCN.permissions.roles.description.saved, {
          role: ROLE_AUDITOR.role,
        }),
      ),
    ).toBeInTheDocument()
  })

  it('deletes a role only after the two-step confirmation', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.removeAria, {
          role: ROLE_AUDITOR.role,
        }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.permissions.roles.removeConfirm,
      }),
    )
    expect(recorder.removes).toEqual([])
    await waitForArmedConfirm()
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitFor(() => expect(recorder.removes).toEqual([ROLE_AUDITOR.role]))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(
      await screen.findByText(
        text(zhCN.permissions.roles.removed, { role: ROLE_AUDITOR.role }),
      ),
    ).toBeInTheDocument()
  })

  it('keeps a builtin role read-only', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_BUILTIN.role)

    expect(
      screen.getByText(zhCN.permissions.roles.builtinLocked),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.roles.editDescriptionAria, {
          role: ROLE_BUILTIN.role,
        }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.roles.removeAria, {
          role: ROLE_BUILTIN.role,
        }),
      }),
    ).toBeNull()

    // The server's builtin mark comes with can_manage=true; the role still
    // opens read-only in the grants section.
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.viewGrantsAria, {
          role: ROLE_BUILTIN.role,
        }),
      }),
    )
    expect(
      await screen.findByText(zhCN.permissions.grants.builtinReadOnly),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    ).toBeNull()
    // The builtin role holds a grant; the read-only tier renders no revoke
    // control for it.
    const grantsSection = section(zhCN.permissions.grants.title)
    expect(
      await within(grantsSection).findByText('Read roles'),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.grants.revokeAria, {
          permission: 'authz.tenant.role.read',
        }),
      }),
    ).toBeNull()
  })

  it('keeps a host-protected role read-only', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantGrants: () =>
        Promise.resolve([
          grant('authz.tenant.role.read', 'tenant_role', 'read', ROLE_PROTECTED.role),
        ]),
    })
    renderRolesPage(backend, { protectedRoles: [ROLE_PROTECTED.role] })
    await screen.findByText(ROLE_PROTECTED.role)
    expect(
      screen.getByText(zhCN.permissions.roles.protectedReadOnly),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.viewGrantsAria, {
          role: ROLE_PROTECTED.role,
        }),
      }),
    )
    expect(
      await screen.findByText(zhCN.permissions.grants.protectedReadOnly),
    ).toBeInTheDocument()
    // The grant renders, its revoke control does not.
    const grantsSection = section(zhCN.permissions.grants.title)
    expect(await within(grantsSection).findByText('Read roles')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.grants.revokeAria, {
          permission: 'authz.tenant.role.read',
        }),
      }),
    ).toBeNull()
  })

  it('renders no write controls when the server says can_manage is false', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantRoles: () =>
        Promise.resolve([role({ role: 'viewer', can_manage: false })]),
      listTenantGrants: () =>
        Promise.resolve([
          grant('authz.tenant.role.read', 'tenant_role', 'read', 'viewer'),
        ]),
    })
    renderRolesPage(backend)
    await screen.findByText('viewer')

    expect(
      screen.queryByLabelText(zhCN.permissions.roles.create.roleLabel),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'viewer' }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.roles.editDescriptionAria, {
          role: 'viewer',
        }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.roles.removeAria, { role: 'viewer' }),
      }),
    ).toBeNull()
    expect(
      screen.getByText(zhCN.permissions.roles.noManageReadOnly),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.viewGrantsAria, { role: 'viewer' }),
      }),
    )
    expect(
      await screen.findByText(zhCN.permissions.grants.noManageReadOnly),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    ).toBeNull()
    // The role's grant renders; its revoke control does not.
    const grantsSection = section(zhCN.permissions.grants.title)
    expect(await within(grantsSection).findByText('Read roles')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.permissions.grants.revokeAria, {
          permission: 'authz.tenant.role.read',
        }),
      }),
    ).toBeNull()
  })
})

describe('the selected role grants', () => {
  it('grants a grantable permission and reports the outcome', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    expect(
      await screen.findByText(
        text(zhCN.permissions.grants.forRole, { role: ROLE_AUDITOR.role }),
      ),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    )
    await user.click(await screen.findByRole('option', { name: /Read roles/ }))
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.grants.add }),
    )

    await waitFor(() =>
      expect(recorder.grants).toEqual([
        {
          tenant: TENANT,
          role: ROLE_AUDITOR.role,
          permission: 'authz.tenant.role.read',
        },
      ]),
    )
    expect(
      await screen.findByText(
        text(zhCN.permissions.grants.granted, {
          permission: 'authz.tenant.role.read',
          role: ROLE_AUDITOR.role,
        }),
      ),
    ).toBeInTheDocument()
  })

  it('offers only grantable permissions in the grant picker', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: ROLE_AUDITOR.role }),
    )

    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    )
    const listbox = await screen.findByRole('listbox')
    expect(within(listbox).getByText('Read roles')).toBeInTheDocument()
    expect(within(listbox).queryByText('Read settings')).toBeNull()
    expect(within(listbox).queryByText('Legacy read')).toBeNull()
  })

  it('requires the two-step confirmation before granting a dangerous permission', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: ROLE_AUDITOR.role }),
    )

    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    )
    await user.click(await screen.findByRole('option', { name: /Manage roles/ }))
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.grants.add }),
    )

    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(
        text(zhCN.permissions.grants.dangerousMessage, {
          permission: 'authz.tenant.role.manage',
          role: ROLE_AUDITOR.role,
        }),
      ),
    ).toBeInTheDocument()
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.permissions.grants.dangerousConfirm,
      }),
    )
    expect(recorder.grants).toEqual([])
    await waitForArmedConfirm()
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitFor(() =>
      expect(recorder.grants).toEqual([
        {
          tenant: TENANT,
          role: ROLE_AUDITOR.role,
          permission: 'authz.tenant.role.manage',
        },
      ]),
    )
  })

  it('revokes by the server-reported permission, never by the resource/action pair', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    const grantsSection = section(zhCN.permissions.grants.title)

    // The readable name is primary; the code stays as secondary text.
    expect(await within(grantsSection).findByText('Read roles')).toBeInTheDocument()
    expect(
      within(grantsSection).getByText('authz.tenant.role.read'),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.grants.revokeAria, {
          permission: 'authz.tenant.role.read',
        }),
      }),
    )
    await waitFor(() =>
      expect(recorder.revokes).toEqual([
        {
          tenant: TENANT,
          role: ROLE_AUDITOR.role,
          permission: 'authz.tenant.role.read',
        },
      ]),
    )
    expect(
      await screen.findByText(
        text(zhCN.permissions.grants.revoked, {
          permission: 'authz.tenant.role.read',
          role: ROLE_AUDITOR.role,
        }),
      ),
    ).toBeInTheDocument()
  })

  it('keeps revoke unavailable for a grant the catalog cannot map', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText('tenant_role/write')

    const revoke = screen.getByRole('button', {
      name: text(zhCN.permissions.grants.revokeAria, {
        permission: 'tenant_role/write',
      }),
    })
    expect(revoke).toBeDisabled()
    expect(
      screen.getByText(zhCN.permissions.grants.revokeUnavailable),
    ).toBeInTheDocument()
    expect(recorder.revokes).toEqual([])
  })

  it('resolves a refused grant through the error-text whitelist', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      grantTenantPermission: () =>
        Promise.reject({ code: 'authz.forbidden' }),
    })
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: ROLE_AUDITOR.role }),
    )

    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    )
    await user.click(await screen.findByRole('option', { name: /Read roles/ }))
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.grants.add }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authz.forbidden)
  })
})

describe('the role usage section', () => {
  it('shows the active member count and the holders', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)

    expect(
      await screen.findByText(
        text(zhCN.permissions.usage.memberCount, { count: 2 }),
      ),
    ).toBeInTheDocument()
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.getByText('Bob Stone')).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.permissions.usage.status.suspended),
    ).toBeInTheDocument()
    // The read is the role-usage endpoint, one page of 50.
    expect(recorder.bindingReads[0]).toEqual({ limit: 50 })
  })

  it('says member state is unavailable when the server reports no count', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantRoles: () =>
        Promise.resolve([
          role({ member_count: null as unknown as number }),
        ]),
    })
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    expect(
      await screen.findByText(zhCN.permissions.usage.memberCountUnavailable),
    ).toBeInTheDocument()
    // The holder list is still rendered; the count is the only unknown.
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
  })

  it('renders an empty holder list as empty, not as a failed read', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantRoleBindings: () =>
        Promise.resolve({ bindings: [], next_cursor: '' }),
    })
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    expect(
      await screen.findByText(zhCN.permissions.usage.empty.title),
    ).toBeInTheDocument()
  })

  it('renders a failed holder read with a retry', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const { backend } = scripted({
      listTenantRoleBindings: () => {
        attempts += 1
        return Promise.reject({ code: 'client.http.500' })
      },
    })
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    expect(
      await screen.findByText(zhCN.permissions.usage.error.title),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.usage.retry }),
    )
    await waitFor(() => expect(attempts).toBeGreaterThan(1))
  })

  it('pages the holders by the server cursor', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText('Ada Lovelace')

    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    await waitFor(() =>
      expect(recorder.bindingReads).toContainEqual({
        limit: 50,
        after: 'acc-2',
      }),
    )
    expect(
      (await screen.findAllByText(zhCN.common.accountUnavailable)).length,
    ).toBeGreaterThan(0)
    expect(screen.queryByText('acc-3')).toBeNull()
    expect(
      screen.getByText(zhCN.permissions.usage.status.unavailable),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.previous }),
    )
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
  })

  it('links to the members page filtered to the role when the caller may manage memberships', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantRoles: () => Promise.resolve([role({ role: 'audit team' })]),
    })
    renderRolesPage(backend, { routes: [INVITATIONS_ROUTE] })
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, {
          role: 'audit team',
        }),
      }),
    )

    const link = await screen.findByRole('link', {
      name: zhCN.permissions.usage.viewInMembers,
    })
    expect(link).toHaveAttribute(
      'href',
      '/organization/admin/invitations?role=audit%20team',
    )
    // The entry is an addition, never a replacement for the holder list.
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
  })

  it('hides the members-page entry from a caller who may only read roles', async () => {
    attachTenantPermissions(['authz.tenant.role.read'])
    const user = userEvent.setup()
    const { backend } = scripted()
    renderRolesPage(backend, { routes: [INVITATIONS_ROUTE] })
    await selectRole(user, ROLE_AUDITOR.role)

    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(
      screen.queryByRole('link', {
        name: zhCN.permissions.usage.viewInMembers,
      }),
    ).toBeNull()
  })
})

describe('the permission catalog reference', () => {
  it('starts collapsed and keeps its region mounted', async () => {
    const { backend } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)
    const toggle = screen.getByRole('button', {
      name: zhCN.permissions.catalog.expand,
    })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // The section title is always discoverable; only its content is hidden.
    expect(
      screen.getByRole('heading', { name: zhCN.permissions.catalog.title }),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.permissions.catalog.search.label),
    ).not.toBeVisible()
  })

  it('expands into groups, marks dangerous and non-grantable entries, and searches', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)

    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.catalog.expand }),
    )
    expect(
      screen.getByRole('button', { name: zhCN.permissions.catalog.collapse }),
    ).toHaveAttribute('aria-expanded', 'true')

    // The groups render in the catalog's own order.
    const headings = screen
      .getAllByRole('heading', { level: 3 })
      .map((heading) => heading.textContent)
    const order = [
      zhCN.permissions.catalog.group.authorization,
      zhCN.permissions.catalog.group.membership,
      zhCN.permissions.catalog.group.settings,
      zhCN.permissions.catalog.group.other,
    ].map((label) => headings.indexOf(label))
    expect(order.every((index) => index >= 0)).toBe(true)
    expect([...order].sort((a, b) => a - b)).toEqual(order)

    expect(screen.getAllByText(zhCN.permissions.catalog.dangerous).length).toBeGreaterThan(0)
    expect(screen.getAllByText(zhCN.permissions.catalog.notGrantable).length).toBeGreaterThan(0)

    const catalogSection = section(zhCN.permissions.catalog.title)
    const search = within(catalogSection).getByLabelText(
      zhCN.permissions.catalog.search.label,
    )
    await user.type(search, 'settings.tenant.read')
    await waitFor(() =>
      expect(
        within(catalogSection).queryByRole('heading', {
          name: zhCN.permissions.catalog.group.authorization,
        }),
      ).toBeNull(),
    )
    expect(
      within(catalogSection).getByRole('heading', {
        name: zhCN.permissions.catalog.group.settings,
      }),
    ).toBeInTheDocument()
    expect(within(catalogSection).getByText('Read settings')).toBeInTheDocument()
    expect(within(catalogSection).queryByText('Read roles')).toBeNull()
  })

  it('collapses again from its own action', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.catalog.expand }),
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.catalog.collapse }),
    )
    expect(
      screen.getByLabelText(zhCN.permissions.catalog.search.label),
    ).not.toBeVisible()
  })
})

describe('the profile permission group', () => {
  it('groups the tenant profile permissions under profile, before other', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantPermissions: () => Promise.resolve(PROFILE_CATALOG),
    })
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.catalog.expand }),
    )

    const catalog = section(zhCN.permissions.catalog.title)
    // The shared order is unchanged and profile sits after the other tenant
    // groups but ahead of the fallback group.
    const headings = within(catalog)
      .getAllByRole('heading', { level: 3 })
      .map((heading) => heading.textContent)
    const order = [
      zhCN.permissions.catalog.group.authorization,
      zhCN.permissions.catalog.group.membership,
      zhCN.permissions.catalog.group.settings,
      zhCN.permissions.catalog.group.profile,
      zhCN.permissions.catalog.group.other,
    ].map((label) => headings.indexOf(label))
    expect(order.every((index) => index >= 0)).toBe(true)
    expect([...order].sort((a, b) => a - b)).toEqual(order)

    // Both profile permissions render under the profile heading, not other.
    const profileHeading = within(catalog)
      .queryAllByRole('heading')
      .find(
        (heading) =>
          heading.textContent === zhCN.permissions.catalog.group.profile,
      )
    expect(profileHeading).toBeDefined()
    const profileSection = profileHeading?.parentElement as HTMLElement
    expect(
      within(profileSection).getByText('tenant.profile.read'),
    ).toBeInTheDocument()
    expect(
      within(profileSection).getByText('tenant.profile.write'),
    ).toBeInTheDocument()
    expect(within(profileSection).queryByText('legacy.read')).toBeNull()

    // A permission naming a group the vocabulary does not carry still falls
    // into other.
    const otherHeading = within(catalog)
      .queryAllByRole('heading')
      .find(
        (heading) =>
          heading.textContent === zhCN.permissions.catalog.group.other,
      )
    expect(otherHeading).toBeDefined()
    const otherSection = otherHeading?.parentElement as HTMLElement
    expect(within(otherSection).getByText('legacy.read')).toBeInTheDocument()
    expect(
      within(otherSection).queryByText('tenant.profile.read'),
    ).toBeNull()
  })

  it('groups the grantable profile permissions under profile in the grant picker', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantPermissions: () => Promise.resolve(PROFILE_CATALOG),
    })
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: ROLE_AUDITOR.role }),
    )

    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    )
    const listbox = await screen.findByRole('listbox')
    const profileLabel = zhCN.permissions.catalog.group.profile
    expect(
      within(listbox).getByText(profileLabel),
    ).toBeInTheDocument()

    // An option's group is the nearest preceding ListSubheader. MUI clones
    // every child, subheaders included, with role="option", so the class is
    // the reliable boundary marker.
    const groupOf = (code: string): string => {
      const option = within(listbox).getByText(code).closest('[role="option"]')
      expect(option).not.toBeNull()
      for (
        let node = (option as HTMLElement).previousElementSibling;
        node !== null;
        node = node.previousElementSibling
      ) {
        if (node.classList.contains('MuiListSubheader-root')) {
          return node.textContent ?? ''
        }
      }
      return ''
    }
    expect(groupOf('tenant.profile.read')).toBe(profileLabel)
    expect(groupOf('tenant.profile.write')).toBe(profileLabel)
  })
})

describe('the page-level refresh and selection', () => {
  it('puts the refresh action in the title row and re-reads every body read', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText('Ada Lovelace')

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh.closest('header')).not.toBeNull()

    const before = {
      roles: recorder.roleReads,
      permissions: recorder.permissionReads,
      grants: recorder.grantReads,
      bindings: recorder.bindingReads.length,
    }
    await user.click(refresh)
    await waitFor(() => {
      expect(recorder.roleReads).toBeGreaterThan(before.roles)
      expect(recorder.permissionReads).toBeGreaterThan(before.permissions)
      expect(recorder.grantReads).toBeGreaterThan(before.grants)
      expect(recorder.bindingReads.length).toBeGreaterThan(before.bindings)
    })
  })

  it('clears the selection when a refresh no longer lists the selected role', async () => {
    const user = userEvent.setup()
    let reads = 0
    const { backend } = scripted({
      listTenantRoles: () => {
        reads += 1
        return Promise.resolve(reads === 1 ? [ROLE_AUDITOR] : [])
      },
    })
    renderRolesPage(backend)
    await selectRole(user, ROLE_AUDITOR.role)
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: ROLE_AUDITOR.role }),
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    expect(
      await screen.findByText(zhCN.permissions.grants.pickRole),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(zhCN.permissions.roles.selectionCleared),
    ).toBeInTheDocument()
  })

  it('reports a refused role write through the role scope', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      writeTenantRole: () => Promise.reject({ code: 'authz.conflict' }),
    })
    renderRolesPage(backend)
    await screen.findByText(ROLE_AUDITOR.role)

    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      'reviewer',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.permissions.roles.create.submit,
      }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authz.conflict)
    // The refused draft stays in the form for correction.
    expect(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
    ).toHaveValue('reviewer')
  })

  it('passes the page-tier axe scan in both color modes', async () => {
    const user = userEvent.setup()
    for (const defaultMode of ['light', 'dark'] as const) {
      const { backend } = scripted()
      const { unmount } = renderWithProviders(
        <OrganizationAdminProvider
          tenant={TENANT}
          operations={createAdminOperations(backend)}
          hasFeature={() => true}
          routes={[INVITATIONS_ROUTE]}
        >
          <OrganizationPermissionsPage />
        </OrganizationAdminProvider>,
        { defaultMode },
      )
      await screen.findByText(ROLE_AUDITOR.role)
      await selectRole(user, ROLE_AUDITOR.role)
      await screen.findByText('Ada Lovelace')
      await user.click(
        screen.getByRole('button', { name: zhCN.permissions.catalog.expand }),
      )
      await expectNoAxeViolations()
      unmount()
    }
  })
})
