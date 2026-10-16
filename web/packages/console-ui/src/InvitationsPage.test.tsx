/**
 * InvitationsPage: the tenant invitation and membership page.
 *
 * It prompts for a tenant until one is chosen (the selector is fed by the
 * roster read), then lists the tenant's invitations and members and drives
 * the issue / revoke / remove writes, the membership write and the SSO
 * exemption toggle through the bundle.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InvitationsPage } from './InvitationsPage.js'
import type {
  ConsoleInvitation,
  ConsoleMember,
  ConsoleSetMemberSSOExemptInput,
  ConsoleWriteMemberInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
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
      ...overrides,
    }),
  )
}

describe('InvitationsPage', () => {
  it('prompts for a tenant before listing', () => {
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
  })

  it('lists the tenant invitations and members', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
    expect(screen.getByText('待接受')).toBeInTheDocument()
    expect(screen.getByText('acc-1')).toBeInTheDocument()
  })

  it('issues an invitation through the bundle', async () => {
    const user = userEvent.setup()
    const issued: string[] = []
    const operations = withTenant({
      issueInvitation: (input) => {
        issued.push(input.email)
        return Promise.resolve()
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
        return Promise.resolve()
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
      removeMember: (input) => {
        removed.push(input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('acc-1')
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(removed).toEqual(['acc-1'])
    })
  })

  it('writes a membership through the bundle', async () => {
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
    await user.type(await screen.findByLabelText('账户'), 'acc-2')
    await user.click(screen.getByRole('button', { name: '写入成员关系' }))
    await vi.waitFor(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true, sso_exempt: false },
      ])
    })
  })

  it('toggles a member SSO exemption through the bundle', async () => {
    const user = userEvent.setup()
    const toggled: ConsoleSetMemberSSOExemptInput[] = []
    const operations = withTenant({
      listMembers: () => Promise.resolve([member({ sso_exempt: false })]),
      setMemberSsoExempt: (input) => {
        toggled.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('acc-1')
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
