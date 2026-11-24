import { afterEach, describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { waitForCondition } from '@speed/test-utils/wait'
import type { FeatureName } from '@speed/api-sdk/features'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { formatInstant } from '@speed/i18n'
import { InvitationsPage } from './InvitationsPage.js'
import { adminOperations } from './operations.js'
import type {
  AdminAccount,
  AdminIssueInvitationInput,
  AdminMember,
  AdminMemberPage,
  AdminOperations,
  AdminSetMemberSSOExemptInput,
  AdminWriteMemberInput,
} from './operations.js'
import type { AdminTenantRole } from './organization-operations.js'
import type { AuthnInviteInvitation } from '@speed/api-sdk'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { renderWithProviders, createAdminUiI18n } from '../test-utils/render.js'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { cellOf } from '../test-utils/table.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import {
  accountFixture,
  createAdminOperations,
  issuedInvitationFixture,
  makeAdminBackend,
  tenantFixture,
  type AdminBackend,
} from '../test-utils/operations.js'
import { AdminInvitationLinkProvider } from './internal/invitation-link.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { createStepUpSession } from '../test-utils/session.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

const SOURCE_LABELS = {
  admin: 'member-source-admin',
  initial: 'member-source-initial',
  invite: 'member-source-invite',
  jit: 'member-source-jit:{{provider}}',
} as const

function fill(
  template: string,
  values: Readonly<Record<string, string>>,
): string {
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replace(`{{${name}}}`, value),
    template,
  )
}

function byName(name: string): string {
  return fill(zhCN.common.accountReference.name, { name })
}

function byIdentifier(identifier: string): string {
  return fill(zhCN.common.accountReference.identifier, { identifier })
}

function byNameAndIdentifier(name: string, identifier: string): string {
  return fill(zhCN.common.accountReference.nameWithIdentifier, {
    name,
    identifier,
  })
}

function invitation(
  overrides: Partial<AuthnInviteInvitation> = {},
): AuthnInviteInvitation {
  return {
    id: 'inv-1',
    tenant: 'acme',
    email: 'ada@example.com',
    status: 'pending',
    state: 'pending',
    invited_by: 'staff-1',
    invited_by_name: 'Demo Operator',
    invited_by_username: 'operator',
    expires_at: '2099-01-01T00:00:00Z',
    accepted_at: null,
    revoked_at: null,
    last_sent_at: '2026-10-15T12:00:00Z',
    send_count: 1,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function member(overrides: Partial<AdminMember> = {}): AdminMember {
  return {
    account: 'acc-1',
    tenant: 'acme',
    active: true,
    display_name: '',
    username: 'ada',
    sso_exempt: false,
    source: 'direct',
    roles: [],
    joined_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function tenantRole(tenant: string, role: string): AdminTenantRole {
  return {
    builtin: false,
    can_manage: true,
    created_at: '2026-10-15T12:00:00Z',
    description: role,
    member_count: null,
    permission_count: 0,
    role,
    tenant,
  }
}

function account(overrides: Partial<AdminAccount> = {}): AdminAccount {
  return {
    id: 'acc-2',
    kind: 'user',
    status: 'active',
    display_name: 'Ada',
    mfa_required: false,
    protected: false,
    password_set: true,
    identifiers: [
      { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
    ],
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText(zhCN.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

async function chooseInvitationAction(
  user: ReturnType<typeof userEvent.setup>,
  email: string,
  action: string,
): Promise<HTMLElement> {
  const inviteSection = section(zhCN.invitations.section.title)
  const trigger = within(inviteSection).getByRole('button', {
    name: fill(zhCN.invitations.actionsAria, { email }),
  })
  await user.click(trigger)
  await user.click(await screen.findByRole('menuitem', { name: action }))
  return trigger
}

function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

function removeInRow(displayName: string): HTMLElement {
  const cell = screen.getByText(displayName)
  const row = cell.closest('tr')
  if (row === null) {
    throw new Error(`no member row for ${displayName}`)
  }
  return within(row as HTMLElement).getByRole('button', {
    name: zhCN.members.remove,
  })
}

/** jsdom implements no layout; a scroll observer installs this stub. */
function stubScrollIntoView(): ReturnType<typeof vi.fn> {
  const spy = vi.fn()
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    writable: true,
    value: spy,
  })
  return spy
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function withTenant(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listAccounts: () =>
        Promise.resolve({ accounts: [account()], next_cursor: '' }),
      ...overrides,
    }),
  )
}

async function expectRefreshInert(
  listInvitations: ReturnType<typeof vi.fn>,
  listMembers: ReturnType<typeof vi.fn>,
  invitationReads: number,
  memberReads: number,
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
  await waitForCondition(() => {
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(listInvitations).toHaveBeenCalledTimes(invitationReads)
  expect(listMembers).toHaveBeenCalledTimes(memberReads)
}

describe('InvitationsPage', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
  })

  it('prompts for a tenant before listing', () => {
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    expect(screen.getByText(zhCN.invitations.needTenant.title)).toBeInTheDocument()
  })

  it('does not read an empty tenant scope on refresh', async () => {
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [], next_cursor: '' }),
    )
    const listMembers = vi.fn(() => Promise.resolve([]))
    const operations = withTenant({ listInvitations, listMembers })
    renderWithProviders(<InvitationsPage />, { operations })

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      zhCN.invitations.needTenant.description,
    )
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(listInvitations).not.toHaveBeenCalled()
    expect(listMembers).not.toHaveBeenCalled()
  })

  it('disables the refresh while the invitation scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
    )
    const listMembers = vi.fn(() => Promise.resolve([member()]))
    const operations = withTenant({
      listInvitations,
      listMembers,
      issueInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    const invitationReads = listInvitations.mock.calls.length
    const memberReads = listMembers.mock.calls.length

    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await expectRefreshInert(
      listInvitations,
      listMembers,
      invitationReads,
      memberReads,
    )

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while the member scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
    )
    const listMembers = vi.fn(() =>
      Promise.resolve([
        member({ display_name: 'Member One', sso_exempt: false }),
      ]),
    )
    const operations = withTenant({
      listInvitations,
      listMembers,
      setMemberSsoExempt: () => pending.promise,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findAllByText('Member One')
    const invitationReads = listInvitations.mock.calls.length
    const memberReads = listMembers.mock.calls.length

    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    await expectRefreshInert(
      listInvitations,
      listMembers,
      invitationReads,
      memberReads,
    )

    await act(async () => {
      pending.resolve()
    })
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=acme')
    try {
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([member()]))
      const operations = withTenant({ listInvitations, listMembers })
      renderWithProviders(<InvitationsPage />, { operations })
      expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
      expect(screen.queryByText(zhCN.invitations.needTenant.title)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent('Acme')
      expect(listInvitations).toHaveBeenCalledWith(
        'acme',
        expect.objectContaining({ limit: 50, status: 'pending' }),
      )
      expect(listMembers).toHaveBeenCalledWith('acme', { limit: 100 })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('lists the tenant invitations and members', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
    const inviteTable = within(
      section(zhCN.invitations.section.title),
    ).getByRole('table')
    expect(
      within(inviteTable).getByText(zhCN.invitations.status.pending),
    ).toBeInTheDocument()
    const inviter = screen.getByText('Demo Operator')
    expect(inviter).toBeInTheDocument()
    expect(inviter).toHaveAttribute('title', 'Demo Operator (operator)')
    expect(screen.getByText('Member One')).toBeInTheDocument()
  })

  it('pins the invitation actions column when the table overflows', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([]),
    })
    const scrollWidth = vi
      .spyOn(Element.prototype, 'scrollWidth', 'get')
      .mockReturnValue(600)
    const clientWidth = vi
      .spyOn(Element.prototype, 'clientWidth', 'get')
      .mockReturnValue(300)
    try {
      renderWithProviders(<InvitationsPage />, { operations })
      await pickTenant(user)
      const inviteTable = within(
        section(zhCN.invitations.section.title),
      ).getByRole('table')
      const headers = inviteTable.querySelectorAll('thead th')
      expect(headers[headers.length - 1]).toHaveStyle({
        position: 'sticky',
        right: '0px',
      })
    } finally {
      scrollWidth.mockRestore()
      clientWidth.mockRestore()
    }
  })

  it('clears the chosen role when the tenant changes', async () => {
    const user = userEvent.setup()
    const globex = tenantFixture({
      name: 'globex',
      display_name: 'Globex',
      status: 'active',
    })
    const issueInvitation = vi.fn((input: AdminIssueInvitationInput) => {
      const issued = issuedInvitationFixture()
      return Promise.resolve({
        ...issued,
        invitation: {
          ...issued.invitation,
          tenant: input.tenant,
          email: input.email,
        },
      })
    })
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve([TENANT, globex]),
        listTenantRoles: (tenant) =>
          Promise.resolve([
            tenantRole(tenant, tenant === 'acme' ? 'admin' : 'viewer'),
          ]),
        listInvitations: () =>
          Promise.resolve({ invitations: [], next_cursor: '' }),
        listMembers: () => Promise.resolve([]),
        issueInvitation,
      }),
    )
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    await user.click(
      await screen.findByLabelText(zhCN.invitations.issue.roleLabel),
    )
    await user.click(await screen.findByRole('option', { name: 'admin' }))

    await user.click(screen.getByLabelText(zhCN.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))

    await waitForCondition(() => {
      expect(
        screen.getByLabelText(zhCN.invitations.issue.roleLabel),
      ).not.toHaveTextContent('admin')
    })
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    await waitForCondition(() => {
      expect(issueInvitation).toHaveBeenCalledTimes(1)
    })
    expect(issueInvitation.mock.calls[0]?.[0]).not.toHaveProperty('role')
  })

  it('drops a role chosen before the tenant stopped reporting role management', async () => {
    const user = userEvent.setup()
    let canManage = true
    const issueInvitation = vi.fn((input: AdminIssueInvitationInput) => {
      const issued = issuedInvitationFixture()
      return Promise.resolve({
        ...issued,
        invitation: {
          ...issued.invitation,
          tenant: input.tenant,
          email: input.email,
        },
      })
    })
    const operations = withTenant({
      listTenantRoles: (tenant) =>
        Promise.resolve([{ ...tenantRole(tenant, 'tenant-inviter'), can_manage: canManage }]),
      listInvitations: () => Promise.resolve({ invitations: [], next_cursor: '' }),
      issueInvitation,
    })
    const { queryClient } = renderWithProviders(<InvitationsPage />, {
      operations,
    })
    await pickTenant(user)
    await user.click(
      await screen.findByLabelText(zhCN.invitations.issue.roleLabel),
    )
    await user.click(
      await screen.findByRole('option', { name: 'tenant-inviter' }),
    )

    canManage = false
    await act(async () => {
      await queryClient.refetchQueries()
    })
    await waitForCondition(() => {
      expect(
        screen.queryByLabelText(zhCN.invitations.issue.roleLabel),
      ).toBeNull()
    })

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    await waitForCondition(() => {
      expect(issueInvitation).toHaveBeenCalledTimes(1)
    })
    expect(issueInvitation.mock.calls[0]?.[0]).not.toHaveProperty('role')
  })

  it('offers no role control while the tenant roles are still being read', async () => {
    const user = userEvent.setup()
    const listTenantRoles = vi.fn(() => new Promise<readonly AdminTenantRole[]>(() => {}))
    const operations = withTenant({
      listTenantRoles,
      listInvitations: () => Promise.resolve({ invitations: [], next_cursor: '' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await waitForCondition(() => expect(listTenantRoles).toHaveBeenCalledWith('acme'))
    expect(
      screen.queryByLabelText(zhCN.invitations.issue.roleLabel),
    ).toBeNull()
  })

  it('offers no role control when the tenant roles cannot be read', async () => {
    const user = userEvent.setup()
    const listTenantRoles = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listTenantRoles,
      listInvitations: () => Promise.resolve({ invitations: [], next_cursor: '' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await waitForCondition(() => expect(listTenantRoles).toHaveBeenCalledWith('acme'))
    await waitForCondition(() => {
      expect(
        screen.queryByLabelText(zhCN.invitations.issue.roleLabel),
      ).toBeNull()
    })
  })

  it('shows only the inviter display name and keeps the full identity on hover', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const named = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              invited_by_name: 'Staff One',
              invited_by_username: 'staff',
            }),
          ],
          next_cursor: '',
        }),
      getAccount,
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: named,
    })
    await pickTenant(user)
    const namedCell = (await screen.findByText('Staff One')).closest('td')
    expect(namedCell).not.toBeNull()
    expect(namedCell).not.toHaveTextContent('(staff)')
    expect(screen.getByText('Staff One')).toHaveAttribute(
      'title',
      'Staff One (staff)',
    )
    expect(within(namedCell as HTMLElement).queryAllByRole('button')).toHaveLength(
      0,
    )
    first.unmount()

    const unnamed = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              invited_by_name: undefined,
              invited_by_username: undefined,
            }),
          ],
          next_cursor: '',
        }),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations: unnamed })
    await pickTenant(user)
    const inviteSection = section(zhCN.invitations.section.title)
    const row = (await within(inviteSection).findByText('ada@example.com')).closest(
      'tr',
    )
    expect(row).not.toBeNull()
    const invitedByHeader = within(inviteSection).getByRole('columnheader', {
      name: zhCN.invitations.column.invitedBy,
    })
    const invitedByCell =
      row!.children[(invitedByHeader as HTMLTableCellElement).cellIndex]!
    expect(invitedByCell.textContent?.trim()).toBe('')
    expect(invitedByCell).not.toHaveAttribute('title')
    expect(
      within(inviteSection).queryByText(zhCN.common.accountUnavailable),
    ).toBeNull()
    expect(invitedByCell).not.toHaveTextContent('staff-1')
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('leaves the role cell blank when the invitation names no role', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [invitation({ role: undefined })],
          next_cursor: '',
        }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const inviteSection = section(zhCN.invitations.section.title)
    const row = (await within(inviteSection).findByText('ada@example.com')).closest(
      'tr',
    )
    expect(row).not.toBeNull()
    const roleHeader = within(inviteSection).getByRole('columnheader', {
      name: zhCN.invitations.column.role,
    })
    const roleCell =
      row!.children[(roleHeader as HTMLTableCellElement).cellIndex]!
    expect(roleCell.textContent?.trim()).toBe('')
    expect(
      within(inviteSection).queryByText(zhCN.invitations.issue.roleNone),
    ).toBeNull()
  })

  it('names the platform member by the member read, never a lookup or the account id', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Ada Lovelace' })]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const memberSection = section(zhCN.members.title)
    const nameCell = await within(memberSection).findByText('Ada Lovelace')
    const usernameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.username,
    })
    const row = nameCell.closest('tr')
    expect(row).not.toBeNull()
    const usernameCell =
      row!.children[(usernameHeader as HTMLTableCellElement).cellIndex]
    expect(usernameCell).toHaveTextContent('ada')
    expect(usernameCell).not.toHaveTextContent('Ada Lovelace (ada)')
    expect(usernameCell).not.toHaveTextContent('acc-1')
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('shows the neutral copy when the platform member has no username', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Ada Lovelace', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(within(memberSection).queryByText('acc-1')).toBeNull()
  })

  it('shows the member read display name and username, never the account id', async () => {
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Ada Lovelace' })]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText('Ada Lovelace'),
    ).toBeInTheDocument()
    expect(
      within(memberSection).getByRole('columnheader', {
        name: zhCN.members.column.displayName,
      }),
    ).toBeInTheDocument()
    expect(
      await within(memberSection).findByText('ada', { exact: true }),
    ).toBeInTheDocument()
    expect(within(memberSection).queryByText('acc-1')).toBeNull()
  })

  it('tenant mode names the account column by display name and username', async () => {
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({ account: 'acc-1', display_name: 'Ada Lovelace' }),
        ]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    const nameCells = await within(memberSection).findAllByText('Ada Lovelace')
    const displayNameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.displayName,
    })
    const accountHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.username,
    })
    const nameCell = nameCells.find(
      (cell) =>
        cell.closest('td')?.cellIndex ===
        (displayNameHeader as HTMLTableCellElement).cellIndex,
    )
    expect(nameCell).not.toBeUndefined()
    const row = nameCell!.closest('tr')
    expect(row).not.toBeNull()
    const accountCell = row!.children[
      (accountHeader as HTMLTableCellElement).cellIndex
    ]
    expect(accountCell).toHaveTextContent('ada')
    expect(accountCell).not.toHaveTextContent('acc-1')
  })

  it('pages the organization member table through every member', async () => {
    const user = userEvent.setup()
    const firstPage = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          params?.after === undefined
            ? { members: firstPage, next_cursor: 'cursor-1' }
            : {
                members: [
                  member({ account: 'acc-100', display_name: 'Last Member' }),
                ],
                next_cursor: '',
              },
        ),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      (await within(memberSection).findAllByText('Member 0')).length,
    ).toBeGreaterThan(0)
    expect(within(memberSection).queryByText('Last Member')).toBeNull()
    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Last Member'),
    ).toBeInTheDocument()
    expect(listMembers).toHaveBeenLastCalledWith('acme', {
      limit: 100,
      after: 'cursor-1',
    })
  })

  it('blocks a second member page change while the kept previous page is loading', async () => {
    const user = userEvent.setup()
    let resolveNext: (page: AdminMemberPage) => void = () => undefined
    const listMembers = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        params?.after === 'cursor-1'
          ? new Promise<AdminMemberPage>((resolve) => {
              resolveNext = resolve
            })
          : Promise.resolve({
              members: [member({ account: 'acc-0', display_name: 'Member 0' })],
              next_cursor: 'cursor-1',
            }),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText('Member 0'),
    ).toBeInTheDocument()
    const next = within(memberSection).getByRole('button', {
      name: zhCN.common.pager.next,
    })
    await user.click(next)
    // While the next page loads, `keepPreviousData` still holds the first
    // page's envelope, whose next_cursor names the same page. The activated
    // direction is busy for the read and refuses a second advance.
    await waitForCondition(() => {
      expect(next).toHaveAttribute('aria-busy', 'true')
      expect(next).toHaveAttribute('aria-disabled', 'true')
    })
    expect(listMembers).toHaveBeenCalledTimes(2)
    await user.click(next)
    expect(listMembers).toHaveBeenCalledTimes(2)

    await act(async () => {
      resolveNext({
        members: [member({ account: 'acc-1', display_name: 'Last Member' })],
        next_cursor: '',
      })
    })
    expect(
      await within(memberSection).findByText('Last Member'),
    ).toBeInTheDocument()
    expect(listMembers).toHaveBeenCalledTimes(2)
    expect(
      within(memberSection).getByText(
        zhCN.common.pager.page.replace('{{page}}', '2'),
      ),
    ).toBeInTheDocument()
  })

  it('treats an exact page as the last page without an empty next page', async () => {
    const page = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(() =>
      Promise.resolve({ members: page, next_cursor: '' }),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText('Member 0'),
    ).toBeInTheDocument()
    expect(
      within(memberSection).queryByRole('button', {
        name: zhCN.common.pager.next,
      }),
    ).toBeNull()
    expect(
      within(memberSection).queryByText(zhCN.members.empty.title),
    ).toBeNull()
    expect(listMembers).toHaveBeenCalledTimes(1)
  })

  it('returns to the first page with an empty cursor on the previous action', async () => {
    const user = userEvent.setup()
    const firstPage = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          params?.after === undefined
            ? { members: firstPage, next_cursor: 'cursor-1' }
            : {
                members: [
                  member({ account: 'acc-100', display_name: 'Member 100' }),
                ],
                next_cursor: '',
              },
        ),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    await within(memberSection).findByText('Member 0')
    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Member 100'),
    ).toBeInTheDocument()

    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.previous,
      }),
    )
    expect(
      await within(memberSection).findByText('Member 0'),
    ).toBeInTheDocument()
    await waitForCondition(() =>
      expect(listMembers).toHaveBeenLastCalledWith('acme', { limit: 100 }),
    )
  })

  it('starts a switched tenant with no member cursor', async () => {
    const user = userEvent.setup()
    const globex = tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' })
    const firstPage = Array.from({ length: 101 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(
      (
        tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          tenant === 'globex'
            ? { members: [], next_cursor: '' }
            : params?.after === undefined
              ? { members: firstPage, next_cursor: 'cursor-1' }
              : {
                  members: [
                    member({ account: 'acc-100', display_name: 'Member 100' }),
                  ],
                  next_cursor: '',
                },
        ),
    )
    const operations = withTenant({
      listTenants: () => Promise.resolve([TENANT, globex]),
      listMembers,
    })
    renderWithProviders(<InvitationsPage defaultTenant="acme" />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    await within(memberSection).findByText('Member 0')
    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Member 100'),
    ).toBeInTheDocument()

    await user.click(screen.getByLabelText(zhCN.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))

    await waitForCondition(() => {
      expect(
        listMembers.mock.calls.some(([tenant]) => tenant === 'globex'),
      ).toBe(true)
    })
    for (const [tenant, params] of listMembers.mock.calls) {
      if (tenant === 'globex') {
        expect(params).toEqual({ limit: 100 })
      }
    }
  })

  it('pages the invitation list through every invitation', async () => {
    const user = userEvent.setup()
    const listInvitations = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useInvitations']>[1],
      ) =>
        Promise.resolve(
          params?.after === 'invitations-cursor-2'
            ? {
                invitations: [
                  invitation({
                    id: 'inv-2',
                    email: 'grace@example.com',
                  }),
                ],
                next_cursor: '',
              }
            : {
                invitations: [invitation()],
                next_cursor: 'invitations-cursor-2',
              },
        ),
    )
    const operations = withTenant({ listInvitations })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const inviteSection = section(zhCN.invitations.section.title)
    await within(inviteSection).findByText('ada@example.com')
    await user.click(
      within(inviteSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(inviteSection).findByText('grace@example.com'),
    ).toBeInTheDocument()
    expect(listInvitations).toHaveBeenLastCalledWith(
      'acme',
      expect.objectContaining({ after: 'invitations-cursor-2' }),
    )
  })

  it('returns the invitation list to the first page with an empty cursor', async () => {
    const user = userEvent.setup()
    const listInvitations = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useInvitations']>[1],
      ) =>
        Promise.resolve(
          params?.after === 'invitations-cursor-2'
            ? {
                invitations: [
                  invitation({ id: 'inv-2', email: 'grace@example.com' }),
                ],
                next_cursor: '',
              }
            : {
                invitations: [invitation()],
                next_cursor: 'invitations-cursor-2',
              },
        ),
    )
    const operations = withTenant({ listInvitations })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const inviteSection = section(zhCN.invitations.section.title)
    await within(inviteSection).findByText('ada@example.com')
    await user.click(
      within(inviteSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(inviteSection).findByText('grace@example.com'),
    ).toBeInTheDocument()

    await user.click(
      within(inviteSection).getByRole('button', {
        name: zhCN.common.pager.previous,
      }),
    )
    expect(
      await within(inviteSection).findByText('ada@example.com'),
    ).toBeInTheDocument()
    await waitForCondition(() =>
      expect(listInvitations).toHaveBeenLastCalledWith('acme', {
        limit: 50,
        status: 'pending',
      }),
    )
  })

  it('starts a switched tenant with no invitation cursor', async () => {
    const user = userEvent.setup()
    const globex = tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' })
    const listInvitations = vi.fn(
      (
        tenant: string,
        params?: Parameters<AdminOperations['useInvitations']>[1],
      ) =>
        Promise.resolve(
          tenant === 'globex'
            ? { invitations: [], next_cursor: '' }
            : params?.after === 'invitations-cursor-2'
              ? {
                  invitations: [
                    invitation({ id: 'inv-2', email: 'grace@example.com' }),
                  ],
                  next_cursor: '',
                }
              : {
                  invitations: [invitation()],
                  next_cursor: 'invitations-cursor-2',
                },
        ),
    )
    const operations = withTenant({
      listTenants: () => Promise.resolve([TENANT, globex]),
      listInvitations,
    })
    renderWithProviders(<InvitationsPage defaultTenant="acme" />, {
      operations,
    })

    const inviteSection = section(zhCN.invitations.section.title)
    await within(inviteSection).findByText('ada@example.com')
    await user.click(
      within(inviteSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(inviteSection).findByText('grace@example.com'),
    ).toBeInTheDocument()

    await user.click(screen.getByLabelText(zhCN.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))

    await waitForCondition(() => {
      expect(
        listInvitations.mock.calls.some(([tenant]) => tenant === 'globex'),
      ).toBe(true)
    })
    for (const [tenant, params] of listInvitations.mock.calls) {
      if (tenant === 'globex') {
        expect(params).toEqual({ limit: 50, status: 'pending' })
      }
    }
  })

  it('filters the invitation list by status and returns to the first page', async () => {
    const user = userEvent.setup()
    const listInvitations = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useInvitations']>[1],
      ) => {
        if (params?.status === 'accepted') {
          return Promise.resolve({
            invitations: [
              invitation({
                id: 'inv-accepted',
                email: 'grace@example.com',
                status: 'accepted',
                state: 'accepted',
                accepted_at: '2026-11-01T12:00:00Z',
              }),
            ],
            next_cursor: '',
          })
        }
        return Promise.resolve(
          params?.after === 'cursor-2'
            ? {
                invitations: [
                  invitation({ id: 'inv-2', email: 'second@example.com' }),
                ],
                next_cursor: '',
              }
            : {
                invitations: [invitation()],
                next_cursor: 'cursor-2',
              },
        )
      },
    )
    const operations = withTenant({ listInvitations })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    await screen.findByText('ada@example.com')
    expect(listInvitations).toHaveBeenCalledWith(
      'acme',
      expect.objectContaining({ limit: 50, status: 'pending' }),
    )

    await user.click(
      within(section(zhCN.invitations.section.title)).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    await screen.findByText('second@example.com')

    await user.click(screen.getByLabelText(zhCN.invitations.filter.label))
    await user.click(
      await screen.findByRole('option', {
        name: zhCN.invitations.status.accepted,
      }),
    )
    expect(
      await screen.findByText('grace@example.com'),
    ).toBeInTheDocument()
    const filtered = listInvitations.mock.lastCall?.[1]
    expect(filtered).toMatchObject({ status: 'accepted' })
    expect(filtered).not.toHaveProperty('after')
  })

  it('leaves the name cell empty and names the username when the member has no display name', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: '', username: 'ada' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const memberSection = section(zhCN.members.title)
    const nameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.displayName,
    })
    const usernameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.username,
    })
    const row = memberSection.querySelector('tbody tr')
    expect(row).not.toBeNull()
    const nameCell = row!.children[
      (nameHeader as HTMLTableCellElement).cellIndex
    ]
    expect(nameCell!.textContent).toBe('')
    const usernameCell =
      row!.children[(usernameHeader as HTMLTableCellElement).cellIndex]
    expect(usernameCell).toHaveTextContent('ada')
    expect(usernameCell).not.toHaveTextContent('acc-1')
  })

  it('tenant mode drops the platform account picker and renders the invitation inviter names', async () => {
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              invited_by_name: 'Ada Lovelace',
              invited_by_username: 'ada',
            }),
          ],
          next_cursor: '',
        }),
      listMembers: () =>
        Promise.resolve([
          member({ account: 'acc-1', display_name: 'Ada Lovelace' }),
        ]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      (await within(memberSection).findAllByText('Ada Lovelace')).length,
    ).toBeGreaterThan(0)
    const invitationSection = section(zhCN.invitations.section.title)
    const inviter = await within(invitationSection).findByText('Ada Lovelace')
    expect(inviter).toHaveAttribute('title', 'Ada Lovelace (ada)')
    expect(screen.queryByLabelText(zhCN.members.write.accountLabel)).toBeNull()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('tenant mode leaves the inviter cell blank when the invitation names no inviter', async () => {
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              invited_by: 'ghost-1',
              invited_by_name: undefined,
              invited_by_username: undefined,
            }),
          ],
          next_cursor: '',
        }),
      listMembers: () =>
        Promise.resolve([member({ account: 'acc-1', display_name: 'Ada' })]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const invitationSection = section(zhCN.invitations.section.title)
    const row = (await within(invitationSection).findByText('ada@example.com')).closest(
      'tr',
    )
    expect(row).not.toBeNull()
    const invitedByHeader = within(invitationSection).getByRole(
      'columnheader',
      { name: zhCN.invitations.column.invitedBy },
    )
    const invitedByCell =
      row!.children[(invitedByHeader as HTMLTableCellElement).cellIndex]!
    expect(invitedByCell.textContent?.trim()).toBe('')
    expect(
      within(invitationSection).queryByText(zhCN.common.accountUnavailable),
    ).toBeNull()
    expect(within(invitationSection).queryByText('ghost-1')).toBeNull()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it("renders each row's inviter from its own invitation record", async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              id: 'inv-1',
              invited_by_name: 'Staff One',
              invited_by_username: 'staff-one',
            }),
            invitation({
              id: 'inv-2',
              email: 'grace@example.com',
              invited_by_name: 'Staff Two',
              invited_by_username: 'staff-two',
            }),
          ],
          next_cursor: '',
        }),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    const first = await screen.findByText('Staff One')
    const second = screen.getByText('Staff Two')
    expect(first).toHaveAttribute('title', 'Staff One (staff-one)')
    expect(second).toHaveAttribute('title', 'Staff Two (staff-two)')
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('shows an inviter that has a display name but no username', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              invited_by_name: 'Staff One',
              invited_by_username: undefined,
            }),
          ],
          next_cursor: '',
        }),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    const inviter = await screen.findByText('Staff One')
    expect(inviter).toHaveAttribute('title', 'Staff One')
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('declares the designed priorities and caps for invitations and members', async () => {
    const user = userEvent.setup()
    const email = 'very-long-address-for-a-person@example.com'
    const invitedByName = 'A veritable paragon of administrative nomenclature'
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              email,
              invited_by_name: invitedByName,
              invited_by_username: 'operator',
            }),
          ],
          next_cursor: '',
        }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText(email)

    const header = (scope: HTMLElement, label: string): Element =>
      within(scope).getByRole('columnheader', { name: label })

    const inviteSection = section(zhCN.invitations.section.title)
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.email))).toBeNull()
    for (const label of [
      zhCN.invitations.column.status,
      zhCN.invitations.column.invitedBy,
      zhCN.invitations.column.expires,
      zhCN.invitations.column.created,
    ]) {
      expect(columnVisibility(header(inviteSection, label))).toEqual({
        hiddenAt: 0,
        visibleFrom: 600,
      })
    }
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.role))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.actions))).toBeNull()
    const emailCell = screen.getByText(email).closest('td')
    expect(emailCell).toHaveAttribute('title', email)
    expect(containerMaxWidthOf(emailCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 120],
        [900, 200],
      ]),
    )
    const invitedByIdentity =
      await within(inviteSection).findByText(invitedByName)
    const invitedByCell = invitedByIdentity.closest('td')
    expect(invitedByCell).not.toBeNull()
    expect(invitedByIdentity).toHaveAttribute(
      'title',
      `${invitedByName} (operator)`,
    )
    expect(
      within(invitedByCell as HTMLElement).queryAllByRole('button'),
    ).toHaveLength(0)
    expect(
      within(invitedByCell as HTMLElement).queryByText('staff-1'),
    ).toBeNull()
    const invitedByWrapper = invitedByCell!.firstElementChild as HTMLElement
    expect(containerMaxWidthOf(invitedByWrapper)).toEqual(
      new Map([
        [0, 120],
        [900, 160],
      ]),
    )

    const memberSection = section(zhCN.members.title)
    expect(columnVisibility(header(memberSection, zhCN.members.column.displayName))).toBeNull()
    expect(columnVisibility(header(memberSection, zhCN.members.column.username))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.active))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.source))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.ssoExempt))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.actions))).toBeNull()
    const memberIdentity = await within(memberSection).findByText('Operator')
    expect(containerMaxWidthOf(memberIdentity.closest('td')!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    const gated = [
      screen.getByRole('button', { name: zhCN.common.refresh }),
      screen.getByRole('button', {
        name: fill(zhCN.invitations.actionsAria, { email: 'ada@example.com' }),
      }),
      screen.getByRole('button', { name: zhCN.members.ssoExemptSet }),
      screen.getByRole('button', { name: zhCN.members.remove }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('issues an invitation through the bundle', async () => {
    const user = userEvent.setup()
    const issued: string[] = []
    const operations = withTenant({
      issueInvitation: (input) => {
        issued.push(input.email)
        return Promise.resolve(issuedInvitationFixture())
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await waitForCondition(() => {
      expect(issued).toEqual(['new@example.com'])
    })
  })

  it('offers fixed invitation lifetimes and sends the chosen one', async () => {
    const user = userEvent.setup()
    const expiresSent: (string | undefined)[] = []
    const operations = withTenant({
      issueInvitation: (input) => {
        expiresSent.push(input.expiresAt)
        return Promise.resolve(issuedInvitationFixture())
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    const inviteSection = section(zhCN.invitations.section.title)
    expect(
      inviteSection.querySelector('input[type="datetime-local"]'),
    ).toBeNull()
    const lifetime = screen.getByLabelText(zhCN.invitations.issue.quickLabel)
    await user.click(lifetime)
    await user.click(
      await screen.findByRole('option', {
        name: zhCN.invitations.issue.oneDay,
      }),
    )
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    const before = Date.now()
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    await waitForCondition(() => {
      expect(expiresSent).toHaveLength(1)
    })
    const sent = expiresSent[0]
    expect(sent).toBeDefined()
    const left = Date.parse(sent as string) - before
    expect(left).toBeGreaterThan(23 * 60 * 60 * 1000)
    expect(left).toBeLessThan(25 * 60 * 60 * 1000)
  })

  it('sends the thirty-day lifetime when it is chosen', async () => {
    const frozen = new Date('2026-03-01T00:00:00Z').getTime()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })
      const expiresSent: (string | undefined)[] = []
      const operations = withTenant({
        issueInvitation: (input) => {
          expiresSent.push(input.expiresAt)
          return Promise.resolve(issuedInvitationFixture())
        },
      })
      renderWithProviders(<InvitationsPage />, { operations })
      await pickTenant(user)
      await user.click(
        screen.getByLabelText(zhCN.invitations.issue.quickLabel),
      )
      await user.click(
        await screen.findByRole('option', {
          name: zhCN.invitations.issue.thirtyDays,
        }),
      )
      await user.type(
        screen.getByLabelText(zhCN.invitations.issue.emailLabel),
        'new@example.com',
      )
      vi.setSystemTime(frozen)
      fireEvent.click(
        screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
      )
      await waitForCondition(() => {
        expect(expiresSent).toHaveLength(1)
      })
      expect(expiresSent[0]).toBe(
        new Date(frozen + 30 * 24 * 60 * 60 * 1000).toISOString(),
      )
    } finally {
      await restoreRealTimers()
    }
  })

  it('issues an invitation with the default lifetime when none is chosen', async () => {
    const frozen = new Date('2026-03-01T00:00:00Z').getTime()
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })
      const expiresSent: (string | undefined)[] = []
      const operations = withTenant({
        issueInvitation: (input) => {
          expiresSent.push(input.expiresAt)
          return Promise.resolve(issuedInvitationFixture())
        },
      })
      renderWithProviders(<InvitationsPage />, { operations })
      await pickTenant(user)
      await user.type(
        screen.getByLabelText(zhCN.invitations.issue.emailLabel),
        'new@example.com',
      )
      vi.setSystemTime(frozen)
      fireEvent.click(
        screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
      )
      await waitForCondition(() => {
        expect(expiresSent).toHaveLength(1)
      })
      expect(expiresSent[0]).toBe(
        new Date(frozen + 7 * 24 * 60 * 60 * 1000).toISOString(),
      )
    } finally {
      await restoreRealTimers()
    }
  })

  it('issues an invitation with the role chosen for the target tenant', async () => {
    const user = userEvent.setup()
    const issued: { email: string; role?: string }[] = []
    const operations = withTenant({
      listTenantRoles: () =>
        Promise.resolve([
          {
            role: 'tenant-inviter',
            tenant: 'acme',
            description: '',
            builtin: false,
            can_manage: true,
            member_count: null,
            permission_count: 0,
            created_at: '2026-10-15T12:00:00Z',
          },
        ]),
      issueInvitation: (input) => {
        issued.push({ email: input.email, ...(input.role === undefined ? {} : { role: input.role }) })
        return Promise.resolve(issuedInvitationFixture())
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    await user.click(
      await screen.findByLabelText(zhCN.invitations.issue.roleLabel),
    )
    await user.click(
      await screen.findByRole('option', { name: 'tenant-inviter' }),
    )
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    await waitForCondition(() => {
      expect(issued).toEqual([
        { email: 'new@example.com', role: 'tenant-inviter' },
      ])
    })
  })

  it('offers no role control when the target tenant reports no role management', async () => {
    const user = userEvent.setup()
    renderWithProviders(<InvitationsPage />, {
      operations: withTenant({
        listInvitations: () =>
          Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
        listTenantRoles: () =>
          Promise.resolve([
            {
              role: 'tenant-inviter',
              tenant: 'acme',
              description: '',
              builtin: false,
              can_manage: false,
              member_count: null,
              permission_count: 0,
              created_at: '2026-10-15T12:00:00Z',
            },
          ]),
      }),
    })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    expect(
      screen.queryByLabelText(zhCN.invitations.issue.roleLabel),
    ).toBeNull()
  })

  it('shows the acceptance link for the invitation just issued', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            invitation: {
              ...issuedInvitationFixture().invitation,
              email: 'new@example.com',
            },
          }),
        ),
    })
    renderWithProviders(
      <AdminInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </AdminInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(
      await screen.findByText(
        fill(zhCN.invitations.issued.link, { email: 'new@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        `${window.location.origin}/invitations/spd_inv_test_token`,
      ),
    ).toBeInTheDocument()
  })

  it('copies the complete acceptance link through an accessible control', async () => {
    const user = userEvent.setup()
    // user-event installs its own clipboard stub at setup, so the spy
    // replaces it afterwards.
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            invitation: {
              ...issuedInvitationFixture().invitation,
              email: 'new@example.com',
            },
          }),
        ),
    })
    renderWithProviders(
      <AdminInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </AdminInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    const link = `${window.location.origin}/invitations/spd_inv_test_token`
    await screen.findByText(link)
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issued.copyLink }))
    await waitForCondition(() => {
      expect(writeText).toHaveBeenCalledWith(link)
    })
  })

  it('shows the bare token when the host supplies no link builder', async () => {    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(
      await screen.findByText(fill(zhCN.invitations.issued.token, { email: 'ada@example.com' })),
    ).toBeInTheDocument()
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
  })

  it.each([
    ['failed', zhCN.invitations.issued.delivery.failed],
    ['not_configured', zhCN.invitations.issued.delivery.not_configured],
  ] as const)(
    'keeps the copy control for the %s delivery outcome',
    async (delivery, expected) => {
      const user = userEvent.setup()
      const operations = withTenant({
        issueInvitation: () =>
          Promise.resolve(issuedInvitationFixture({ email_delivery: delivery })),
      })
      renderWithProviders(<InvitationsPage />, { operations })
      await pickTenant(user)
      await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
      await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
      expect(await screen.findByText(expected)).toBeInTheDocument()
      expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
      expect(
        screen.getByRole('button', {
          name: zhCN.invitations.issued.copyToken,
        }),
      ).toBeInTheDocument()
    },
  )

  it('says the invitation was emailed and offers no copy when the answer carries no token', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(issuedInvitationFixture({ email_delivery: 'sent' })),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    const emailed = await screen.findByText(
      fill(zhCN.invitations.issued.emailed, { email: 'ada@example.com' }),
    )
    const panel = emailed.closest('[role="status"]') as HTMLElement
    expect(within(panel).queryByText('spd_inv_test_token')).toBeNull()
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

  it('keeps the copy control for a sent answer that still carries a token', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            email_delivery: 'sent',
            token: 'spd_inv_override_token',
          }),
        ),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(await screen.findByText('spd_inv_override_token')).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: zhCN.invitations.issued.copyToken,
      }),
    ).toBeInTheDocument()
  })

  it('shows the acceptance value in a named live region and asks before dropping it', async () => {
    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))

    const display = await screen.findByText(
      fill(zhCN.invitations.issued.token, { email: 'ada@example.com' }),
    )
    expect(display.closest('[role="status"]')).not.toBeNull()
    const close = screen.getByRole('button', {
      name: fill(zhCN.invitations.issued.closeAria, {
        tenant: 'acme',
        email: 'ada@example.com',
      }),
    })
    await user.click(close)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.invitations.issued.keepOpen,
      }),
    )
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()

    await user.click(close)
    const confirming = await screen.findByRole('dialog')
    await user.click(
      within(confirming).getByRole('button', {
        name: zhCN.invitations.issued.close,
      }),
    )
    expect(screen.queryByText('spd_inv_test_token')).toBeNull()
    expect(screen.getByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }))).toBeInTheDocument()
  })

  it('closes a delivered panel directly, since it holds no link', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(issuedInvitationFixture({ email_delivery: 'sent' })),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )

    const emailed = await screen.findByText(
      fill(zhCN.invitations.issued.emailed, { email: 'ada@example.com' }),
    )
    const panel = emailed.closest('[role="status"]') as HTMLElement
    const close = within(panel).getByRole('button', {
      name: fill(zhCN.invitations.issued.closeAria, {
        tenant: 'acme',
        email: 'ada@example.com',
      }),
    })
    await user.click(close)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      screen.queryByText(
        fill(zhCN.invitations.issued.emailed, { email: 'ada@example.com' }),
      ),
    ).toBeNull()
  })

  it('keeps the acceptance value through a reset of the creating scope', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const operations = withTenant({
      issueInvitation: () => {
        attempts += 1
        return attempts === 1
          ? Promise.resolve(issuedInvitationFixture())
          : Promise.reject({ status: 403, code: 'authn.forbidden' })
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await screen.findByText('spd_inv_test_token')

    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
    expect(screen.queryByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }))).toBeNull()
  })

  it('revokes a pending invitation through the bundle', async () => {
    const user = userEvent.setup()
    const revoked: string[] = []
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      revokeInvitation: (input) => {
        revoked.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.revoke,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() => {
      expect(revoked).toEqual(['inv-1'])
    })
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    const invitationSection = section(zhCN.invitations.section.title)
    const heading = within(invitationSection).getByRole('heading', {
      name: zhCN.invitations.section.title,
    })
    await waitForCondition(() => expect(document.activeElement).toBe(heading))
    expect(heading).toHaveAttribute('tabindex', '-1')
    expect(
      within(invitationSection)
        .getByText(fill(zhCN.invitations.revoked, { email: 'ada@example.com' }))
        .closest('[role="status"]'),
    ).not.toBeNull()
  })

  it('re-reads the list when a revoke answers invitation-terminal', async () => {
    const user = userEvent.setup()
    let terminal = false
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/system/tenants') {
        return { tenants: [TENANT] }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/invitations'
      ) {
        return {
          invitations: [
            terminal
              ? invitation({
                  state: 'expired',
                  expires_at: '2026-02-01T00:00:00Z',
                })
              : invitation(),
          ],
          next_cursor: '',
        }
      }
      if (call.method === 'GET' && call.path === '/api/v1/tenants/acme/members') {
        return { members: [], next_cursor: '' }
      }
      if (call.method === 'GET' && call.path === '/api/v1/tenants/acme/roles') {
        return { roles: [] }
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/tenants/acme/invitations/inv-1'
      ) {
        terminal = true
        throw { status: 409, code: 'authn.invitation_terminal' }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(<InvitationsPage />, { operations: adminOperations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await user.click(screen.getByLabelText(zhCN.invitations.filter.label))
    await user.click(
      await screen.findByRole('option', { name: zhCN.invitations.filter.all }),
    )
    await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.revoke,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() =>
      expect(
        within(section(zhCN.invitations.section.title)).getByText(
          zhCN.invitations.status.expired,
        ),
      ).toBeInTheDocument(),
    )
  })

  it('moves focus to the invitation heading when a terminal revoke empties the filter', async () => {
    const user = userEvent.setup()
    let terminal = false
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/system/tenants') {
        return { tenants: [TENANT] }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/invitations'
      ) {
        if (terminal && call.query?.status === 'pending') {
          return { invitations: [], next_cursor: '' }
        }
        return {
          invitations: [
            terminal
              ? invitation({
                  state: 'expired',
                  expires_at: '2026-02-01T00:00:00Z',
                })
              : invitation(),
          ],
          next_cursor: '',
        }
      }
      if (call.method === 'GET' && call.path === '/api/v1/tenants/acme/members') {
        return { members: [], next_cursor: '' }
      }
      if (call.method === 'GET' && call.path === '/api/v1/tenants/acme/roles') {
        return { roles: [] }
      }
      if (
        call.method === 'DELETE' &&
        call.path === '/api/v1/tenants/acme/invitations/inv-1'
      ) {
        terminal = true
        throw { status: 409, code: 'authn.invitation_terminal' }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(<InvitationsPage />, { operations: adminOperations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.revoke,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    const heading = within(section(zhCN.invitations.section.title)).getByRole(
      'heading',
      { name: zhCN.invitations.section.title },
    )
    await waitForCondition(() => expect(screen.queryByText('ada@example.com')).toBeNull())
    await waitForCondition(() => expect(document.activeElement).toBe(heading))
    expect(heading).toHaveAttribute('tabindex', '-1')
  })

  it('resends a pending invitation on the same row and hands out the new link', async () => {
    const user = userEvent.setup()
    const resent: string[] = []
    const sentAt = '2026-11-10T09:00:00Z'
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            resent.length === 0
              ? invitation()
              : invitation({ send_count: 2, last_sent_at: sentAt }),
          ],
          next_cursor: '',
        }),
      resendInvitation: (input) => {
        resent.push(`${input.tenant}:${input.id}`)
        return Promise.resolve(
          issuedInvitationFixture({
            invitation: {
              ...issuedInvitationFixture().invitation,
              send_count: 2,
              last_sent_at: sentAt,
            },
          }),
        )
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')

    await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.resend,
    )
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toHaveTextContent(
      fill(zhCN.invitations.resendMessage, {
        email: 'ada@example.com',
        time: formatInstant('2099-01-01T00:00:00Z', 'zh-CN', {
          timeZone: TEST_TIME_ZONE,
        }),
      }),
    )
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitForCondition(() => {
      expect(resent).toEqual(['acme:inv-1'])
    })
    expect(
      await screen.findByText('spd_inv_test_token'),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.invitations.resent, { email: 'ada@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(
        fill(zhCN.invitations.created.resentOnce, {
          time: formatInstant(sentAt, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
        }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        formatInstant('2026-10-15T12:00:00Z', 'zh-CN', {
          timeZone: TEST_TIME_ZONE,
        }),
      ),
    ).toBeInTheDocument()
  })

  it('re-reads the list when a resend answers invitation-terminal', async () => {
    const user = userEvent.setup()
    let terminal = false
    bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/system/tenants') {
        return { tenants: [TENANT] }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/invitations'
      ) {
        return {
          invitations: [
            terminal
              ? invitation({
                  state: 'expired',
                  expires_at: '2026-02-01T00:00:00Z',
                })
              : invitation(),
          ],
          next_cursor: '',
        }
      }
      if (call.method === 'GET' && call.path === '/api/v1/tenants/acme/members') {
        return { members: [], next_cursor: '' }
      }
      if (call.method === 'GET' && call.path === '/api/v1/tenants/acme/roles') {
        return { roles: [] }
      }
      if (
        call.method === 'POST' &&
        call.path === '/api/v1/tenants/acme/invitations/inv-1/resend'
      ) {
        terminal = true
        throw { status: 409, code: 'authn.invitation_terminal' }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(<InvitationsPage />, { operations: adminOperations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await user.click(screen.getByLabelText(zhCN.invitations.filter.label))
    await user.click(
      await screen.findByRole('option', { name: zhCN.invitations.filter.all }),
    )
    await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.resend,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() =>
      expect(
        within(section(zhCN.invitations.section.title)).getByText(
          zhCN.invitations.status.expired,
        ),
      ).toBeInTheDocument(),
    )
  })

  it('shows the role, the sent count and the near-expiry remainder', async () => {
    const user = userEvent.setup()
    const lastSentAt = '2026-11-10T09:00:00Z'
    const expiresAt = new Date(Date.now() + 2 * 60 * 60 * 1000).toISOString()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              role: 'tenant-inviter',
              send_count: 3,
              last_sent_at: lastSentAt,
              expires_at: expiresAt,
            }),
          ],
          next_cursor: '',
        }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')

    expect(screen.getByText('tenant-inviter')).toBeInTheDocument()
    expect(
      screen.getByText(
        formatInstant('2026-10-15T12:00:00Z', 'zh-CN', {
          timeZone: TEST_TIME_ZONE,
        }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.invitations.created.resent, {
          count: '2',
          time: formatInstant(lastSentAt, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
        }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.invitations.expires.remainingHours, { hours: '1' }),
      ),
    ).toBeInTheDocument()
  })

  it('shows terminal moments under the status and offers no actions', async () => {
    const user = userEvent.setup()
    const acceptedAt = '2026-11-05T10:00:00Z'
    const revokedAt = '2026-11-06T10:00:00Z'
    const expiredAt = '2026-01-01T00:00:00Z'
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({
              id: 'inv-accepted',
              email: 'accepted@example.com',
              status: 'accepted',
              state: 'accepted',
              expires_at: '2026-11-09T10:00:00Z',
              accepted_at: acceptedAt,
            }),
            invitation({
              id: 'inv-revoked',
              email: 'revoked@example.com',
              status: 'revoked',
              state: 'revoked',
              expires_at: '2026-11-09T10:00:00Z',
              revoked_at: revokedAt,
            }),
            invitation({
              id: 'inv-expired',
              email: 'expired@example.com',
              status: 'pending',
              state: 'expired',
              expires_at: expiredAt,
            }),
          ],
          next_cursor: '',
        }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('accepted@example.com')

    expect(
      screen.getByText(
        fill(zhCN.invitations.statusSince.accepted, {
          time: formatInstant(acceptedAt, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
        }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.invitations.statusSince.revoked, {
          time: formatInstant(revokedAt, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
        }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.invitations.statusSince.expired, {
          time: formatInstant(expiredAt, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
        }),
      ),
    ).toBeInTheDocument()
    const inviteSection = section(zhCN.invitations.section.title)
    for (const email of [
      'accepted@example.com',
      'revoked@example.com',
      'expired@example.com',
    ]) {
      expect(
        within(inviteSection).queryByRole('button', {
          name: fill(zhCN.invitations.actionsAria, { email }),
        }),
      ).toBeNull()
      const row = within(inviteSection)
        .getByText(email)
        .closest('tr') as HTMLElement
      expect(
        cellOf(row, zhCN.invitations.column.expires).textContent?.trim(),
      ).toBe('')
    }
  })

  it('removes a membership through the bundle', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      removeMember: (input) => {
        removed.push(input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() => {
      expect(removed).toEqual(['acc-1'])
    })
  })

  it('names the removed member by the member read and never the recorded id', async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    let dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(
      await screen.findByText(
        fill(zhCN.members.removed, {
          account: byNameAndIdentifier('Member One', 'ada'),
        }),
      ),
    ).toBeInTheDocument()
    first.unmount()

    const unnamed = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: '', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations: unnamed })
    await pickTenant(user)
    await screen.findByText(zhCN.common.accountUnavailable)
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    const notice = await screen.findByText(zhCN.members.removedUnnamed)
    expect(notice).not.toHaveTextContent('acc-1')
  })

  const MEMBER_UUID = '01a170b2-7c4d-7e2f-9a11-6b0c2f9e4d31'

  async function openRemovalConfirmation(
    user: ReturnType<typeof userEvent.setup>,
    options: {
      readonly member?: Partial<AdminMember>
      readonly hasFeature?: (name: FeatureName) => boolean
    } = {},
  ): Promise<HTMLElement> {
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({
            account: MEMBER_UUID,
            display_name: 'Ada Lovelace',
            username: 'ada',
            ...options.member,
          }),
        ]),
    })
    renderWithProviders(<InvitationsPage />, {
      operations,
      ...(options.hasFeature === undefined
        ? {}
        : { hasFeature: options.hasFeature }),
    })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: zhCN.members.remove }),
    )
    return screen.findByRole('dialog', { name: zhCN.members.removeTitle })
  }

  function expectNeutralRemovalConfirmation(dialog: HTMLElement): void {
    expect(
      within(dialog).getByText(zhCN.members.removeMessageUnnamed),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  }

  it('names the member being removed by its display name and login username', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user)
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessageWithIdentifier, {
          name: 'Ada Lovelace',
          identifier: 'ada',
        }),
      ),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  })

  it('names the member being removed by its display name when there is no username', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user, {
      member: { username: '' },
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessage, { name: 'Ada Lovelace' }),
      ),
    ).toBeInTheDocument()
  })

  it('names the member being removed by its username when the display name is empty', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user, {
      member: { display_name: '' },
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessageByIdentifier, { identifier: 'ada' }),
      ),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  })

  it('keeps the member UUID out of the removal confirmation', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user)
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  })

  it('uses the neutral sentence when the member has neither display name nor username', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user, {
      member: { display_name: '', username: '' },
    })
    expectNeutralRemovalConfirmation(dialog)
  })

  it('names the member from the member read without the authn.admin feature', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() =>
      Promise.reject(new Error('the member row must not issue a read')),
    )
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({
            account: MEMBER_UUID,
            display_name: 'Ada Lovelace',
            username: 'ada',
          }),
        ]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin',
    })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: zhCN.members.remove }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.members.removeTitle,
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessageWithIdentifier, {
          name: 'Ada Lovelace',
          identifier: 'ada',
        }),
      ),
    ).toBeInTheDocument()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('writes the chosen account id, not the typed name', async () => {
    const user = userEvent.setup()
    const written: AdminWriteMemberInput[] = []
    const operations = withTenant({
      writeMember: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(
      screen.getByRole('button', { name: zhCN.members.write.submit }),
    ).toHaveAttribute('aria-disabled', 'true')
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    await waitForCondition(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true, sso_exempt: false },
      ])
    })
    expect(screen.getByLabelText(zhCN.members.write.accountLabel)).toHaveValue('')
  })

  const MEMBER_ACTIONS = [
    {
      action: 'remove',
      named: zhCN.members.removed,
      unnamed: zhCN.members.removedUnnamed,
    },
    {
      action: 'sso-saved',
      named: zhCN.members.ssoExemptSaved,
      unnamed: zhCN.members.ssoExemptSavedUnnamed,
    },
    {
      action: 'sso-cleared',
      named: zhCN.members.ssoExemptCleared,
      unnamed: zhCN.members.ssoExemptClearedUnnamed,
    },
    {
      action: 'write',
      named: zhCN.members.write.saved,
      unnamed: zhCN.members.write.savedUnnamed,
    },
  ] as const

  const NOTICE_IDENTIFIERS: AdminAccount['identifiers'] = [
    { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
  ]

  async function driveMemberNotice(
    user: ReturnType<typeof userEvent.setup>,
    action: (typeof MEMBER_ACTIONS)[number]['action'],
    displayName: string,
    identifiers: AdminAccount['identifiers'],
    expected: string,
  ): Promise<HTMLElement> {
    const username =
      identifiers.find((identifier) => identifier.kind === 'username')?.value ??
      ''
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({
            sso_exempt: action === 'sso-cleared',
            display_name: displayName,
            username,
          }),
        ]),
      listAccounts: () =>
        Promise.resolve({
          accounts: [account({ display_name: displayName, identifiers })],
          next_cursor: '',
        }),
      removeMember: () => Promise.resolve(),
      setMemberSsoExempt: () => Promise.resolve(),
      writeMember: () => Promise.resolve(),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    if (action === 'write') {
      const optionName =
        displayName === '' && identifiers.length === 0
          ? zhCN.common.accountUnavailable
          : displayName === ''
            ? 'ada'
            : `${displayName} (ada)`
      await chooseAccount(
        user,
        zhCN.members.write.accountLabel,
        'Ada',
        optionName,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.members.write.submit }),
      )
    } else {
      await screen.findByRole('button', { name: zhCN.members.remove })
      if (action === 'remove') {
        await user.click(
          screen.getByRole('button', { name: zhCN.members.remove }),
        )
        const dialog = await screen.findByRole('dialog')
        await user.click(
          within(dialog).getByRole('button', { name: zhCN.common.confirm }),
        )
      } else {
        await user.click(
          screen.getByRole('button', {
            name:
              action === 'sso-saved'
                ? zhCN.members.ssoExemptSet
                : zhCN.members.ssoExemptClear,
          }),
        )
      }
    }

    return screen.findByText(expected)
  }

  describe.each(MEMBER_ACTIONS)('members notice: $action', ({ action, named, unnamed }) => {
    it('names the account by display name and first identifier, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveMemberNotice(
        user,
        action,
        'Ada',
        NOTICE_IDENTIFIERS,
        fill(named, { account: byNameAndIdentifier('Ada', 'ada') }),
      )
      expect(notice).toBeInTheDocument()
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })

    it('names the account by its first identifier when the display name is empty, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveMemberNotice(
        user,
        action,
        '',
        NOTICE_IDENTIFIERS,
        fill(named, { account: byIdentifier('ada') }),
      )
      expect(notice).toBeInTheDocument()
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })

    it('uses the neutral sentence when neither resolves, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveMemberNotice(user, action, '', [], unnamed)
      expect(notice).toBeInTheDocument()
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })
  })

  it('re-reads the member list after a landed write and leaves it alone after a refusal', async () => {
    const user = userEvent.setup()
    let memberRows = 0
    let writeFails = false
    const calls = bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/system/tenants') {
        return { tenants: [TENANT] }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/invitations'
      ) {
        return { invitations: [], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/members'
      ) {
        memberRows += 1
        return {
          members:
            memberRows > 1
              ? [member({ account: 'acc-2', display_name: 'Ada', username: 'ada' })]
              : [],
          next_cursor: '',
        }
      }
      if (call.method === 'GET' && call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [account()], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/system/auth/accounts/acc-2'
      ) {
        return { account: account() }
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/tenants/acme/members/acc-2'
      ) {
        if (writeFails) {
          throw { status: 403, code: 'client.http.403' }
        }
        return undefined
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(<InvitationsPage />, { operations: adminOperations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.members.empty.title)).toBeInTheDocument()
    const listReads = (): number =>
      calls.filter(
        (entry) =>
          entry.method === 'GET' &&
          entry.path === '/api/v1/tenants/acme/members',
      ).length

    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    expect(
      await screen.findByText(
        fill(zhCN.members.write.saved, {
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      ),
    ).toBeInTheDocument()
    const memberSection = section(zhCN.members.title)
    expect(await within(memberSection).findByText('Ada')).toBeInTheDocument()
    expect(within(memberSection).getByText('ada')).toBeInTheDocument()
    expect(listReads()).toBe(2)

    writeFails = true
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    expect(await within(section(zhCN.members.title)).findByRole('alert')).toBeInTheDocument()
    expect(listReads()).toBe(2)
  })

  it('toggles a member SSO exemption through the bundle', async () => {
    const user = userEvent.setup()
    const toggled: AdminSetMemberSSOExemptInput[] = []
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ sso_exempt: false, display_name: 'Member One' })]),
      setMemberSsoExempt: (input) => {
        toggled.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    await waitForCondition(() => {
      expect(toggled).toEqual([
        { tenant: 'acme', account: 'acc-1', sso_exempt: true },
      ])
    })
  })

  it('renders the retryable error placeholder when a read fails', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.invitations.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.invitations.error.description)).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when a read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.invitations.retry })).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it("reports the invitation and member failures in their own sections", async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator' })]),
      issueInvitation: () => Promise.reject({ code: 'authn.conflict' }),
      removeMember: () => Promise.reject({ code: 'authn.forbidden' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(await within(section(zhCN.invitations.section.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.conflict,
    )

    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await within(section(zhCN.members.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )

    expect(
      within(section(zhCN.invitations.section.title)).queryByText(zhCN.errors.authn.forbidden),
    ).toBeNull()
    expect(
      within(section(zhCN.members.title)).queryByText(zhCN.errors.authn.conflict),
    ).toBeNull()
    expect(screen.getAllByRole('alert')).toHaveLength(2)
  })

  it('offers the enterprise re-login exit for an SSO refusal on an invitation write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      issueInvitation: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.submit }),
    ).toBeNull()
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })
    await expectNoAxeViolations()

    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it("keeps a plain invitation refusal's own text and offers no SSO exit", async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      issueInvitation: () =>
        Promise.reject({ status: 409, code: 'authn.conflict' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.conflict,
    )
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeNull()
  })

  it('offers the enterprise re-login exit for an SSO refusal on the invitation list read', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withTenant({
      listInvitations: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)

    expect(
      await screen.findByText(zhCN.errors.authn.sso_required),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.invitations.retry }),
    ).toBeNull()
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })

    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('shows one banner with the re-login exit for an SSO refusal on a member write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator' })]),
      setMemberSsoExempt: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)
    await screen.findAllByText('Operator')

    await user.click(
      screen.getByRole('button', { name: zhCN.members.ssoExemptSet }),
    )

    const memberSection = section(zhCN.members.title)
    const alert = await within(memberSection).findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(
      within(memberSection).getByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeInTheDocument()
  })

  it('marks the refused invitation row inline and brings its region into view', async () => {
    mockMatchMedia(false)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      revokeInvitation: () => Promise.reject({ code: 'authn.forbidden' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    const trigger = await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.revoke,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(trigger).toHaveAttribute('aria-invalid', 'true')
    const hintId = trigger.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    expect(document.getElementById(hintId as string)?.textContent).toBe(
      screen.getByRole('alert').textContent,
    )
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    })
    await expectNoAxeViolations()
  })

  it('drops the refused row marker when the next issue starts', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      revokeInvitation: () => Promise.reject({ code: 'authn.forbidden' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    const trigger = await chooseInvitationAction(
      user,
      'ada@example.com',
      zhCN.invitations.revoke,
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(trigger).toHaveAttribute('aria-invalid', 'true')

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    expect(
      await screen.findByText(
        fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(trigger).toHaveAttribute('aria-invalid', 'false')
  })

  it('names the changed object on each landed write', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(
      await screen.findByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' })),
    ).toBeInTheDocument()

    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    expect(
      await screen.findByText(
        fill(zhCN.members.write.saved, {
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      ),
    ).toBeInTheDocument()

    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    expect(
      await screen.findByText(fill(zhCN.members.ssoExemptSaved, { account: byName('Operator') })),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(
      await screen.findByText(fill(zhCN.members.removed, { account: byName('Operator') })),
    ).toBeInTheDocument()
  })

  it('disables the tenant selector while a write is in flight and clears both scopes when the tenant changes', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({
      listTenants: () =>
        Promise.resolve([
          TENANT,
          tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
        ]),
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator', username: '' })]),
      issueInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))

    expect(screen.getByLabelText(zhCN.common.tenant)).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' })),
    ).toBeInTheDocument()

    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    expect(
      await screen.findByText(fill(zhCN.members.ssoExemptSaved, { account: byName('Operator') })),
    ).toBeInTheDocument()

    await user.click(screen.getByLabelText(zhCN.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))
    expect(screen.queryByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }))).toBeNull()
    expect(screen.queryByText(fill(zhCN.members.ssoExemptSaved, { account: byName('Operator') }))).toBeNull()
  })

  it('re-reads the invitation and member lists on refresh', async () => {
    const user = userEvent.setup()
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
    )
    const listMembers = vi.fn(() => Promise.resolve([member()]))
    const operations = withTenant({ listInvitations, listMembers })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')

    const invitationBefore = listInvitations.mock.calls.length
    const memberBefore = listMembers.mock.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitForCondition(() =>
      expect(listInvitations.mock.calls.length).toBe(invitationBefore + 1),
    )
    await waitForCondition(() =>
      expect(listMembers.mock.calls.length).toBe(memberBefore + 1),
    )
  })

  it('locks the scope to a controlled tenant and hides the selector', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=globex')
    try {
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([]))
      const operations = withTenant({ listInvitations, listMembers })
      renderWithProviders(<InvitationsPage tenant="acme" />, { operations })

      await waitForCondition(() => {
        expect(listInvitations).toHaveBeenCalledWith(
          'acme',
          expect.objectContaining({ limit: 50, status: 'pending' }),
        )
      })
      expect(listMembers).toHaveBeenCalledWith('acme', { limit: 100 })
      expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('seeds from defaultTenant ahead of the URL and reports a re-selection', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=globex')
    try {
      const user = userEvent.setup()
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([]))
      const onTenantChange = vi.fn()
      const operations = withTenant({
        listTenants: () =>
          Promise.resolve([
            TENANT,
            tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
          ]),
        listInvitations,
        listMembers,
      })
      renderWithProviders(
        <InvitationsPage defaultTenant="acme" onTenantChange={onTenantChange} />,
        { operations },
      )

      await waitForCondition(() => {
        expect(listInvitations).toHaveBeenCalledWith(
          'acme',
          expect.objectContaining({ status: 'pending' }),
        )
      })
      await waitForCondition(() => {
        expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent(
          'Acme',
        )
      })

      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await waitForCondition(() => {
        expect(listInvitations).toHaveBeenCalledWith(
          'globex',
          expect.objectContaining({ status: 'pending' }),
        )
      })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('keeps the SSO exemption out of the member surface when its feature is absent', async () => {
    const user = userEvent.setup()
    const written: AdminWriteMemberInput[] = []
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      writeMember: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin.sso',
    })
    await pickTenant(user)
    await screen.findByText('Member One')

    expect(
      screen.queryByRole('columnheader', {
        name: zhCN.members.column.ssoExempt,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.members.ssoExemptSet }),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.members.write.ssoExemptLabel),
    ).toBeNull()

    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    await waitForCondition(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true },
      ])
    })
  })

  it('translates the member sources it knows and leaves an unknown one as recorded', async () => {
    const user = userEvent.setup()
    const i18n = createAdminUiI18n()
    i18n.addResourceBundle(
      'zh-CN',
      ADMIN_UI_NAMESPACE,
      { members: { source: SOURCE_LABELS } },
      true,
      true,
    )
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([
          member({ account: 'acc-admin', source: 'admin' }),
          member({ account: 'acc-initial', source: 'initial' }),
          member({ account: 'acc-invite', source: 'invite' }),
          member({ account: 'acc-jit', source: 'jit:acme-idp' }),
          member({ account: 'acc-import', source: 'import' }),
        ]),
      getAccount: ({ id }) =>
        Promise.resolve(accountFixture({ id, display_name: id })),
    })
    renderWithProviders(<InvitationsPage />, { operations, i18n })
    await pickTenant(user)

    expect(await screen.findByText(SOURCE_LABELS.admin)).toBeInTheDocument()
    expect(screen.getByText(SOURCE_LABELS.initial)).toBeInTheDocument()
    expect(screen.getByText(SOURCE_LABELS.invite)).toBeInTheDocument()
    expect(
      screen.getByText(fill(SOURCE_LABELS.jit, { provider: 'acme-idp' })),
    ).toBeInTheDocument()
    expect(screen.getByText('import')).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await expectNoAxeViolations()
  })
})

describe('InvitationsPage write controls stay focusable', () => {
  it('keeps the issue control focused and busy while the invitation is sent', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({
      issueInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })
    await user.click(submit)

    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')
    expect(submit).toHaveAttribute('aria-disabled', 'true')

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    await waitForCondition(() =>
      expect(submit).not.toHaveAttribute('aria-busy', 'true'),
    )
    expect(submit).toHaveFocus()
  })

  it('keeps the issue control focusable and names the missing address', async () => {
    const user = userEvent.setup()
    const issueInvitation = vi.fn(() =>
      Promise.resolve(issuedInvitationFixture()),
    )
    renderWithProviders(<InvitationsPage />, {
      operations: withTenant({ issueInvitation }),
    })
    await pickTenant(user)
    const submit = screen.getByRole('button', {
      name: zhCN.invitations.issue.submit,
    })

    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.invitations.issue.emailRequired,
    )

    submit.focus()
    fireEvent.click(submit)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(submit).toHaveFocus()
    expect(issueInvitation).not.toHaveBeenCalled()
  })

  it('keeps the member write control focused and busy while the membership is saved', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({ writeMember: () => pending.promise })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(
      user,
      zhCN.members.write.accountLabel,
      'Ada',
      'Ada (ada)',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.members.write.submit,
    })
    await user.click(submit)

    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-busy', 'true')

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    await waitForCondition(() =>
      expect(submit).not.toHaveAttribute('aria-busy', 'true'),
    )
    expect(submit).toHaveFocus()
  })

  it('keeps the member write control focusable and names the missing account', async () => {
    const user = userEvent.setup()
    const writeMember = vi.fn(() => Promise.resolve())
    renderWithProviders(<InvitationsPage />, {
      operations: withTenant({ writeMember }),
    })
    await pickTenant(user)
    const submit = screen.getByRole('button', {
      name: zhCN.members.write.submit,
    })

    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.members.write.accountRequired,
    )

    submit.focus()
    fireEvent.click(submit)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(submit).toHaveFocus()
    expect(writeMember).not.toHaveBeenCalled()
  })

  it('keeps the SSO toggle focused and marks the remove control unavailable while it runs', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      setMemberSsoExempt: () => pending.promise,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    const toggle = screen.getByRole('button', {
      name: zhCN.members.ssoExemptSet,
    })
    const remove = screen.getByRole('button', { name: zhCN.members.remove })
    await user.click(toggle)

    expect(toggle).toHaveFocus()
    expect(toggle).toHaveAttribute('aria-busy', 'true')
    expect(remove).toHaveAttribute('aria-disabled', 'true')
    expect(remove).not.toHaveAttribute('aria-busy')
    expect(remove).not.toHaveAttribute('aria-describedby')

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    await waitForCondition(() =>
      expect(remove).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('keeps the remove control focused and marks the SSO toggle unavailable while it runs', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      removeMember: () => pending.promise,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    const remove = screen.getByRole('button', { name: zhCN.members.remove })
    const toggle = screen.getByRole('button', {
      name: zhCN.members.ssoExemptSet,
    })
    await waitForCondition(() => expect(remove).toHaveFocus())
    expect(remove).toHaveAttribute('aria-busy', 'true')
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    expect(toggle).not.toHaveAttribute('aria-busy')

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    await waitForCondition(() =>
      expect(toggle).not.toHaveAttribute('aria-disabled', 'true'),
    )
  })

  it('moves focus to the next row after a removal lands', async () => {
    const user = userEvent.setup()
    let members = [
      member({ account: 'acc-a', display_name: 'Alpha', username: 'alpha' }),
      member({ account: 'acc-b', display_name: 'Bravo', username: 'bravo' }),
      member({ account: 'acc-c', display_name: 'Charlie', username: 'charlie' }),
    ]
    const operations = withTenant({
      listMembers: () => Promise.resolve([...members]),
      removeMember: (input) => {
        members = members.filter((row) => row.account !== input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Bravo')

    await user.click(removeInRow('Bravo'))
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    await waitForCondition(() => expect(screen.queryByText('Bravo')).toBeNull())
    await waitForCondition(() => expect(removeInRow('Charlie')).toHaveFocus())
  })

  it('moves focus to the previous row after the last row is removed', async () => {
    const user = userEvent.setup()
    let members = [
      member({ account: 'acc-a', display_name: 'Alpha', username: 'alpha' }),
      member({ account: 'acc-b', display_name: 'Bravo', username: 'bravo' }),
    ]
    const operations = withTenant({
      listMembers: () => Promise.resolve([...members]),
      removeMember: (input) => {
        members = members.filter((row) => row.account !== input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Bravo')

    await user.click(removeInRow('Bravo'))
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    await waitForCondition(() => expect(screen.queryByText('Bravo')).toBeNull())
    await waitForCondition(() => expect(removeInRow('Alpha')).toHaveFocus())
  })

  it('moves focus to the member list container when the only row is removed', async () => {
    const user = userEvent.setup()
    let members = [
      member({ account: 'acc-b', display_name: 'Bravo', username: 'bravo' }),
    ]
    const operations = withTenant({
      listMembers: () => Promise.resolve([...members]),
      removeMember: (input) => {
        members = members.filter((row) => row.account !== input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Bravo')

    await user.click(removeInRow('Bravo'))
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    await waitForCondition(() => expect(screen.queryByText('Bravo')).toBeNull())
    await waitForCondition(() =>
      expect(screen.getByRole('group', { name: zhCN.members.title })).toHaveFocus(),
    )
  })
})

describe('InvitationsPage removal focus guards', () => {
  it('leaves focus where the reader moved it when a removal lands', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    let rows = [
      member({ account: 'acc-a', display_name: 'Alpha', username: 'alpha' }),
      member({ account: 'acc-b', display_name: 'Bravo', username: 'bravo' }),
      member({ account: 'acc-c', display_name: 'Charlie', username: 'charlie' }),
    ]
    const operations = withTenant({
      listMembers: () => Promise.resolve([...rows]),
      removeMember: (input) =>
        pending.promise.then(() => {
          rows = rows.filter((row) => row.account !== input.account)
        }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Bravo')

    await user.click(removeInRow('Bravo'))
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    const alpha = removeInRow('Alpha')
    alpha.focus()
    expect(alpha).toHaveFocus()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    await waitForCondition(() => expect(screen.queryByText('Bravo')).toBeNull())
    expect(alpha).toHaveFocus()
  })

  it('forgets the removal when its re-read fails, so a later read leaves focus alone', async () => {
    const user = userEvent.setup()
    let fail = false
    let page = 0
    const listMembers = vi.fn(() => {
      if (fail) {
        return Promise.reject({ status: 500, code: 'client.http.500' })
      }
      return page === 0
        ? Promise.resolve({
            members: [
              member({ account: 'acc-b', display_name: 'Bravo', username: 'bravo' }),
              member({ account: 'acc-c', display_name: 'Charlie', username: 'charlie' }),
            ],
            next_cursor: 'c2',
          })
        : Promise.resolve({
            members: [
              member({ account: 'acc-d', display_name: 'Delta', username: 'delta' }),
            ],
            next_cursor: '',
          })
    })
    const operations = withTenant({
      listMembers,
      removeMember: () => Promise.resolve(),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Bravo')

    await user.click(removeInRow('Bravo'))
    const dialog = await screen.findByRole('dialog')
    fail = true
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitForCondition(() => expect(listMembers.mock.calls.length).toBeGreaterThan(1))
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    fail = false

    const staleTrigger = removeInRow('Bravo')
    staleTrigger.focus()
    expect(staleTrigger).toHaveFocus()

    page = 1
    fireEvent.click(screen.getByRole('button', { name: zhCN.common.pager.next }))
    await screen.findByText('Delta')
    expect(document.activeElement).toBe(document.body)
  })
})

describe('InvitationsPage write attribution', () => {
  it('marks the issue control unavailable, not busy, while a resend is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const issueInvitation = vi.fn(() =>
      Promise.resolve(issuedInvitationFixture()),
    )
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [invitation()],
          next_cursor: '',
        }),
      resendInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
      issueInvitation,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )

    await chooseInvitationAction(
      user,
      'ada@example.com',
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
    expect(issueInvitation).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks the membership-write control unavailable, not busy, while a row write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const writeMember = vi.fn(() => Promise.resolve())
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      setMemberSsoExempt: () => pending.promise,
      writeMember,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await chooseAccount(
      user,
      zhCN.members.write.accountLabel,
      'Ada',
      'Ada (ada)',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.members.ssoExemptSet }),
    )

    const submit = screen.getByRole('button', {
      name: zhCN.members.write.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).not.toHaveAttribute('aria-busy')
    fireEvent.click(submit)
    await act(async () => {
      await Promise.resolve()
    })
    expect(writeMember).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks the row controls unavailable, not busy, while the membership write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const removeMember = vi.fn(() => Promise.resolve())
    const operations = withTenant({
      listMembers: () => Promise.resolve([member({ display_name: 'Member One' })]),
      writeMember: () => pending.promise,
      removeMember,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await chooseAccount(
      user,
      zhCN.members.write.accountLabel,
      'Ada',
      'Ada (ada)',
    )
    const submit = screen.getByRole('button', {
      name: zhCN.members.write.submit,
    })
    await user.click(submit)

    expect(submit).toHaveAttribute('aria-busy', 'true')
    const remove = screen.getByRole('button', { name: zhCN.members.remove })
    expect(remove).toHaveAttribute('aria-disabled', 'true')
    expect(remove).not.toHaveAttribute('aria-busy')
    expect(remove).not.toHaveAttribute('aria-describedby')
    fireEvent.click(remove)
    await act(async () => {
      await Promise.resolve()
    })
    expect(removeMember).not.toHaveBeenCalled()
    expect(
      screen.queryByRole('dialog', { name: zhCN.members.removeTitle }),
    ).toBeNull()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })
})
