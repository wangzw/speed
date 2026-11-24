
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachSession } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import { uiKitResources } from '@speed/ui-kit'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { organizationAdminOperations } from './organization-operations.js'
import { organizationAdminRoutes } from './organization-routes.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import { OrganizationMembersPage } from './OrganizationMembersPage.js'
import { SEARCH_DEBOUNCE_MS } from './internal/use-debounced-value.js'
import type {
  AdminInvitation,
  AdminIssuedInvitation,
  AdminMember,
  AdminMemberPage,
  AdminOperations,
} from './operations.js'
import type {
  AdminTenantBinding,
  AdminTenantBindingRef,
  AdminTenantGrant,
  AdminTenantPermission,
  AdminTenantRole,
} from './organization-operations.js'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
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
import { cellOf } from '../test-utils/table.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

function fill(
  template: string,
  values: Readonly<Record<string, string>>,
): string {
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replace(`{{${name}}}`, value),
    template,
  )
}

const TENANT = 'acme'
const SELF = 'acc-1'

const ADA: AdminMember = {
  account: 'acc-1',
  tenant: TENANT,
  active: true,
  display_name: 'Ada Lovelace',
  username: 'ada',
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
  username: 'bob',
  source: 'invite',
  sso_exempt: false,
  roles: [],
  joined_at: '2026-10-20T08:30:00Z',
}

const CAROL: AdminMember = {
  account: 'acc-3',
  tenant: TENANT,
  active: true,
  display_name: 'Carol Reed',
  username: 'carol',
  source: 'admin',
  sso_exempt: false,
  roles: [],
  joined_at: '2026-10-25T09:00:00Z',
}

const ROLE_OPERATOR: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-operator',
  builtin: false,
  description: '',
  permission_count: 0,
  member_count: null,
  can_manage: false,
  created_at: '2026-01-01T00:00:00Z',
}

const ROLE_SUPER: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-super',
  builtin: false,
  description: '',
  permission_count: 0,
  member_count: null,
  can_manage: false,
  created_at: '2026-01-01T00:00:00Z',
}

function tenantPermission(value: string): AdminTenantPermission {
  return {
    permission: value,
    description: '',
    dangerous: false,
    grantable: true,
    group: '',
  }
}

const PERMISSIONS: readonly AdminTenantPermission[] = [
  tenantPermission('authz.tenant.role.read'),
  tenantPermission('authz.tenant.role.manage'),
  tenantPermission('authn.account.delete'),
]

const OPERATOR_BINDING: AdminTenantBinding = {
  tenant: TENANT,
  account: ADA.account,
  role: 'tenant-operator',
  granted_at: '2026-10-15T12:00:00Z',
}

const SUPER_BINDING: AdminTenantBinding = {
  tenant: TENANT,
  account: ADA.account,
  role: ROLE_SUPER.role,
  granted_at: '2026-10-15T12:00:00Z',
}

const TENANT_ADMIN_ROLE = 'tenant-admin'

const SUPER_GRANT: AdminTenantGrant = {
  tenant: TENANT,
  role: 'tenant-super',
  permission: 'authn.account.delete',
  resource: 'account',
  action: 'delete',
  created_at: '2026-01-01T00:00:00Z',
}

const GOVERNED_PERMISSIONS: readonly AdminTenantPermission[] = [
  tenantPermission('authz.tenant.role.read'),
  tenantPermission('authz.tenant.role.manage'),
  tenantPermission('authn.membership.manage'),
  tenantPermission('authn.idp.manage'),
  tenantPermission('settings.tenant.read'),
  tenantPermission('settings.tenant.write'),
]

const ROLE_GOVERNED: AdminTenantRole = {
  tenant: TENANT,
  role: 'tenant-admin',
  builtin: false,
  description: '',
  permission_count: 0,
  member_count: null,
  can_manage: false,
  created_at: '2026-01-01T00:00:00Z',
}

const GOVERNED_GRANTS: readonly AdminTenantGrant[] = [
  { permission: 'authz.tenant.role.read', resource: 'tenant_role', action: 'read' },
  { permission: 'authz.tenant.role.manage', resource: 'tenant_role', action: 'manage' },
  { permission: 'authn.membership.manage', resource: 'membership', action: 'manage' },
  { permission: 'authn.idp.manage', resource: 'idp', action: 'manage' },
  { permission: 'settings.tenant.read', resource: 'tenant_settings', action: 'read' },
  { permission: 'settings.tenant.write', resource: 'tenant_settings', action: 'write' },
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
  invited_by_name: ADA.display_name,
  invited_by_username: ADA.username,
  status: 'pending',
  state: 'pending',
  role: ROLE_OPERATOR.role,
  created_at: '2026-11-01T10:00:00Z',
  expires_at: '2099-01-01T00:00:00Z',
  accepted_at: null,
  revoked_at: null,
  last_sent_at: '2026-11-01T10:00:00Z',
  send_count: 1,
}

const EXPIRED_INVITATION: AdminInvitation = {
  ...PENDING_INVITATION,
  id: 'inv-expired',
  email: 'expired@example.com',
  state: 'expired',
  role: undefined,
  created_at: '2026-01-01T10:00:00Z',
  expires_at: '2026-02-01T00:00:00Z',
}

const ACCEPTED_INVITATION: AdminInvitation = {
  ...PENDING_INVITATION,
  id: 'inv-accepted',
  email: 'accepted@example.com',
  status: 'accepted',
  state: 'accepted',
  role: undefined,
  expires_at: '2026-11-09T10:00:00Z',
  accepted_at: '2026-11-02T10:00:00Z',
}

const REVOKED_INVITATION: AdminInvitation = {
  ...PENDING_INVITATION,
  id: 'inv-revoked',
  email: 'revoked@example.com',
  status: 'revoked',
  state: 'revoked',
  role: undefined,
  expires_at: '2026-11-09T10:00:00Z',
  revoked_at: '2026-11-03T10:00:00Z',
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
    readonly title: string
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
  readonly issues: Array<{ tenant: string; email: string; role?: string }>
  readonly resends: Array<{ tenant: string; id: string }>
  readonly revokes: Array<{ tenant: string; id: string }>
  readonly accountBindingReads: string[]
  readonly roleReads: string[]
  readonly grantReads: string[]
}

interface Scripted {
  readonly backend: AdminBackend
  readonly recorder: Recorder
}

function scripted(overrides: Partial<AdminBackend> = {}): Scripted {
  const recorder: Recorder = {
    memberParams: [],
    invitationParams: [],
    binds: [],
    unbinds: [],
    writes: [],
    removes: [],
    issues: [],
    resends: [],
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
                (invitation) => invitation.state === status,
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
    resendInvitation: (input) => {
      recorder.resends.push(input)
      return Promise.resolve(issuedFixture(PENDING_INVITATION.email))
    },
    ...overrides,
  })
  return { backend, recorder }
}

function issuedFixture(
  email: string,
  delivery: AdminIssuedInvitation['email_delivery'] = 'not_configured',
): AdminIssuedInvitation {
  const answer: AdminIssuedInvitation = {
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
  if (delivery === 'sent') {
    delete answer.token
  }
  return answer
}

function renderPage(
  backend: AdminBackend,
  options: {
    readonly language?: string
    readonly defaultMode?: 'light' | 'dark' | 'system'
    readonly timeZone?: string
    readonly session?: ReturnType<typeof createStepUpSession>
  } = {},
) {
  return renderWithProviders(
    <OrganizationAdminProvider
      tenant={TENANT}
      operations={createAdminOperations(backend)}
      hasFeature={() => true}
      session={options.session}
      breadcrumbs={[
        { label: 'Acme' },
        { label: zhCN.invitations.title },
      ]}
      invitationLink={(token) => `/invitations/${token}`}
      protectedRoles={[TENANT_ADMIN_ROLE]}
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

function invitationRowsInOrder(): HTMLElement[] {
  const table = screen.getAllByRole('table').find((candidate) =>
    within(candidate).queryByRole('columnheader', {
      name: zhCN.invitations.column.email,
    }),
  )
  if (table === undefined) {
    throw new Error('the page rendered no invitation table')
  }
  return Array.from(table.querySelectorAll<HTMLElement>('tbody tr'))
}

function statusTagOf(row: HTMLElement, label: string): HTMLElement {
  const chip = within(row).getByText(label).closest('.MuiChip-root')
  const tag = chip?.parentElement
  if (!(tag instanceof HTMLElement)) {
    throw new Error(`no status tag labelled ${label}`)
  }
  return tag
}

function statusChipOf(row: HTMLElement, label: string): HTMLElement {
  const chip = within(row).getByText(label).closest('.MuiChip-root')
  if (!(chip instanceof HTMLElement)) {
    throw new Error(`no status chip labelled ${label}`)
  }
  return chip
}

function composedName(member: AdminMember): string {
  return member.username === ''
    ? member.display_name
    : `${member.display_name} (${member.username})`
}

function manageAria(member: AdminMember): string {
  return zhCN.members.manageAria.replace('{{name}}', composedName(member))
}

function manageButton(member: AdminMember): HTMLElement {
  return screen.getByRole('button', { name: manageAria(member) })
}

async function chooseInvitationAction(
  user: ReturnType<typeof userEvent.setup>,
  email: string,
  label: string,
): Promise<void> {
  const row = rowOf(email)
  await user.click(
    within(row).getByRole('button', {
      name: zhCN.invitations.actionsAria.replace('{{email}}', email),
    }),
  )
  await user.click(await screen.findByRole('menuitem', { name: label }))
}

function invitationErrorOf(email: string): HTMLElement | null {
  return rowOf(email).querySelector<HTMLElement>('[id^="invitation-error-"]')
}

async function openPanel(
  user: ReturnType<typeof userEvent.setup>,
  member: AdminMember,
): Promise<HTMLElement> {
  await screen.findAllByText(member.display_name)
  await user.click(manageButton(member))
  return screen.getByRole('dialog', { name: zhCN.members.panel.title })
}

async function removeThroughPanel(
  user: ReturnType<typeof userEvent.setup>,
  member: AdminMember,
): Promise<void> {
  const panel = await openPanel(user, member)
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
}

beforeEach(() => {
  attachTenantPermissions(FULL_TENANT_PERMISSIONS, 'acc-admin')
})

afterEach(async () => {
  await restoreRealTimers()
})

describe('the page structure', () => {
  it('renders members first and invitations second, with the tables settled', async () => {
    const { backend } = scripted()
    renderPage(backend)

    expect((await screen.findAllByText(ADA.display_name)).length).toBeGreaterThan(0)
    expect(
      screen.getByRole('heading', {
        level: 1,
        name: zhCN.invitations.title,
      }),
    ).toBeInTheDocument()
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

    const row = rowOf((await screen.findAllByText(ADA.display_name))[0]!)
    expect(
      within(row).getByText('tenant-operator'),
    ).toBeInTheDocument()
    expect(
      within(row).getByText(
        formatInstant(ADA.joined_at, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getAllByRole('columnheader', { name: zhCN.members.column.roles })
        .length,
    ).toBeGreaterThan(0)
    expect(
      screen.getByRole('columnheader', { name: zhCN.members.column.joinedAt }),
    ).toBeInTheDocument()
    const displayCell = within(row).getByText(ADA.display_name).closest('td')
    const roleCell = within(row).getByText('tenant-operator').closest('td')
    expect(displayCell?.querySelector('button')).toBeNull()
    expect(roleCell?.querySelector('button')).toBeNull()
  })

  it('shows the joined-at time in the display zone, not the browser zone', async () => {
    const { backend } = scripted()
    renderPage(backend, { timeZone: 'Asia/Tokyo' })

    const row = rowOf((await screen.findAllByText(ADA.display_name))[0]!)
    expect(
      within(row).getByText(
        new Intl.DateTimeFormat('zh-CN', {
          dateStyle: 'medium',
          timeStyle: 'short',
          timeZone: 'Asia/Tokyo',
        }).format(new Date(ADA.joined_at)),
      ),
    ).toBeInTheDocument()
  })

  it.each(['light', 'dark'] as const)(
    'renders in the %s color mode',
    async (defaultMode) => {
      const { backend } = scripted()
      renderPage(backend, { defaultMode })
      expect((await screen.findAllByText(ADA.display_name)).length).toBeGreaterThan(0)
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
    await screen.findAllByText(ADA.display_name)
    await expectNoAxeViolations()
  })
})

describe('the member search and paging', () => {
  it('retries a failed member read from the error placeholder', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const { backend } = scripted({
      listMembers: () => {
        attempts += 1
        return attempts === 1
          ? Promise.reject(new Error('members unavailable'))
          : Promise.resolve({ members: [ADA, BOB], next_cursor: '' })
      },
    })
    renderPage(backend)

    expect(
      await screen.findByText(zhCN.members.error.title),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.members.retry }),
    )
    expect(
      (await screen.findAllByText(ADA.display_name)).length,
    ).toBeGreaterThan(0)
    expect(
      screen.queryByText(zhCN.members.error.title),
    ).toBeNull()
  })

  it('debounces the search and sends it as the server query', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup({
      advanceTimers: vi.advanceTimersByTimeAsync,
    })
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)

    const input = screen.getByLabelText(zhCN.members.search.label)
    await user.type(input, 'ada')
    expect(
      recorder.memberParams.filter((params) => params?.query !== undefined),
    ).toHaveLength(0)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(SEARCH_DEBOUNCE_MS)
    })
    await restoreRealTimers()
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
    await screen.findAllByText(ADA.display_name)
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

  it('blocks a second page change while the kept previous page is still loading', async () => {
    const user = userEvent.setup()
    let resolveNext: (page: AdminMemberPage) => void = () => undefined
    const { backend, recorder } = scripted({
      listMembers: (_tenant, params) => {
        recorder.memberParams.push(params)
        if (params?.after === 'acc-1') {
          return new Promise<AdminMemberPage>((resolve) => {
            resolveNext = resolve
          })
        }
        return Promise.resolve({ members: [ADA], next_cursor: 'acc-1' })
      },
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)

    const next = screen.getByRole('button', { name: zhCN.common.pager.next })
    await user.click(next)
    await waitFor(() => {
      expect(next).toHaveAttribute('aria-busy', 'true')
      expect(next).toHaveAttribute('aria-disabled', 'true')
    })
    await user.click(next)
    expect(recorder.memberParams).toHaveLength(2)

    await act(async () => {
      resolveNext({ members: [BOB], next_cursor: '' })
    })
    expect(await screen.findByText(BOB.display_name)).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.common.pager.page.replace('{{page}}', '2')),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.common.pager.page.replace('{{page}}', '3')),
    ).toBeNull()
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

    await screen.findAllByText(ADA.display_name)
    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    expect(await screen.findByText(BOB.display_name)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'switch' }))
    await waitFor(() =>
      expect(calls.some((call) => call.tenant === 'globex')).toBe(true),
    )
    expect(
      calls
        .filter((call) => call.tenant === 'globex')
        .every((call) => (call.params as { after?: string }).after === undefined),
    ).toBe(true)
  })

  it('closes an open management panel when the host switches tenant', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    const operations = createAdminOperations(backend)
    let switchTenant: () => void = () => undefined
    function SwitchTenant() {
      const [tenant, setTenant] = useState(TENANT)
      switchTenant = () => setTenant('globex')
      return (
        <OrganizationAdminProvider
          tenant={tenant}
          operations={operations}
          hasFeature={() => true}
        >
          <OrganizationMembersPage tenant={tenant} />
        </OrganizationAdminProvider>
      )
    }
    renderWithProviders(<SwitchTenant />)

    await screen.findAllByText(ADA.display_name)
    const panel = await openPanel(user, ADA)
    expect(panel).toBeInTheDocument()

    await act(async () => {
      switchTenant()
    })

    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: zhCN.members.panel.title }),
      ).toBeNull(),
    )
  })
})

describe('the role filter', () => {
  afterEach(() => {
    window.history.replaceState({}, '', '/')
  })

  it('lists the roles the tenant read answers and filters the member read', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)

    const select = screen.getByRole('combobox', {
      name: zhCN.members.filter.roleLabel,
    })
    await user.click(select)
    expect(
      await screen.findByRole('option', { name: ROLE_SUPER.role }),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('option', { name: ROLE_OPERATOR.role }),
    )

    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({
        limit: 100,
        role: ROLE_OPERATOR.role,
      }),
    )
    expect(screen.getAllByText(ADA.display_name).length).toBeGreaterThan(0)
    expect(screen.getAllByText(BOB.display_name).length).toBeGreaterThan(0)
  })

  it('applies a role from the URL on entry and writes it back encoded', async () => {
    const user = userEvent.setup()
    const role = 'tenant operator'
    window.history.replaceState(
      {},
      '',
      `/organization/admin/invitations?role=${encodeURIComponent(role)}`,
    )
    const { backend, recorder } = scripted()
    renderPage(backend)

    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({ limit: 100, role }),
    )

    const select = screen.getByRole('combobox', {
      name: zhCN.members.filter.roleLabel,
    })
    await user.click(select)
    await user.click(
      screen.getByRole('option', { name: ROLE_SUPER.role }),
    )
    await waitFor(() =>
      expect(window.location.search).toBe(
        `?role=${encodeURIComponent(ROLE_SUPER.role)}`,
      ),
    )
    expect(window.location.pathname).toBe('/organization/admin/invitations')
  })

  it('treats an empty role parameter as no filter', async () => {
    window.history.replaceState(
      {},
      '',
      '/organization/admin/invitations?role=',
    )
    const { backend, recorder } = scripted()
    renderPage(backend)
    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({ limit: 100 }),
    )
    expect(
      recorder.memberParams.every((params) => params?.role === undefined),
    ).toBe(true)
  })

  it('shows an empty result with a clear action for an unknown role, not an error', async () => {
    const user = userEvent.setup()
    window.history.replaceState(
      {},
      '',
      '/organization/admin/invitations?role=retired-role',
    )
    const { backend, recorder } = scripted({
      listMembers: (_tenant, params) => {
        recorder.memberParams.push(params)
        return Promise.resolve(
          params?.role === 'retired-role'
            ? { members: [], next_cursor: '' }
            : { members: [ADA, BOB], next_cursor: '' },
        )
      },
    })
    renderPage(backend)

    expect(
      await screen.findByText(zhCN.members.empty.roleTitle),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        required(
          zhCN.members.empty.roleDescription.replace(
            '{{role}}',
            'retired-role',
          ),
        ),
      ),
    ).toBeInTheDocument()
    expect(screen.queryByText(zhCN.members.error.title)).toBeNull()

    await user.click(
      screen.getByRole('button', { name: zhCN.members.filter.clear }),
    )
    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({ limit: 100 }),
    )
    expect(
      (await screen.findAllByText(ADA.display_name)).length,
    ).toBeGreaterThan(0)
    expect(window.location.search).toBe('')
  })

  it('offers a retry when the role list cannot be read and invents no options', async () => {
    const user = userEvent.setup()
    const listTenantRoles = vi.fn(() =>
      Promise.reject(new Error('roles unavailable')),
    )
    window.history.replaceState(
      {},
      '',
      `/organization/admin/invitations?role=${encodeURIComponent(ROLE_OPERATOR.role)}`,
    )
    const { backend, recorder } = scripted({ listTenantRoles })
    renderPage(backend)

    expect(
      await screen.findByText(zhCN.members.filter.rolesError),
    ).toBeInTheDocument()
    expect(recorder.memberParams).toContainEqual({
      limit: 100,
      role: ROLE_OPERATOR.role,
    })
    await user.click(
      screen.getByRole('combobox', { name: zhCN.members.filter.roleLabel }),
    )
    expect(
      screen.getByRole('option', { name: zhCN.members.filter.anyRole }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: ROLE_SUPER.role }),
    ).toBeNull()
    await user.keyboard('{Escape}')

    await user.click(
      screen.getByRole('button', { name: zhCN.members.filter.rolesRetry }),
    )
    await waitFor(() => expect(listTenantRoles).toHaveBeenCalledTimes(2))
  })

  it('carries the role into the next cursor page', async () => {
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
    await screen.findAllByText(ADA.display_name)
    const select = screen.getByRole('combobox', {
      name: zhCN.members.filter.roleLabel,
    })
    await user.click(select)
    await user.click(
      screen.getByRole('option', { name: ROLE_OPERATOR.role }),
    )
    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({
        limit: 100,
        role: ROLE_OPERATOR.role,
      }),
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({
        limit: 100,
        role: ROLE_OPERATOR.role,
        after: 'acc-1',
      }),
    )
  })

  it('returns to the first page and clears the member scope when the filter changes', async () => {
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
    await screen.findAllByText(ADA.display_name)
    await user.click(
      screen.getByRole('button', { name: zhCN.common.pager.next }),
    )
    expect(
      (await screen.findAllByText(BOB.display_name)).length,
    ).toBeGreaterThan(0)

    const panel = await openPanel(user, BOB)
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
    const removedMessage = zhCN.members.removed.replace(
      '{{account}}',
      composedName(BOB),
    )
    expect(
      await screen.findByText(removedMessage),
    ).toBeInTheDocument()

    const select = screen.getByRole('combobox', {
      name: zhCN.members.filter.roleLabel,
    })
    await user.click(select)
    await user.click(
      screen.getByRole('option', { name: ROLE_OPERATOR.role }),
    )

    await waitFor(() =>
      expect(recorder.memberParams).toContainEqual({
        limit: 100,
        role: ROLE_OPERATOR.role,
      }),
    )
    expect(screen.queryByText(removedMessage)).toBeNull()
    expect(
      screen.getByText(zhCN.common.pager.page.replace('{{page}}', '1')),
    ).toBeInTheDocument()
  })
})

describe('the member management panel', () => {
  it('binds a role and unbinds one only after confirmation', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantAccountBindings: () => Promise.resolve([SUPER_BINDING]),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)

    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    expect(
      screen.queryByRole('option', { name: ROLE_SUPER.role }),
    ).toBeNull()
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
    expect(bind).not.toHaveAttribute('aria-disabled', 'true')
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

    const unbind = within(panel).getByRole('button', {
      name: zhCN.members.panel.unbind,
    })
    await user.click(unbind)
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.members.panel.unbindTitle,
    })
    expect(
      within(dialog).getByText(
        zhCN.members.panel.unbindMessage.replace(
          '{{role}}',
          ROLE_SUPER.role,
        ),
      ),
    ).toBeInTheDocument()
    expect(recorder.unbinds).toEqual([])
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.cancel }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', {
          name: zhCN.members.panel.unbindTitle,
        }),
      ).toBeNull(),
    )
    expect(recorder.unbinds).toEqual([])

    await user.click(unbind)
    const again = await screen.findByRole('dialog', {
      name: zhCN.members.panel.unbindTitle,
    })
    await user.click(
      within(again).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.unbinds).toEqual([
        {
          tenant: TENANT,
          account: ADA.account,
          role: ROLE_SUPER.role,
        },
      ]),
    )
  })

  it('offers only unbound roles and says when every role is bound', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      listTenantRoles: () => Promise.resolve([ROLE_OPERATOR]),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)

    expect(
      await within(panel).findByText(zhCN.members.panel.allRolesBound),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('combobox', {
        name: zhCN.members.panel.roleLabel,
      }),
    ).toHaveAttribute('aria-disabled', 'true')
  })

  it('disables unbinding its own protected binding and says why', async () => {
    attachTenantPermissions(FULL_TENANT_PERMISSIONS, SELF)
    const ownBinding: AdminTenantBinding = {
      tenant: TENANT,
      account: ADA.account,
      role: TENANT_ADMIN_ROLE,
      granted_at: '2026-10-15T12:00:00Z',
    }
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantAccountBindings: () => Promise.resolve([ownBinding]),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)

    expect(
      await within(panel).findByText(zhCN.members.panel.cannotUnbindOwnAdmin),
    ).toBeInTheDocument()
    const row = within(panel).getByText(TENANT_ADMIN_ROLE).closest('li')
    if (row === null) {
      throw new Error('no tenant-admin binding row')
    }
    const unbind = within(row).getByRole('button', {
      name: zhCN.members.panel.unbind,
    })
    expect(unbind).toHaveAttribute('aria-disabled', 'true')
    expect(unbind).toHaveAccessibleDescription(
      zhCN.members.panel.cannotUnbindOwnAdmin,
    )
    expect(recorder.unbinds).toEqual([])
  })

  it('suspends an active member and resumes a suspended one', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)

    const activePanel = await openPanel(user, ADA)
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
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: uiKitZh.confirmDialog.title }),
      ).toBeNull(),
    )
    await user.click(
      within(activePanel).getByRole('button', {
        name: zhCN.members.panel.closeAria.replace(
          '{{name}}',
          composedName(ADA),
        ),
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: zhCN.members.panel.title }),
      ).toBeNull(),
    )

    const suspendedPanel = await openPanel(user, BOB)
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
    const panel = await openPanel(user, ADA)

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
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.members.panel.suspendedBindNote,
    )
  })

  it('disables suspending yourself and says why', async () => {
    attachTenantPermissions(FULL_TENANT_PERMISSIONS, SELF)
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA)
    const suspend = within(panel).getByRole('button', {
      name: zhCN.members.panel.suspend,
    })
    expect(suspend).toHaveAttribute('aria-disabled', 'true')
    expect(suspend).toHaveAccessibleDescription(
      zhCN.members.panel.cannotSuspendSelf,
    )
    expect(
      within(panel).getByText(zhCN.members.panel.cannotSuspendSelf),
    ).toBeInTheDocument()
  })

  it('removes a member behind the double confirmation', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, BOB)

    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.remove }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
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
    expect(
      await screen.findByText(
        zhCN.members.removed.replace('{{account}}', composedName(BOB)),
      ),
    ).toBeInTheDocument()
  })

  it('adds the last-admin page text when the removal answers a conflict', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      removeMember: () => Promise.reject({ code: 'authn.conflict' }),
    })
    renderPage(backend)
    const panel = await openPanel(user, BOB)
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

  it('refuses to remove your own membership and says why', async () => {
    attachTenantPermissions(FULL_TENANT_PERMISSIONS, SELF)
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA)
    const remove = within(panel).getByRole('button', {
      name: zhCN.members.remove,
    })
    expect(remove).toHaveAttribute('aria-disabled', 'true')
    expect(remove).toHaveAccessibleDescription(
      zhCN.members.panel.cannotSuspendSelf,
    )
    expect(
      within(panel).getByText(zhCN.members.panel.cannotSuspendSelf),
    ).toBeInTheDocument()
  })

  it('explains that binding needs a role before it sends anything', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA)

    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.members.panel.bindNoRole,
    )
    await user.click(bind)
    expect(recorder.binds).toEqual([])
  })

  it('locks the role choice read-only while a bind is in flight', async () => {
    const user = userEvent.setup()
    let resolveBind: () => void = () => {}
    const { backend, recorder } = scripted({
      listTenantAccountBindings: () => Promise.resolve([SUPER_BINDING]),
      bindTenantRole: (input) => {
        recorder.binds.push(input)
        return new Promise<void>((resolve) => {
          resolveBind = resolve
        })
      },
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)
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

    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    )
    await waitFor(() =>
      expect(recorder.binds).toHaveLength(1),
    )
    expect(select).toHaveAttribute('aria-readonly', 'true')
    expect(select).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(select)
    expect(screen.queryByRole('listbox')).toBeNull()

    await act(async () => {
      resolveBind()
    })
    await waitFor(() =>
      expect(select).not.toHaveAttribute('aria-readonly', 'true'),
    )
  })

  it('moves focus to the next row when the panel removes a member', async () => {
    const user = userEvent.setup()
    let members: AdminMember[] = [ADA, BOB, CAROL]
    const { backend } = scripted({
      listMembers: () => Promise.resolve({ members, next_cursor: '' }),
      removeMember: (input) => {
        members = members.filter((row) => row.account !== input.account)
        return Promise.resolve()
      },
    })
    renderPage(backend)
    await removeThroughPanel(user, BOB)

    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: manageAria(BOB) }),
      ).toBeNull(),
    )
    expect(manageButton(CAROL)).toHaveFocus()
  })

  it('moves focus to the previous row when the panel removes the last member', async () => {
    const user = userEvent.setup()
    let members: AdminMember[] = [ADA, BOB]
    const { backend } = scripted({
      listMembers: () => Promise.resolve({ members, next_cursor: '' }),
      removeMember: (input) => {
        members = members.filter((row) => row.account !== input.account)
        return Promise.resolve()
      },
    })
    renderPage(backend)
    await removeThroughPanel(user, BOB)

    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: manageAria(BOB) }),
      ).toBeNull(),
    )
    expect(manageButton(ADA)).toHaveFocus()
  })

  it('moves focus to the member list container when the removed row was the only one', async () => {
    const user = userEvent.setup()
    let members: AdminMember[] = [BOB]
    const { backend } = scripted({
      listMembers: () => Promise.resolve({ members, next_cursor: '' }),
      removeMember: (input) => {
        members = members.filter((row) => row.account !== input.account)
        return Promise.resolve()
      },
    })
    renderPage(backend)
    await removeThroughPanel(user, BOB)

    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: manageAria(BOB) }),
      ).toBeNull(),
    )
    expect(document.activeElement).not.toBe(document.body)
    expect(
      screen.getByRole('group', { name: zhCN.members.title }),
    ).toHaveFocus()
  })

  it('marks only the bind control busy and the other panel controls unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const unbindRole = vi.fn(() => Promise.resolve())
    const writeMember = vi.fn(() => Promise.resolve())
    const { backend } = scripted({
      listMembers: () => Promise.resolve({ members: [CAROL], next_cursor: '' }),
      listTenantGrants: () => Promise.resolve([]),
      bindTenantRole: () => pending.promise,
      unbindTenantRole: unbindRole,
      writeMember,
    })
    renderPage(backend)
    const panel = await openPanel(user, CAROL)

    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(await screen.findByRole('option', { name: ROLE_SUPER.role }))
    await waitFor(() =>
      expect(
        within(panel).queryByText(zhCN.members.panel.escalationChecking),
      ).toBeNull(),
    )
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    await user.click(bind)

    expect(bind).toHaveAttribute('aria-busy', 'true')
    const unbind = within(panel).getByRole('button', {
      name: zhCN.members.panel.unbind,
    })
    const suspend = within(panel).getByRole('button', {
      name: zhCN.members.panel.suspend,
    })
    const remove = within(panel).getByRole('button', {
      name: zhCN.members.remove,
    })
    for (const control of [unbind, suspend, remove]) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).not.toHaveAttribute('aria-busy')
      expect(control).not.toHaveAttribute('aria-describedby')
    }

    fireEvent.click(unbind)
    fireEvent.click(suspend)
    fireEvent.click(remove)
    await act(async () => {
      await Promise.resolve()
    })
    expect(unbindRole).not.toHaveBeenCalled()
    expect(writeMember).not.toHaveBeenCalled()
    expect(
      screen.queryByRole('dialog', { name: zhCN.members.removeTitle }),
    ).toBeNull()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the unbinding row busy and the other panel controls unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const bindTenantRole = vi.fn(() => Promise.resolve())
    const { backend } = scripted({
      listMembers: () => Promise.resolve({ members: [CAROL], next_cursor: '' }),
      listTenantGrants: () => Promise.resolve([]),
      bindTenantRole,
      unbindTenantRole: () => pending.promise,
    })
    renderPage(backend)
    const panel = await openPanel(user, CAROL)

    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.panel.unbind }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )

    const unbind = within(panel).getByRole('button', {
      name: zhCN.members.panel.unbind,
    })
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    const suspend = within(panel).getByRole('button', {
      name: zhCN.members.panel.suspend,
    })
    const remove = within(panel).getByRole('button', {
      name: zhCN.members.remove,
    })
    expect(unbind).toHaveAttribute('aria-busy', 'true')
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).not.toHaveAttribute('aria-busy')
    for (const control of [suspend, remove]) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).not.toHaveAttribute('aria-busy')
      expect(control).not.toHaveAttribute('aria-describedby')
    }

    fireEvent.click(bind)
    await act(async () => {
      await Promise.resolve()
    })
    expect(bindTenantRole).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the removing control busy and the other panel controls unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const { backend } = scripted({
      listMembers: () => Promise.resolve({ members: [CAROL], next_cursor: '' }),
      listTenantGrants: () => Promise.resolve([]),
      removeMember: () => pending.promise,
    })
    renderPage(backend)
    const panel = await openPanel(user, CAROL)

    await user.click(
      within(panel).getByRole('button', { name: zhCN.members.remove }),
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, 700),
      )
    })
    await user.click(
      screen.getByRole('button', {
        name: uiKitZh.confirmDialog.confirmAgainLabel,
      }),
    )

    const remove = within(panel).getByRole('button', {
      name: zhCN.members.remove,
    })
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    const unbind = within(panel).getByRole('button', {
      name: zhCN.members.panel.unbind,
    })
    const suspend = within(panel).getByRole('button', {
      name: zhCN.members.panel.suspend,
    })
    expect(remove).toHaveAttribute('aria-busy', 'true')
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).not.toHaveAttribute('aria-busy')
    for (const control of [unbind, suspend]) {
      expect(control).toHaveAttribute('aria-disabled', 'true')
      expect(control).not.toHaveAttribute('aria-busy')
      expect(control).not.toHaveAttribute('aria-describedby')
    }

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })
})

describe('the role permission tiers', () => {
  it('without role.read says so and reads no bindings', async () => {
    attachTenantPermissions(null)
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA)

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
    expect(recorder.roleReads).toEqual([TENANT])
  })

  it('with role.read but not role.manage keeps the list read-only', async () => {
    attachTenantPermissions(['authz.tenant.role.read'])
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA)

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
    expect(recorder.roleReads).toEqual([TENANT])
  })

  it('with role.manage renders the complete control set', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    const panel = await openPanel(user, ADA)

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
    const panel = await openPanel(user, ADA)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_SUPER.role }))

    expect(
      await within(panel).findByText(zhCN.members.panel.escalationBlocked),
    ).toBeInTheDocument()
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.members.panel.escalationBlocked,
    )
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
    const panel = await openPanel(user, ADA)
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

    expect(
      within(panel).queryByText(zhCN.members.panel.escalationBlocked),
    ).toBeNull()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('trusts the server permission even when the recorded pair is not inferable', async () => {
    attachTenantPermissions(
      GOVERNED_PERMISSIONS.map((entry) => entry.permission),
    )
    const user = userEvent.setup()
    const grants = GOVERNED_GRANTS.map((grant) => ({
      ...grant,
      resource: 'retired_object',
      action: 'retired_action',
    }))
    const { backend } = scripted({
      listTenantRoles: () => Promise.resolve([ROLE_GOVERNED]),
      listTenantPermissions: () => Promise.resolve(GOVERNED_PERMISSIONS),
      listTenantGrants: () => Promise.resolve(grants),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)
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
    expect(
      within(panel).queryByText(zhCN.members.panel.escalationBlocked),
    ).toBeNull()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.bind }),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('fails closed when a grant carries no permission code, while unbind stays available', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantGrants: () =>
        Promise.resolve([
          {
            ...SUPER_GRANT,
            permission: '',
            resource: 'retired_object',
            action: 'retired_action',
          },
        ]),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_SUPER.role }))

    expect(
      await within(panel).findByText(
        zhCN.members.panel.escalationUnconfirmed,
      ),
    ).toBeInTheDocument()
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.members.panel.escalationUnconfirmed,
    )
    expect(recorder.binds).toEqual([])

    const unbind = within(panel).getByRole('button', {
      name: zhCN.members.panel.unbind,
    })
    expect(unbind).not.toHaveAttribute('aria-disabled', 'true')
    await user.click(unbind)
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.unbinds).toEqual([
        {
          tenant: TENANT,
          account: ADA.account,
          role: OPERATOR_BINDING.role,
        },
      ]),
    )
  })

  it('does not default to allowing when the grant read fails', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantGrants: () => Promise.reject(new Error('grants unavailable')),
    })
    renderPage(backend)
    const panel = await openPanel(user, ADA)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_SUPER.role }))

    expect(
      await within(panel).findByText(zhCN.members.panel.escalationFailed),
    ).toBeInTheDocument()
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.members.panel.escalationFailed,
    )
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
    const panel = await openPanel(user, BOB)
    const select = within(panel).getByRole('combobox', {
      name: zhCN.members.panel.roleLabel,
    })
    await waitFor(() => expect(select).toBeEnabled())
    await user.click(select)
    await user.click(screen.getByRole('option', { name: ROLE_SUPER.role }))
    await waitFor(() =>
      expect(
        within(panel).queryByText(zhCN.members.panel.escalationChecking),
      ).toBeNull(),
    )
    const bind = within(panel).getByRole('button', {
      name: zhCN.members.panel.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.members.panel.suspendedBindNote,
    )
    expect(
      within(panel).getByText(zhCN.members.panel.suspendedBindNote),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', { name: zhCN.members.panel.unbind }),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })
})

describe('the invitation section', () => {
  it('opens on pending, reads the server state, and filters the server read', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)

    const pendingRow = rowOf(await screen.findByText(PENDING_INVITATION.email))
    expect(
      within(pendingRow).getByText(zhCN.invitations.status.pending),
    ).toBeInTheDocument()
    expect(screen.queryByText(EXPIRED_INVITATION.email)).toBeNull()
    expect(screen.queryByText(ACCEPTED_INVITATION.email)).toBeNull()
    expect(screen.queryByText(REVOKED_INVITATION.email)).toBeNull()
    expect(recorder.invitationParams).toContainEqual({
      limit: 50,
      status: 'pending',
    })

    await user.click(
      screen.getByRole('combobox', { name: zhCN.invitations.filter.label }),
    )
    await user.click(screen.getByRole('option', { name: zhCN.invitations.filter.all }))
    expect(
      await screen.findByText(EXPIRED_INVITATION.email),
    ).toBeInTheDocument()
    expect(
      invitationRowsInOrder().map(
        (row) =>
          ALL_INVITATIONS.find((invitation) =>
            row.textContent?.includes(invitation.email),
          )?.email,
      ),
    ).toEqual(ALL_INVITATIONS.map((invitation) => invitation.email))
    const expiredRow = rowOf(EXPIRED_INVITATION.email)
    expect(
      within(expiredRow).getByText(zhCN.invitations.status.expired),
    ).toBeInTheDocument()
    expect(
      within(expiredRow).getByText(
        fill(zhCN.invitations.statusSince.expired, {
          time: formatInstant(EXPIRED_INVITATION.expires_at, 'zh-CN', {
            timeZone: TEST_TIME_ZONE,
          }),
        }),
      ),
    ).toBeInTheDocument()
    const acceptedRow = rowOf(ACCEPTED_INVITATION.email)
    expect(
      within(acceptedRow).getByText(
        fill(zhCN.invitations.statusSince.accepted, {
          time: formatInstant(
            ACCEPTED_INVITATION.accepted_at ?? '',
            'zh-CN',
            { timeZone: TEST_TIME_ZONE },
          ),
        }),
      ),
    ).toBeInTheDocument()
    const revokedRow = rowOf(REVOKED_INVITATION.email)
    expect(
      within(revokedRow).getByText(
        fill(zhCN.invitations.statusSince.revoked, {
          time: formatInstant(REVOKED_INVITATION.revoked_at ?? '', 'zh-CN', {
            timeZone: TEST_TIME_ZONE,
          }),
        }),
      ),
    ).toBeInTheDocument()

    expect(
      statusChipOf(rowOf(PENDING_INVITATION.email), zhCN.invitations.status.pending),
    ).toHaveClass('MuiChip-colorInfo')
    expect(
      statusChipOf(rowOf(EXPIRED_INVITATION.email), zhCN.invitations.status.expired),
    ).toHaveClass('MuiChip-colorDefault')
    expect(
      statusChipOf(rowOf(ACCEPTED_INVITATION.email), zhCN.invitations.status.accepted),
    ).toHaveClass('MuiChip-colorSuccess')
    expect(
      statusChipOf(rowOf(REVOKED_INVITATION.email), zhCN.invitations.status.revoked),
    ).toHaveClass('MuiChip-colorDefault')

    expect(
      cellOf(
        rowOf(PENDING_INVITATION.email),
        zhCN.invitations.column.expires,
      ).textContent,
    ).not.toBe('')
    for (const invitation of [
      EXPIRED_INVITATION,
      ACCEPTED_INVITATION,
      REVOKED_INVITATION,
    ]) {
      expect(
        cellOf(rowOf(invitation.email), zhCN.invitations.column.expires)
          .textContent?.trim(),
      ).toBe('')
    }

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

  it('renders the rows with the contract fields and hides actions on terminal rows', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.click(
      screen.getByRole('combobox', { name: zhCN.invitations.filter.label }),
    )
    await user.click(screen.getByRole('option', { name: zhCN.invitations.filter.all }))

    const pendingRow = rowOf(await screen.findByText(PENDING_INVITATION.email))
    const inviter = within(pendingRow).getByText(ADA.display_name)
    expect(inviter).toHaveAttribute(
      'title',
      `${ADA.display_name} (${ADA.username})`,
    )
    expect(
      within(pendingRow).getByText(ROLE_OPERATOR.role),
    ).toBeInTheDocument()
    expect(
      within(pendingRow).queryByRole('button', {
        name: zhCN.invitations.actionsAria.replace(
          '{{email}}',
          PENDING_INVITATION.email,
        ),
      }),
    ).toBeInTheDocument()

    for (const invitation of [EXPIRED_INVITATION, ACCEPTED_INVITATION, REVOKED_INVITATION]) {
      const row = rowOf(invitation.email)
      expect(
        within(row).queryByRole('button', {
          name: zhCN.invitations.actionsAria.replace('{{email}}', invitation.email),
        }),
      ).toBeNull()
    }
  })

  it('attaches a role the caller may grant and hides the choice without role management', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    const view = renderPage(backend)
    await screen.findAllByText(ADA.display_name)

    const role = screen.getByRole('combobox', {
      name: zhCN.invitations.issue.roleLabel,
    })
    await user.click(role)
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.invitations.issue.roleChecking),
      ).toBeNull(),
    )
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'role@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    await waitFor(() =>
      expect(recorder.issues).toEqual([
        {
          tenant: TENANT,
          email: 'role@example.com',
          expiresAt: expect.any(String),
          role: ROLE_OPERATOR.role,
        },
      ]),
    )
    view.unmount()

    attachTenantPermissions(['authn.membership.manage'])
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    expect(
      screen.queryByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    ).toBeNull()
  })

  it('clears a chosen role when the role-management permission leaves', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)

    const role = screen.getByRole('combobox', {
      name: zhCN.invitations.issue.roleLabel,
    })
    await user.click(role)
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.invitations.issue.roleChecking),
      ).toBeNull(),
    )

    attachTenantPermissions(['authn.membership.manage'])
    await waitFor(() =>
      expect(
        screen.queryByRole('combobox', {
          name: zhCN.invitations.issue.roleLabel,
        }),
      ).toBeNull(),
    )

    attachTenantPermissions(FULL_TENANT_PERMISSIONS, 'acc-admin')
    const restored = await screen.findByRole('combobox', {
      name: zhCN.invitations.issue.roleLabel,
    })
    expect(restored).not.toHaveTextContent(ROLE_OPERATOR.role)
  })

  it('explains the empty address on the issue control without sending', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)

    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.emailRequired,
    )
    await user.click(submit)
    submit.focus()
    expect(submit).toHaveFocus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(recorder.issues).toEqual([])
  })

  it('keeps focus on the issue control while the write runs and after it clears the address', async () => {
    const user = userEvent.setup()
    let resolveIssue: () => void = () => {}
    const { backend, recorder } = scripted({
      issueInvitation: (input) => {
        recorder.issues.push(input)
        return new Promise<AdminIssuedInvitation>((resolve) => {
          resolveIssue = () => resolve(issuedFixture(input.email))
        })
      },
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'again@example.com',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    await user.click(submit)

    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveFocus()

    await act(async () => {
      resolveIssue()
    })
    await waitFor(() =>
      expect(submit).toHaveAttribute('aria-disabled', 'true'),
    )
    expect(submit).toHaveFocus()
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.emailRequired,
    )
    await user.click(submit)
    expect(recorder.issues).toHaveLength(1)
  })

  it('blocks attaching a role whose grants exceed the caller', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'blocked@example.com',
    )
    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    )
    await user.click(screen.getByRole('option', { name: ROLE_SUPER.role }))

    expect(
      await screen.findByText(zhCN.invitations.issue.roleBlocked),
    ).toBeInTheDocument()
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.roleBlocked,
    )
    expect(recorder.issues).toEqual([])
  })

  it('shows the checking state while the selected role grants load', async () => {
    const user = userEvent.setup()
    let resolveGrants: (grants: readonly AdminTenantGrant[]) => void = () => {}
    const { backend } = scripted({
      listTenantGrants: () =>
        new Promise((resolve) => {
          resolveGrants = resolve
        }),
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'checking@example.com',
    )
    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    )
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))

    expect(
      screen.getByText(zhCN.invitations.issue.roleChecking),
    ).toBeInTheDocument()
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.roleChecking,
    )

    await act(async () => {
      resolveGrants([])
    })
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.invitations.issue.roleChecking),
      ).toBeNull(),
    )
  })

  it('fails closed and offers a retry when the role grants cannot be read', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantGrants: () => Promise.reject(new Error('grants unavailable')),
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'failed@example.com',
    )
    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    )
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))

    expect(
      await screen.findByText(zhCN.invitations.issue.roleFailed),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.invitations.issue.roleRetry }),
    ).toBeInTheDocument()
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.roleFailed,
    )
    expect(recorder.issues).toEqual([])
  })

  it('fails closed when a grant carries no permission code', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      listTenantGrants: () =>
        Promise.resolve([
          {
            tenant: TENANT,
            role: ROLE_OPERATOR.role,
            permission: '',
            resource: 'unknown',
            action: 'unknown',
            created_at: '2026-01-01T00:00:00Z',
          },
        ]),
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'unconfirmed@example.com',
    )
    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    )
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))

    expect(
      await screen.findByText(zhCN.invitations.issue.roleUnconfirmed),
    ).toBeInTheDocument()
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.roleUnconfirmed,
    )
    expect(recorder.issues).toEqual([])
  })

  it("starts the issue form's role clean when the host switches tenant", async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    function SwitchTenant() {
      const [tenant, setTenant] = useState(TENANT)
      return (
        <OrganizationAdminProvider
          tenant={tenant}
          operations={createAdminOperations(backend)}
          hasFeature={() => true}
          protectedRoles={[TENANT_ADMIN_ROLE]}
        >
          <button type="button" onClick={() => setTenant('globex')}>
            switch
          </button>
          <OrganizationMembersPage tenant={tenant} />
        </OrganizationAdminProvider>
      )
    }
    renderWithProviders(<SwitchTenant />)
    await screen.findAllByText(ADA.display_name)

    await user.click(
      screen.getByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    )
    await user.click(screen.getByRole('option', { name: ROLE_OPERATOR.role }))
    expect(
      screen.getByRole('combobox', {
        name: zhCN.invitations.issue.roleLabel,
      }),
    ).toHaveTextContent(ROLE_OPERATOR.role)

    await user.click(screen.getByRole('button', { name: 'switch' }))
    await waitFor(() =>
      expect(
        screen
          .getByRole('combobox', {
            name: zhCN.invitations.issue.roleLabel,
          })
          .closest('.MuiInputBase-root')
          ?.querySelector('input')
          ?.getAttribute('value'),
      ).toBe(''),
    )
  })

  it('offers the enterprise re-login exit for an SSO refusal on an invitation write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const { backend } = scripted({
      issueInvitation: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderPage(backend, { session })
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'sso@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    await user.click(
      screen.getByRole('button', { name: zhCN.stepUp.relogin }),
    )
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('marks a deadline within a day with the time left', async () => {
    const soon: AdminInvitation = {
      ...PENDING_INVITATION,
      id: 'inv-soon',
      email: 'soon@example.com',
      expires_at: new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString(),
    }
    const { backend } = scripted({
      listInvitations: () =>
        Promise.resolve({ invitations: [soon], next_cursor: '' }),
    })
    renderPage(backend)
    const row = rowOf(await screen.findByText(soon.email))
    expect(
      within(row).getByText(
        zhCN.invitations.expires.remainingHours.replace('{{hours}}', '1'),
      ),
    ).toBeInTheDocument()
  })

  it('shows the resend count and the latest send beside the time', async () => {
    const resent: AdminInvitation = {
      ...PENDING_INVITATION,
      id: 'inv-resent',
      email: 'resent@example.com',
      send_count: 3,
      last_sent_at: '2026-11-10T09:30:00Z',
    }
    const { backend } = scripted({
      listInvitations: () =>
        Promise.resolve({ invitations: [resent], next_cursor: '' }),
    })
    renderPage(backend)
    const row = rowOf(await screen.findByText(resent.email))
    expect(
      within(row).getByText(
        formatInstant(resent.created_at, 'zh-CN', {
          timeZone: TEST_TIME_ZONE,
        }),
      ),
    ).toBeInTheDocument()
    expect(
      within(row).getByText(
        fill(zhCN.invitations.created.resent, {
          count: '2',
          time: formatInstant('2026-11-10T09:30:00Z', 'zh-CN', {
            timeZone: TEST_TIME_ZONE,
          }),
        }),
      ),
    ).toBeInTheDocument()
  })

  it('leaves a missing inviter and a null terminal instant blank', async () => {
    const user = userEvent.setup()
    const nameless: AdminInvitation = {
      ...PENDING_INVITATION,
      id: 'inv-nameless',
      email: 'nameless@example.com',
      status: 'accepted',
      state: 'accepted',
      invited_by_name: undefined,
      invited_by_username: undefined,
      accepted_at: null,
    }
    const { backend } = scripted({
      listInvitations: () =>
        Promise.resolve({ invitations: [nameless], next_cursor: '' }),
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.click(
      screen.getByRole('combobox', { name: zhCN.invitations.filter.label }),
    )
    await user.click(screen.getByRole('option', { name: zhCN.invitations.filter.all }))
    const row = rowOf(await screen.findByText(nameless.email))
    expect(
      within(row).getByText(zhCN.invitations.status.accepted),
    ).toBeInTheDocument()
    expect(statusTagOf(row, zhCN.invitations.status.accepted).textContent).toBe(
      zhCN.invitations.status.accepted,
    )
    expect(within(row).queryByText(ADA.display_name)).toBeNull()
  })

  it('resends the same invitation and exposes the new one-time link', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted({
      resendInvitation: (input) => {
        recorder.resends.push(input)
        return Promise.resolve({
          ...issuedFixture(PENDING_INVITATION.email),
          invitation: {
            ...issuedFixture(PENDING_INVITATION.email).invitation,
            id: PENDING_INVITATION.id,
          },
        })
      },
    })
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)
    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.resend,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() =>
      expect(recorder.resends).toEqual([
        { tenant: TENANT, id: PENDING_INVITATION.id },
      ]),
    )
    expect(recorder.issues).toEqual([])
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
  })

  it('states the unchanged deadline before resending', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)

    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.resend,
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.invitations.resendTitle,
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.invitations.resendMessage, {
          email: PENDING_INVITATION.email,
          time: formatInstant(PENDING_INVITATION.expires_at, 'zh-CN', {
            timeZone: TEST_TIME_ZONE,
          }),
        }),
        { exact: true },
      ),
    ).toBeInTheDocument()
  })

  it('clears an earlier failed row marker when a later issue succeeds', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      resendInvitation: () =>
        Promise.reject(new Error('invite write refused')),
    })
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)
    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.resend,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const failedMarker = await waitFor(() => {
      const marker = invitationErrorOf(PENDING_INVITATION.email)
      expect(marker).not.toBeNull()
      return marker as HTMLElement
    })
    expect(failedMarker.textContent ?? '').not.toBe('')

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'fresh@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    await screen.findByText(
      `${window.location.origin}/invitations/spd_inv_new_token`,
    )
    expect(invitationErrorOf(PENDING_INVITATION.email)).toBeNull()
  })

  it('clears the invitation scope and row marker when the status filter changes', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      resendInvitation: () =>
        Promise.reject(new Error('invite write refused')),
    })
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)
    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.resend,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const failedMarker = await waitFor(() => {
      const marker = invitationErrorOf(PENDING_INVITATION.email)
      expect(marker).not.toBeNull()
      return marker as HTMLElement
    })
    const failureText = failedMarker.textContent ?? ''
    expect(failureText).not.toBe('')

    await user.click(
      screen.getByRole('combobox', { name: zhCN.invitations.filter.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.invitations.filter.all }),
    )
    await screen.findByText(PENDING_INVITATION.email)
    expect(invitationErrorOf(PENDING_INVITATION.email)).toBeNull()
    expect(screen.queryByText(failureText)).toBeNull()
  })

  it('revokes a pending invitation', async () => {
    const user = userEvent.setup()
    const { backend, recorder } = scripted()
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)
    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.revoke,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(recorder.revokes).toEqual([
        { tenant: TENANT, id: PENDING_INVITATION.id },
      ]),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const heading = screen.getByRole('heading', {
      name: zhCN.invitations.section.title,
    })
    const invitationSection = heading.closest('section')
    expect(invitationSection).not.toBeNull()
    await waitFor(() => expect(document.activeElement).toBe(heading))
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(
      within(invitationSection as HTMLElement)
        .getByText(
          fill(zhCN.invitations.revoked, { email: PENDING_INVITATION.email }),
        )
        .closest('[role="status"]'),
    ).not.toBeNull()
  })

  it('re-reads the list when a revoke answers invitation-terminal', async () => {
    const user = userEvent.setup()
    let terminal = false
    const expired: AdminInvitation = {
      ...PENDING_INVITATION,
      state: 'expired',
      expires_at: '2026-02-01T00:00:00Z',
    }
    bindAdminRequest((call) => {
      if (
        call.method === 'GET' &&
        call.path === `/api/v1/tenants/${TENANT}/members`
      ) {
        return { members: [], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === `/api/v1/tenants/${TENANT}/roles`
      ) {
        return { roles: [] }
      }
      if (
        call.method === 'GET' &&
        call.path === `/api/v1/tenants/${TENANT}/invitations`
      ) {
        return {
          invitations: [terminal ? expired : PENDING_INVITATION],
          next_cursor: '',
        }
      }
      if (
        call.method === 'DELETE' &&
        call.path ===
          `/api/v1/tenants/${TENANT}/invitations/${PENDING_INVITATION.id}`
      ) {
        terminal = true
        throw { status: 409, code: 'authn.invitation_terminal' }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={organizationAdminOperations}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
        protectedRoles={[TENANT_ADMIN_ROLE]}
      >
        <OrganizationMembersPage tenant={TENANT} />
      </OrganizationAdminProvider>,
    )
    await screen.findByText(PENDING_INVITATION.email)
    await user.click(
      screen.getByRole('combobox', { name: zhCN.invitations.filter.label }),
    )
    await user.click(
      screen.getByRole('option', { name: zhCN.invitations.filter.all }),
    )
    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.revoke,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() =>
      expect(
        within(rowOf(PENDING_INVITATION.email)).getByText(
          zhCN.invitations.status.expired,
        ),
      ).toBeInTheDocument(),
    )
  })

  it('moves focus to the invitation heading when a terminal revoke empties the pending filter', async () => {
    const user = userEvent.setup()
    let terminal = false
    const expired: AdminInvitation = {
      ...PENDING_INVITATION,
      state: 'expired',
      expires_at: '2026-02-01T00:00:00Z',
    }
    bindAdminRequest((call) => {
      if (
        call.method === 'GET' &&
        call.path === `/api/v1/tenants/${TENANT}/members`
      ) {
        return { members: [], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === `/api/v1/tenants/${TENANT}/roles`
      ) {
        return { roles: [] }
      }
      if (
        call.method === 'GET' &&
        call.path === `/api/v1/tenants/${TENANT}/invitations`
      ) {
        if (terminal && call.query?.status === 'pending') {
          return { invitations: [], next_cursor: '' }
        }
        return {
          invitations: [terminal ? expired : PENDING_INVITATION],
          next_cursor: '',
        }
      }
      if (
        call.method === 'DELETE' &&
        call.path ===
          `/api/v1/tenants/${TENANT}/invitations/${PENDING_INVITATION.id}`
      ) {
        terminal = true
        throw { status: 409, code: 'authn.invitation_terminal' }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={organizationAdminOperations}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
        protectedRoles={[TENANT_ADMIN_ROLE]}
      >
        <OrganizationMembersPage tenant={TENANT} />
      </OrganizationAdminProvider>,
    )
    await screen.findByText(PENDING_INVITATION.email)
    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.revoke,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const heading = screen.getByRole('heading', {
      name: zhCN.invitations.section.title,
    })
    await waitFor(() =>
      expect(screen.queryByText(PENDING_INVITATION.email)).toBeNull(),
    )
    await waitFor(() => expect(document.activeElement).toBe(heading))
    expect(heading).toHaveAttribute('tabindex', '-1')
  })

  it('asks before closing the one-time panel and only then clears it', async () => {
    const user = userEvent.setup()
    const { backend } = scripted()
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)

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
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
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

  it('closes a delivered panel without asking, since it holds no link', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      issueInvitation: (input) =>
        Promise.resolve(issuedFixture(input.email, 'sent')),
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'delivery@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    const emailed = await screen.findByText(
      fill(zhCN.invitations.issued.emailed, {
        email: 'delivery@example.com',
      }),
    )

    await user.click(
      screen.getByRole('button', {
        name: zhCN.invitations.issued.closeAria
          .replace('{{tenant}}', TENANT)
          .replace('{{email}}', 'delivery@example.com'),
      }),
    )
    expect(
      screen.queryByRole('dialog', {
        name: zhCN.invitations.issued.closeTitle,
      }),
    ).toBeNull()
    await waitFor(() => expect(emailed).not.toBeInTheDocument())
  })

  it.each(['failed', 'not_configured'] as const)(
    'renders the %s delivery outcome beside the one-time value',
    async (delivery) => {
      const user = userEvent.setup()
      const { backend } = scripted({
        issueInvitation: (input) =>
          Promise.resolve(issuedFixture(input.email, delivery)),
      })
      renderPage(backend)
      await screen.findAllByText(ADA.display_name)
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
      expect(
        screen.getByText(
          `${window.location.origin}/invitations/spd_inv_new_token`,
        ),
      ).toBeInTheDocument()
    },
  )

  it('says the invitation was emailed and offers no copy when the answer carries no token', async () => {
    const user = userEvent.setup()
    const { backend } = scripted({
      issueInvitation: (input) =>
        Promise.resolve(issuedFixture(input.email, 'sent')),
    })
    renderPage(backend)
    await screen.findAllByText(ADA.display_name)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'delivery@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    const emailed = await screen.findByText(
      fill(zhCN.invitations.issued.emailed, {
        email: 'delivery@example.com',
      }),
    )
    const panel = emailed.closest('[role="status"]') as HTMLElement
    expect(within(panel).queryByText('spd_inv_new_token')).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.invitations.issued.copyLink,
      }),
    ).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.invitations.issued.copyToken,
      }),
    ).toBeNull()
  })

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
      status: 'pending',
      after: 'inv-pending',
    })
  })
})

describe('the invitation issue control write attribution', () => {
  it('marks the issue control unavailable, not busy, while a row resend is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const { backend, recorder } = scripted({
      resendInvitation: () =>
        pending.promise.then(() => issuedFixture(PENDING_INVITATION.email)),
      listInvitations: () =>
        Promise.resolve({
          invitations: [PENDING_INVITATION],
          next_cursor: '',
        }),
    })
    renderPage(backend)
    await screen.findByText(PENDING_INVITATION.email)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )

    await chooseInvitationAction(
      user,
      PENDING_INVITATION.email,
      zhCN.invitations.resend,
    )
    await user.click(
      await screen.findByRole('button', { name: zhCN.common.confirm }),
    )

    const issue = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    expect(issue).toHaveAttribute('aria-disabled', 'true')
    expect(issue).not.toHaveAttribute('aria-busy')
    fireEvent.click(issue)
    issue.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await act(async () => {
      await Promise.resolve()
    })
    expect(recorder.issues).toEqual([])

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })
})
