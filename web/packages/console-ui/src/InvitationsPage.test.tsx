/**
 * InvitationsPage: the tenant invitation and membership page.
 *
 * It prompts for a tenant until one is chosen (the selector is fed by the
 * roster read), then lists the tenant's invitations and members and drives
 * the issue / revoke / remove writes, the membership write and the SSO
 * exemption toggle through the bundle.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InvitationsPage } from './InvitationsPage.js'
import type {
  ConsoleAccount,
  ConsoleInvitation,
  ConsoleMember,
  ConsoleSetMemberSSOExemptInput,
  ConsoleWriteMemberInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createConsoleOperations,
  issuedInvitationFixture,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { ConsoleInvitationLinkProvider } from './internal/invitation-link.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

function invitation(overrides: Partial<ConsoleInvitation> = {}): ConsoleInvitation {
  return {
    id: 'inv-1',
    tenant: 'acme',
    email: 'ada@example.com',
    status: 'pending',
    invited_by: 'staff-1',
    expires_at: '2026-10-20T00:00:00Z',
    accepted_at: '0001-01-01T00:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function member(overrides: Partial<ConsoleMember> = {}): ConsoleMember {
  return {
    account: 'acc-1',
    tenant: 'acme',
    active: true,
    sso_exempt: false,
    source: 'direct',
    ...overrides,
  }
}

/** The account the picker finds: named "Ada", identified by an opaque id. */
function account(overrides: Partial<ConsoleAccount> = {}): ConsoleAccount {
  return {
    id: 'acc-2',
    kind: 'user',
    status: 'active',
    display_name: 'Ada',
    mfa_required: false,
    protected: false,
    password_set: true,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('租户'))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

function withTenant(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listAccounts: () =>
        Promise.resolve({ accounts: [account()], next_cursor: '' }),
      ...overrides,
    }),
  )
}

describe('InvitationsPage', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
  })

  it('prompts for a tenant before listing', () => {
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/console/invitations?tenant=acme')
    try {
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([member()]))
      const operations = withTenant({ listInvitations, listMembers })
      renderWithProviders(<InvitationsPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: both reads run for it and the selector shows its alias.
      expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
      expect(screen.queryByText('请选择租户')).toBeNull()
      expect(screen.getByLabelText('租户')).toHaveTextContent('Acme')
      expect(listInvitations).toHaveBeenCalledWith('acme')
      expect(listMembers).toHaveBeenCalledWith('acme')
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('lists the tenant invitations and members', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
      getAccount: ({ id }) =>
        Promise.resolve(
          accountFixture({
            id,
            display_name: id === 'staff-1' ? 'Staff One' : 'Member One',
          }),
        ),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
    expect(screen.getByText('待接受')).toBeInTheDocument()
    expect(screen.getByText('Staff One')).toBeInTheDocument()
    expect(screen.getByText('Member One')).toBeInTheDocument()
  })

  it("shows the inviter's display name, or the recorded id marked unresolved when the lookup is refused", async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Staff One' })),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
    first.unmount()

    const refused = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage />, { operations: refused })
    await pickTenant(user)
    // The cell keeps the recorded id but labels it unresolved rather than
    // passing it off as a resolved name.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('staff-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it("shows the member's display name, or the recorded id marked unresolved when the lookup is refused", async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    expect(await screen.findByText('Member One')).toBeInTheDocument()
    first.unmount()

    const refused = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage />, { operations: refused })
    await pickTenant(user)
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it('reads a repeated account once across rows', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() =>
      Promise.resolve(accountFixture({ display_name: 'Staff One' })),
    )
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({ id: 'inv-1', invited_by: 'staff-9' }),
            invitation({ id: 'inv-2', invited_by: 'staff-9' }),
          ],
          next_cursor: '',
        }),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findAllByText('Staff One')).toHaveLength(2)
    expect(getAccount).toHaveBeenCalledTimes(1)
    expect(getAccount).toHaveBeenCalledWith({ id: 'staff-9' })
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
      screen.getByRole('button', { name: '刷新' }),
      screen.getByRole('button', { name: '撤销' }),
      screen.getByRole('button', { name: '豁免 SSO' }),
      screen.getByRole('button', { name: '移除' }),
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
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    await vi.waitFor(() => {
      expect(issued).toEqual(['new@example.com'])
    })
  })

  it('takes the invitation expiry from a date-time field and sends the instant', async () => {
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
    // The expiry is a date-time control, not a free-text box.
    const expires = screen.getByLabelText('过期时间')
    expect(expires).toHaveAttribute('type', 'datetime-local')
    fireEvent.change(expires, { target: { value: '2026-11-01T12:00' } })
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    await vi.waitFor(() => {
      expect(expiresSent).toEqual([
        new Date('2026-11-01T12:00').toISOString(),
      ])
    })
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
      <ConsoleInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </ConsoleInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    // The one-time token is turned into the host's acceptance link, not dropped.
    expect(
      await screen.findByText('把这条接受链接交给「new@example.com」：'),
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
      <ConsoleInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </ConsoleInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    const link = `${window.location.origin}/invitations/spd_inv_test_token`
    await screen.findByText(link)
    await user.click(screen.getByRole('button', { name: '复制接受链接' }))
    await vi.waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(link)
    })
  })

  it('shows the bare token when the host supplies no link builder', async () => {    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    // With no builder the token is still surfaced, never silently dropped.
    expect(
      await screen.findByText('「ada@example.com」的接受令牌（链接由宿主构造）：'),
    ).toBeInTheDocument()
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
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
    await user.click(screen.getByRole('button', { name: '撤销' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(revoked).toEqual(['inv-1'])
    })
  })

  it('removes a membership through the bundle', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
      removeMember: (input) => {
        removed.push(input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(removed).toEqual(['acc-1'])
    })
  })

  it('writes the chosen account id, not the typed name', async () => {
    const user = userEvent.setup()
    const written: ConsoleWriteMemberInput[] = []
    const operations = withTenant({
      writeMember: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    // No account chosen: the picker holds no id yet.
    expect(
      screen.getByRole('button', { name: '写入成员关系' }),
    ).toBeDisabled()
    // A display name finds the account; the write carries its id ("acc-2"),
    // never the typed name.
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '写入成员关系' }))
    await vi.waitFor(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true, sso_exempt: false },
      ])
    })
    // The landed write clears both the selection and the box that named it.
    expect(screen.getByLabelText('账户')).toHaveValue('')
  })

  it('toggles a member SSO exemption through the bundle', async () => {
    const user = userEvent.setup()
    const toggled: ConsoleSetMemberSSOExemptInput[] = []
    const operations = withTenant({
      listMembers: () => Promise.resolve([member({ sso_exempt: false })]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
      setMemberSsoExempt: (input) => {
        toggled.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: '豁免 SSO' }))
    await vi.waitFor(() => {
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
    expect(await screen.findByText('邀请加载失败')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when a read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    // The refusal is a permission denial, not a load failure: the
    // permission text, and no retry (a retry would only be refused again).
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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
