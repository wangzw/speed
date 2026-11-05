/**
 * The organization administrator's members and invitations page.
 *
 * The cases pin the page's product shape: members first, invitations
 * second; a server-side member search behind a 300 ms debounce; cursor
 * paging on both lists; the member panel and its four write groups; the
 * three role-permission tiers and the escalation check before a bind; the
 * invitation status derivation, filter, resend/revoke/copy-link actions;
 * the one-time panel's close confirmation and three delivery outcomes; the
 * refresh action in the title row; and both color modes.
 *
 * The operations bundle is the scripted admin-ui backend, so the tests
 * observe the parameters the reads actually carry (`query`, `after`,
 * `status`) and the exact write inputs rather than stubbing the page's
 * hooks.
 */

import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachSession } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import { uiKitResources } from '@speed/ui-kit'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { OrganizationMembersPage } from './OrganizationMembersPage.js'
import { invitationStatusOf } from './internal/invitation-status.js'
import { SEARCH_DEBOUNCE_MS } from './internal/use-debounced-value.js'
import type {
  AdminInvitation,
  AdminIssuedInvitation,
  AdminMember,
  AdminOperations,
} from './operations.js'
import type {
  AdminTenantBinding,
  AdminTenantBindingRef,
  AdminTenantGrant,
  AdminTenantPermission,
  AdminTenantRole,
} from './organization-operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import type { AdminBackend } from '../test-utils/operations.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'
const SELF = 'acc-1'

const ADA: AdminMember = {
  account: 'acc-1',
  tenant: TENANT,
  active: true,
  display_name: 'Ada Lovelace',
  source: 'admin',
  sso_exempt: false,
  roles: ['tenant-operator'],
  joined_at: '2026-10-15T12:00:00Z',
}

const BOB: AdminMember = {
  account: 'acc-2',
  tenant: TENANT,
  active: false,
  display_name: 'Bob Stone',
  source: 'invite',
  sso_exempt: false,
  roles: [],
  joined_at: '2026-10-20T08:30:00Z',
}

const ROLE_OPERATOR: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-operator',
  builtin: false,
  description_key: 'role.tenantOperator',
  created_at: '2026-01-01T00:00:00Z',
}

const ROLE_SUPER: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-super',
  builtin: false,
  description_key: 'role.tenantSuper',
  created_at: '2026-01-01T00:00:00Z',
}

const PERMISSIONS: readonly AdminTenantPermission[] = [
  { permission: 'authz.tenant.role.read', description: '' },
  { permission: 'authz.tenant.role.manage', description: '' },
  { permission: 'authn.account.delete', description: '' },
]

const OPERATOR_BINDING: AdminTenantBinding = {
  tenant: TENANT,
  account: ADA.account,
  role: 'tenant-operator',
  granted_at: '2026-10-15T12:00:00Z',
}

const SUPER_GRANT: AdminTenantGrant = {
  tenant: TENANT,
  role: 'tenant-super',
  resource: 'account',
  action: 'delete',
  created_at: '2026-01-01T00:00:00Z',
}

/**
 * The six permissions pkg/authz/policy marks TenantGoverned, each one
 * translated to the (resource, action) pair the engine matches on. These are
 * the codes a tenant administrator holds and the pairs a role's grants carry,
 * so the panel's escalation check has to map one back to the other.
 */
const GOVERNED_PERMISSIONS: readonly AdminTenantPermission[] = [
  { permission: 'authz.tenant.role.read', description: '' },
  { permission: 'authz.tenant.role.manage', description: '' },
  { permission: 'authn.membership.manage', description: '' },
  { permission: 'authn.idp.manage', description: '' },
  { permission: 'settings.tenant.read', description: '' },
  { permission: 'settings.tenant.write', description: '' },
]

const ROLE_GOVERNED: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-admin',
  builtin: false,
  description_key: '',
  created_at: '2026-01-01T00:00:00Z',
}

const GOVERNED_GRANTS: readonly AdminTenantGrant[] = [
  { resource: 'tenant_role', action: 'read' },
  { resource: 'tenant_role', action: 'manage' },
  { resource: 'membership', action: 'manage' },
  { resource: 'idp', action: 'manage' },
  { resource: 'tenant_settings', action: 'read' },
  { resource: 'tenant_settings', action: 'write' },
].map((grant) => ({
  tenant: TENANT,
  role: ROLE_GOVERNED.role,
  created_at: '2026-01-01T00:00:00Z',
  ...grant,
}))

const PENDING_INVITATION: AdminInvitation = {
  id: 'inv-pending',
  tenant: TENANT,
  email: 'pending@example.com',
  invited_by: ADA.account,
  status: 'pending',
  created_at: '2026-11-01T10:00:00Z',
  expires_at: '2099-01-01T00:00:00Z',
  accepted_at: '0001-01-01T00:00:00Z',
}

const EXPIRED_INVITATION: AdminInvitation = {
  ...PENDING_INVITATION,
  id: 'inv-expired',
  email: 'expired@example.com',
  created_at: '2026-01-01T10:00:00Z',
  expires_at: '2026-02-01T00:00:00Z',
}

const ACCEPTED_INVITATION: AdminInvitation = {
  ...PENDING_INVITATION,
  id: 'inv-accepted',
  email: 'accepted@example.com',
  status: 'accepted',
  accepted_at: '2026-11-02T10:00:00Z',
}

const REVOKED_INVITATION: AdminInvitation = {
  ...PENDING_INVITATION,
  id: 'inv-revoked',
  email: 'revoked@example.com',
  status: 'revoked',
}

const ALL_INVITATIONS = [
  PENDING_INVITATION,
  EXPIRED_INVITATION,
  ACCEPTED_INVITATION,
  REVOKED_INVITATION,
]

const FULL_TENANT_PERMISSIONS = [
  'authz.tenant.role.read',
  'authz.tenant.role.manage',
]

const uiKitZh = uiKitResources['zh-CN'] as unknown as {
  readonly confirmDialog: {
    readonly confirmAgainLabel: string
  }
}

function attachTenantPermissions(
  tenant: readonly string[] | null,
  accountId: string = SELF,
): void {
  const principal = AUTHENTICATED_SNAPSHOT.principal
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      principal:
        principal === null ? null : { ...principal, account_id: accountId },
      permissionSets: { tenant, system: null },
    }),
  )
}

interface Recorder {
  readonly memberParams: Array<
    Parameters<AdminOperations['useMembers']>[1] | undefined
  >
  readonly invitationParams: Array<
    Parameters<AdminOperations['useInvitations']>[1] | undefined
  >
  readonly binds: AdminTenantBindingRef[]
  readonly unbinds: AdminTenantBindingRef[]
  readonly writes: Array<{ tenant: string; account: string; active?: boolean }>
  readonly removes: Array<{ tenant: string; account: string }>
  readonly issues: Array<{ tenant: string; email: string }>
  readonly revokes: Array<{ tenant: string; id: string }>
  readonly accountBindingReads: string[]
  readonly roleReads: string[]
  readonly grantReads: string[]
}

interface Scripted {
  readonly backend: AdminBackend
  readonly recorder: Recorder
}

/** The scripted backend: successful scripted reads, recorded writes. */
function scripted(overrides: Partial<AdminBackend> = {}): Scripted {
  const recorder: Recorder = {
    memberParams: [],
    invitationParams: [],
    binds: [],
    unbinds: [],
    writes: [],
    removes: [],
    issues: [],
    revokes: [],
    accountBindingReads: [],
    roleReads: [],
    grantReads: [],
  }
  const backend = makeAdminBackend({
    listMembers: (_tenant, params) => {
      recorder.memberParams.push(params)
      return Promise.resolve({ members: [ADA, BOB], next_cursor: '' })
    },
    listInvitations: (_tenant, params) => {
      recorder.invitationParams.push(params)
      const status = params?.status
      return Promise.resolve({
        invitations:
          status === undefined
            ? ALL_INVITATIONS
            : ALL_INVITATIONS.filter(
                (invitation) => invitationStatusOf(invitation) === status,
              ),
        next_cursor: '',
      })
    },
    listTenantRoles: (tenant) => {
      recorder.roleReads.push(tenant)
      return Promise.resolve([ROLE_OPERATOR, ROLE_SUPER])
    },
    listTenantPermissions: () => Promise.resolve(PERMISSIONS),
    listTenantGrants: (tenant, role) => {
      recorder.grantReads.push(role)
      return Promise.resolve(role === ROLE_SUPER.role ? [SUPER_GRANT] : [])
    },
    listTenantAccountBindings: (_tenant, account) => {
      recorder.accountBindingReads.push(account)
      return Promise.resolve([OPERATOR_BINDING])
    },
    bindTenantRole: (input) => {
      recorder.binds.push(input)
      return Promise.resolve()
    },
    unbindTenantRole: (input) => {
      recorder.unbinds.push(input)
      return Promise.resolve()
    },
    writeMember: (input) => {
      recorder.writes.push(input)
      return Promise.resolve()
    },
    removeMember: (input) => {
      recorder.removes.push(input)
      return Promise.resolve()
    },
    issueInvitation: (input) => {
      recorder.issues.push(input)
      return Promise.resolve(issuedFixture(input.email))
    },
    revokeInvitation: (input) => {
      recorder.revokes.push(input)
      return Promise.resolve()
    },
    ...overrides,
  })
  return { backend, recorder }
}

/** The issue answer for `email`, carrying a fresh pending invitation. */
function issuedFixture(
  email: string,
  delivery: AdminIssuedInvitation['email_delivery'] = 'sent',
): AdminIssuedInvitation {
  return {
    email_delivery: delivery,
    invitation: {
      ...PENDING_INVITATION,
      id: 'inv-new',
      email,
      created_at: '2026-11-05T12:00:00Z',
      expires_at: '2099-01-01T00:00:00Z',
    },
    token: 'spd_inv_new_token',
  }
}

function renderPage(
  backend: AdminBackend,
  options: {
    readonly language?: string
    readonly defaultMode?: 'light' | 'dark' | 'system'
  } = {},
) {
  return renderWithProviders(
    <OrganizationAdminProvider
      tenant={TENANT}
      operations={createAdminOperations(backend)}
      hasFeature={() => true}
      breadcrumbs={[
        { label: 'Acme' },
        { label: zhCN.invitations.title },
      ]}
      invitationLink={(token) => `/invitations/${token}`}
    >
      <OrganizationMembersPage tenant={TENANT} />
    </OrganizationAdminProvider>,
    options,
  )
}

function required(text: string): RegExp {
  return new RegExp(text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
}

function rowOf(target: string | HTMLElement): HTMLElement {
  const cell =
    typeof target === 'string' ? screen.getByText(target) : target
  const row = cell.closest('tr')
  if (row === null) {
    throw new Error(`no row for ${String(target)}`)
  }
  return row
}

function manageButton(name: string): HTMLElement {
  return screen.getByRole('button', {
    name: zhCN.members.manageAria.replace('{{name}}', name),
  })
}

async function openPanel(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<HTMLElement> {
  await screen.findByText(name)
  await user.click(manageButton(name))
  return screen.getByRole('dialog', { name: zhCN.members.panel.title })
}

beforeEach(() => {
  // Not one of the fixture members: the self-only cases attach their own
  // session with the member's account id.
  attachTenantPermissions(FULL_TENANT_PERMISSIONS, 'acc-admin')
})

afterEach(() => {
  vi.useRealTimers()
})

describe('the page structure', () => {
  it('renders members first and invitations second, with the tables settled', async () => {
    const { backend } = scripted()
    renderPage(backend)

    expect(await screen.findByText(ADA.display_name)).toBeInTheDocument()
    const headings = screen
      .getAllByRole('heading', { level: 2 })
      .map((heading) => heading.textContent)
    expect(headings.indexOf(zhCN.members.title)).toBeGreaterThanOrEqual(0)
    expect(headings.indexOf(zhCN.members.title)).toBeLessThan(
      headings.indexOf(zhCN.invitations.section.title),
    )
  })

  it('shows the role chips and the joined-at column', async () => {
    const { backend } = scripted()
    renderPage(backend)

    const row = rowOf(await screen.findByText(ADA.display_name))
    expect(
      within(row).getByText('tenant-operator'),
    ).toBeInTheDocument()
    expect(
      within(row).getByText(
        formatInstant(ADA.joined_at, 'zh-CN', { timeZone: 'UTC' }),
      ),
    ).toBeInTheDocument()
    // The two columns are ordinary row information, not fine-print detail.
    expect(
      screen.getByRole('columnheader', { name: zhCN.members.column.roles }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', { name: zhCN.members.column.joinedAt }),
    ).toBeInTheDocument()
  })

  it('keeps the refresh action in the title row', async () => {
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findByText(ADA.display_name)
    const before = recorder.memberParams.length
    const header = screen.getByRole('banner')
    const refresh = within(header).getByRole('button', {
      name: zhCN.common.refresh,
    })
    await userEvent.setup().click(refresh)
    await waitFor(() =>
      expect(recorder.memberParams.length).toBeGreaterThan(before),
    )
  })

  it.each(['light', 'dark'] as const)(
    'renders in the %s color mode',
    async (defaultMode) => {
      const { backend } = scripted()
      renderPage(backend, { defaultMode })
      expect(await screen.findByText(ADA.display_name)).toBeInTheDocument()
      expect(
        screen.getByRole('heading', { name: zhCN.members.title }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('heading', { name: zhCN.invitations.section.title }),
      ).toBeInTheDocument()
    },
  )

  it('has no axe violations', async () => {
    const { backend } = scripted()
    renderPage(backend)
    await screen.findByText(ADA.display_name)
    await expectNoAxeViolations()
  })
})

describe('the member search and paging', () => {
  it('debounces the search and sends it as the server query', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({
      advanceTimers: vi.advanceTimersByTimeAsync,
    })
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findByText(ADA.display_name)

    const input = screen.getByLabelText(zhCN.members.search.label)
    await user.type(input, 'ada')
    // The debounce has not settled: no read carries the query yet.
    expect(
      recorder.memberParams.filter((params) => params?.query !== undefined),
    ).toHaveLength(0)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SEARCH_DEBOUNCE_MS)
    })
    vi.useRealTimers()
    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({
        limit: 100,
        query: 'ada',
      }),
    )
  })

  it('pages the member list by the server cursor', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listMembers: (_tenant, params) => {
        recorder.memberParams.push(params)
        return Promise.resolve(
          params?.after === 'acc-1'
            ? { members: [BOB], next_cursor: '' }
            : { members: [ADA], next_cursor: 'acc-1' },
        )
      },
    })
    renderPage(backend)
    await screen.findByText(ADA.display_name)
    expect(screen.queryByText(BOB.display_name)).toBeNull()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    expect(await screen.findByText(BOB.display_name)).toBeInTheDocument()
    expect(recorder.memberParams).toContainEqual({
      limit: 100,
      after: 'acc-1',
    })
    expect(
      screen.getByText(zhCN.common.pager.page.replace('{{page}}', '2')),
    ).toBeInTheDocument()
  })

  it('starts the new tenant at the first page without carrying the old cursor', async () => {
    const user = userEvent.setup()
    const calls: Array<{ tenant: string; params: unknown }> = []
    const { backend } = scripted({
      listMembers: (tenant, params) => {
        calls.push({ tenant, params })
        if (tenant !== TENANT) {
          return Promise.resolve({ members: [ADA], next_cursor: '' })
        }
        return Promise.resolve(
          params?.after === 'acc-1'
            ? { members: [BOB], next_cursor: '' }
            : { members: [ADA], next_cursor: 'acc-1' },
        )
      },
    })
    function SwitchTenant() {
      const [tenant, setTenant] = useState(TENANT)
      return (
        <OrganizationAdminProvider
          tenant={tenant}
          operations={createAdminOperations(backend)}
          hasFeature={() => true}
        >
          <button type="button" onClick={() => setTenant('globex')}>
            switch
          </button>
          <OrganizationMembersPage tenant={tenant} />
        </OrganizationAdminProvider>
      )
    }
    renderWithProviders(<SwitchTenant />)

    await screen.findByText(ADA.display_name)
    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    expect(await screen.findByText(BOB.display_name)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'switch' }))
    await waitFor(() =>
      expect(calls.some((call) => call.tenant === 'globex')).toBe(true),
    )
    // The first read the new tenant issues carries no cursor from the page
    // the operator was on under the tenant they left.
    expect(
      calls
        .filter((call) => call.tenant === 'globex')
        .every((call) => (call.params as { after?: string }).after === undefined),
    ).toBe(true)
  })
})

describe('the member management panel', () => {
  it('binds and unbinds a role', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)

    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(
      screen.getByRole('option', { name: ROLE_OPERATOR.role }),
    )
    await waitFor(() =>
      expect(
        within(panel).queryByText(zhCN.members.panel.escalationChecking),
      ).toBeNull(),
    )
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toBeEnabled()
    await user.click(bind)
    await waitFor(() =>
      expect(recorder.binds).toEqual([
        {
          tenant: TENANT,
          account: ADA.account,
          role: ROLE_OPERATOR.role,
        },
      ]),
    )
    expect(
      await within(panel).findByText(
        zhCN.members.panel.bindSaved
          .replace('{{account}}', ADA.display_name)
          .replace('{{role}}', ROLE_OPERATOR.role),
      ),
    ).toBeInTheDocument()

    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.panel.unbind }),
    )
    await waitFor(() =>
      expect(recorder.unbinds).toEqual([
        {
          tenant: TENANT,
          account: ADA.account,
          role: ROLE_OPERATOR.role,
        },
      ]),
    )
  })

  it('suspends an active member and resumes a suspended one', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)

    const activePanel = await openPanel(user, ADA.display_name)
    await user.click(
      within(activePanel).getByRole('button', {
        name: zhCN.members.panel.suspend,
      }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.writes).toContainEqual({
        tenant: TENANT,
        account: ADA.account,
        active: false,
      }),
    )
    await user.click(
      within(activePanel).getByRole('button', {
        name: zhCN.members.panel.closeAria.replace(
          '{{name}}',
          ADA.display_name,
        ),
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: zhCN.members.panel.title }),
      ).toBeNull(),
    )

    const suspendedPanel = await openPanel(user, BOB.display_name)
    await user.click(
      within(suspendedPanel).getByRole('button', {
        name: zhCN.members.panel.resume,
      }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.writes).toContainEqual({
        tenant: TENANT,
        account: BOB.account,
        active: true,
      }),
    )
  })

  it('updates the open panel after the member is suspended', async () => {
    const user = userEvent.setup()
    let adaActive = true
    const { backend, recorder } = scripted({
      listMembers: () =>
        Promise.resolve({
          members: [{ ...ADA, active: adaActive }, BOB],
          next_cursor: '',
        }),
      writeMember: (input) => {
        recorder.writes.push(input)
        if (input.account === ADA.account) {
          adaActive = input.active ?? adaActive
        }
        return Promise.resolve()
      },
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)

    await user.click(
      within(panel).getByRole('button', {
        name: zhCN.members.panel.suspend,
      }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.writes).toContainEqual({
        tenant: TENANT,
        account: ADA.account,
        active: false,
      }),
    )

    // The panel stays open, and the list read it derives from now carries
    // the suspended state: the same panel offers resume, shows the
    // suspended chip and refuses binding without a reopen.
    expect(
      await within(panel).findByRole('button', {
        name: zhCN.members.panel.resume,
      }),
    ).toBeInTheDocument()
    expect(
      within(panel).getByText(zhCN.members.status.suspended),
    ).toBeInTheDocument()
    expect(
      within(panel).getByText(zhCN.members.panel.suspendedBindNote),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).toBeDisabled()
  })

  it('disables suspending yourself and says why', async () => {
    attachTenantPermissions(FULL_TENANT_PERMISSIONS, SELF)
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)
    expect(
      within(panel).getByRole('button', {
        name: zhCN.members.panel.suspend,
      }),
    ).toBeDisabled()
    expect(
      within(panel).getByText(zhCN.members.panel.cannotSuspendSelf),
    ).toBeInTheDocument()
  })

  it('removes a member behind the double confirmation', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, BOB.display_name)

    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.remove }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    // The first click only arms the guard.
    expect(recorder.removes).toHaveLength(0)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 700))
    })
    await user.click(
      screen.getByRole('button', {
        name: uiKitZh.confirmDialog.confirmAgainLabel,
      }),
    )
    await waitFor(() =>
      expect(recorder.removes).toEqual([
        { tenant: TENANT, account: BOB.account },
      ]),
    )
    // The success is written into the list's scope after the panel exits.
    expect(
      await screen.findByText(
        zhCN.members.removed.replace('{{account}}', BOB.account),
        undefined,
        { timeout: 3000 },
      ),
    ).toBeInTheDocument()
  })

  it('adds the last-admin page text when the removal answers a conflict', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      removeMember: () => Promise.reject({ code: 'authn.conflict' }),
    })
    renderPage(backend)
    const panel = await openPanel(user, BOB.display_name)
    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.remove }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 700))
    })
    await user.click(
      screen.getByRole('button', {
        name: uiKitZh.confirmDialog.confirmAgainLabel,
      }),
    )
    expect(
      await within(panel).findByText(zhCN.members.lastAdminConflict),
    ).toBeInTheDocument()
  })

  it('warns that removing yourself can be refused by the last-admin guard', async () => {
    attachTenantPermissions(FULL_TENANT_PERMISSIONS, SELF)
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)
    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.remove }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.members.removeTitle,
    })
    expect(
      within(dialog).getByText(
        required(zhCN.members.removeSelfNote),
      ),
    ).toBeInTheDocument()
  })
})

describe('the role permission tiers', () => {
  it('without role.read says so and reads no bindings', async () => {
    attachTenantPermissions(null)
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)

    expect(
      within(panel).getByText(zhCN.members.panel.rolesNoRead),
    ).toBeInTheDocument()
    expect(
      within(panel).queryByRole('combobox', {
        name: zhCN.members.panel.roleLabel,
      }),
    ).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.members.panel.bind,
      }),
    ).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.members.panel.unbind,
      }),
    ).toBeNull()
    expect(recorder.accountBindingReads).toEqual([])
    expect(recorder.roleReads).toEqual([])
  })

  it('with role.read but not role.manage keeps the list read-only', async () => {
    attachTenantPermissions(['authz.tenant.role.read'])
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)

    expect(
      await within(panel).findByText(ROLE_OPERATOR.role),
    ).toBeInTheDocument()
    expect(
      within(panel).getByText(zhCN.members.panel.rolesReadOnly),
    ).toBeInTheDocument()
    expect(
      within(panel).queryByRole('combobox', {
        name: zhCN.members.panel.roleLabel,
      }),
    ).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.members.panel.bind,
      }),
    ).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.members.panel.unbind,
      }),
    ).toBeNull()
    expect(recorder.accountBindingReads).toEqual([ADA.account])
    expect(recorder.roleReads).toEqual([])
  })

  it('with role.manage renders the complete control set', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)

    expect(
      within(panel).getByRole('combobox', {
        name: zhCN.members.panel.roleLabel,
      }),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.unbind }),
    ).toBeInTheDocument()
  })
})

describe('the bind escalation check', () => {
  it('blocks a role whose grants exceed the caller and sends no request', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_SUPER.role }))

    expect(
      await within(panel).findByText(zhCN.members.panel.escalationBlocked),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).toBeDisabled()
    expect(recorder.binds).toEqual([])
  })

  it('allows binding tenant-admin when the caller holds every governed grant', async () => {
    attachTenantPermissions(
      GOVERNED_PERMISSIONS.map((entry) => entry.permission),
    )
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantRoles: () => Promise.resolve([ROLE_GOVERNED]),
      listTenantPermissions: () => Promise.resolve(GOVERNED_PERMISSIONS),
      listTenantGrants: () => Promise.resolve(GOVERNED_GRANTS),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(
      screen.getByRole('option', { name: ROLE_GOVERNED.role }),
    )
    await waitFor(() =>
      expect(
        within(panel).queryByText(zhCN.members.panel.escalationChecking),
      ).toBeNull(),
    )

    // Every grant maps back to a governed code the caller holds, so the
    // server's own tenant-admin path -- allowed, subject to the subset --
    // stays reachable instead of being refused by the client.
    expect(
      within(panel).queryByText(zhCN.members.panel.escalationBlocked),
    ).toBeNull()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).toBeEnabled()
  })

  it('does not default to allowing when the grant read fails', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantGrants: () => Promise.reject(new Error('grants unavailable')),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA.display_name)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))

    expect(
      await within(panel).findByText(zhCN.members.panel.escalationFailed),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).toBeDisabled()
    expect(
      within(panel).getByRole('button', {
        name: zhCN.members.panel.escalationRetry,
      }),
    ).toBeInTheDocument()
    expect(recorder.binds).toEqual([])
  })

  it('disables binding a suspended member while keeping unbind available', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, BOB.display_name)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))
    await waitFor(() =>
      expect(
        within(panel).queryByText(zhCN.members.panel.escalationChecking),
      ).toBeNull(),
    )
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).toBeDisabled()
    expect(
      within(panel).getByText(zhCN.members.panel.suspendedBindNote),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.unbind }),
    ).toBeEnabled()
  })
})

describe('the invitation section', () => {
  it('derives the four statuses and filters the server read', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)

    const pendingRow = rowOf(await screen.findByText(PENDING_INVITATION.email))
    expect(
      within(pendingRow).getByText(zhCN.invitations.status.pending),
    ).toBeInTheDocument()
    const expiredRow = rowOf(screen.getByText(EXPIRED_INVITATION.email))
    expect(
      within(expiredRow).getByText(zhCN.invitations.status.expired),
    ).toBeInTheDocument()
    const acceptedRow = rowOf(screen.getByText(ACCEPTED_INVITATION.email))
    expect(
      within(acceptedRow).getByText(zhCN.invitations.status.accepted),
    ).toBeInTheDocument()
    const revokedRow = rowOf(screen.getByText(REVOKED_INVITATION.email))
    expect(
      within(revokedRow).getByText(zhCN.invitations.status.revoked),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('combobox', { name: zhCN.invitations.filter.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.invitations.status.expired }),
    )
    await waitFor(() =>
      expect(recorder.invitationParams).toContainEqual({
        limit: 50,
        status: 'expired',
      }),
    )
    expect(
      await screen.findByText(EXPIRED_INVITATION.email),
    ).toBeInTheDocument()
    expect(screen.queryByText(PENDING_INVITATION.email)).toBeNull()
  })

  it('resends through the issue route and exposes the new one-time link', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      issueInvitation: (input) => {
        recorder.issues.push(input)
        return Promise.resolve({
          ...issuedFixture(input.email),
          invitation: {
            ...issuedFixture(input.email).invitation,
            id: PENDING_INVITATION.id,
          },
        })
      },
    })
    renderPage(backend)
    const row = rowOf(await screen.findByText(PENDING_INVITATION.email))
    await user.click(
      within(row).getByRole('button', { name: zhCN.invitations.resend }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() => {
      expect(recorder.issues).toHaveLength(1)
      expect(recorder.issues[0]).toMatchObject({
        tenant: TENANT,
        email: PENDING_INVITATION.email,
      })
    })
    // The new token is shown once, and the row it belongs to now offers
    // the copy action while the other rows do not.
    expect(
      await screen.findByText(
        `${window.location.origin}/invitations/spd_inv_new_token`,
      ),
    ).toBeInTheDocument()
    expect(
      within(rowOf(PENDING_INVITATION.email)).getByRole('button', {
        name: zhCN.invitations.copyLink,
      }),
    ).toBeInTheDocument()
    expect(
      within(rowOf(ACCEPTED_INVITATION.email)).queryByRole('button', {
        name: zhCN.invitations.copyLink,
      }),
    ).toBeNull()
  })

  it('revokes a pending invitation', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const row = rowOf(await screen.findByText(PENDING_INVITATION.email))
    await user.click(
      within(row).getByRole('button', { name: zhCN.invitations.revoke }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.revokes).toEqual([
        { tenant: TENANT, id: PENDING_INVITATION.id },
      ]),
    )
  })

  it('asks before closing the one-time panel and only then clears it', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    await screen.findByText(ADA.display_name)

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    const link = await screen.findByText(
      `${window.location.origin}/invitations/spd_inv_new_token`,
    )
    expect(link).toBeInTheDocument()

    const closeAria = zhCN.invitations.issued.closeAria
      .replace('{{tenant}}', TENANT)
      .replace('{{email}}', 'new@example.com')
    await user.click(screen.getByRole('button', { name: closeAria }))
    const closeDialog = await screen.findByRole('dialog', {
      name: zhCN.invitations.issued.closeTitle,
    })
    await user.click(
      within(closeDialog).getByRole('button', {
        name: zhCN.invitations.issued.keepOpen,
      }),
    )
    // Cancelling keeps the only copy.
    expect(
      screen.getByText(`${window.location.origin}/invitations/spd_inv_new_token`),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: closeAria }))
    const confirmDialog = await screen.findByRole('dialog', {
      name: zhCN.invitations.issued.closeTitle,
    })
    await user.click(
      within(confirmDialog).getByRole('button', {
        name: zhCN.invitations.issued.close,
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByText(
          `${window.location.origin}/invitations/spd_inv_new_token`,
        ),
      ).toBeNull(),
    )
  })

  it.each(['sent', 'failed', 'not_configured'] as const)(
    'renders the %s delivery outcome',
    async (delivery) => {
      const user = userEvent.setup()
      const { backend } = scripted({
        issueInvitation: (input) =>
          Promise.resolve(issuedFixture(input.email, delivery)),
      })
      renderPage(backend)
      await screen.findByText(ADA.display_name)
      await user.type(
        screen.getByLabelText(zhCN.invitations.issue.emailLabel),
        'delivery@example.com',
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
      )
      expect(
        await screen.findByText(
          zhCN.invitations.issued.delivery[delivery],
        ),
      ).toBeInTheDocument()
    },
  )

  it('pages the invitation list by the server cursor', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listInvitations: (_tenant, params) => {
        recorder.invitationParams.push(params)
        return Promise.resolve(
          params?.after === 'inv-pending'
            ? { invitations: [ACCEPTED_INVITATION], next_cursor: '' }
            : { invitations: [PENDING_INVITATION], next_cursor: 'inv-pending' },
        )
      },
    })
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)
    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    expect(
      await screen.findByText(ACCEPTED_INVITATION.email),
    ).toBeInTheDocument()
    expect(recorder.invitationParams).toContainEqual({
      limit: 50,
      after: 'inv-pending',
    })
  })
})
