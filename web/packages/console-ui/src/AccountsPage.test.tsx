/**
 * AccountsPage: the account-administration page.
 *
 * It lists accounts (narrowed by an identifier), opens one account's
 * detail (its fields, factors, secrets and sessions), and drives the
 * body-free writes -- delete, reset MFA, revoke sessions, revoke a secret,
 * revoke a session and remove a named identifier -- through the bundle.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AccountsPage } from './AccountsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

function withAccounts(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listAccounts: () =>
        Promise.resolve({
          accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
          next_cursor: '',
        }),
      ...overrides,
    }),
  )
}

async function search(
  user: ReturnType<typeof userEvent.setup>,
  value = 'ada',
): Promise<void> {
  await user.type(screen.getByLabelText('登录标识'), value)
  await user.click(screen.getByRole('button', { name: '查询' }))
}

async function openDetail(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await search(user)
  await screen.findByText('Ada Lovelace')
  await user.click(screen.getByRole('button', { name: '管理' }))
}

describe('AccountsPage', () => {
  it('renders the empty state when no account matches', async () => {
    const operations = withAccounts({
      listAccounts: () => Promise.resolve({ accounts: [], next_cursor: '' }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    expect(await screen.findByText('暂无账户')).toBeInTheDocument()
  })

  it('lists accounts and shows their status', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.getByText('已启用')).toBeInTheDocument()
  })

  it('opens the account detail with its factors, secrets and sessions', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountFactors: () =>
        Promise.resolve([{ kind: 'totp', label: 'Authenticator', status: 'active' }]),
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: '0001-01-01T00:00:00Z', revoked: false },
        ]),
      listAccountSessions: () =>
        Promise.resolve([
          {
            id: 'sess-1',
            kind: 'refresh',
            assurance: 1,
            auth_time: '2026-10-15T12:00:00Z',
            created_at: '2026-10-15T12:00:00Z',
            last_seen_at: '2026-10-15T12:00:00Z',
            expires_at: '2026-10-16T12:00:00Z',
            revoked: false,
          },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    expect(await screen.findByText('Authenticator')).toBeInTheDocument()
    expect(screen.getByText('ci')).toBeInTheDocument()
    expect(screen.getByText('sess-1')).toBeInTheDocument()
  })

  it('deletes an account through the bundle', async () => {
    const user = userEvent.setup()
    const deleted: string[] = []
    const operations = withAccounts({
      deleteAccount: (input) => {
        deleted.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    await user.click(screen.getByRole('button', { name: '删除' }))
    await vi.waitFor(() => {
      expect(deleted).toEqual(['acc-1'])
    })
  })

  it('resets MFA and revokes sessions from the detail', async () => {
    const user = userEvent.setup()
    const reset: string[] = []
    const revoked: string[] = []
    const operations = withAccounts({
      resetMFA: (input) => {
        reset.push(input.id)
        return Promise.resolve()
      },
      revokeAccountSessions: (input) => {
        revoked.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.click(await screen.findByRole('button', { name: '重置 MFA' }))
    await user.click(screen.getByRole('button', { name: '撤销全部会话' }))
    await vi.waitFor(() => {
      expect(reset).toEqual(['acc-1'])
      expect(revoked).toEqual(['acc-1'])
    })
  })

  it('revokes a secret and a session through the bundle', async () => {
    const user = userEvent.setup()
    const secret: string[] = []
    const session: string[] = []
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: '0001-01-01T00:00:00Z', revoked: false },
        ]),
      listAccountSessions: () =>
        Promise.resolve([
          {
            id: 'sess-1',
            kind: 'refresh',
            assurance: 1,
            auth_time: '2026-10-15T12:00:00Z',
            created_at: '2026-10-15T12:00:00Z',
            last_seen_at: '2026-10-15T12:00:00Z',
            expires_at: '2026-10-16T12:00:00Z',
            revoked: false,
          },
        ]),
      revokeSecret: (input) => {
        secret.push(input.secret)
        return Promise.resolve()
      },
      revokeSession: (input) => {
        session.push(input.sessionId)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await screen.findByText('ci')
    const revokeButtons = screen.getAllByRole('button', { name: '撤销' })
    await user.click(revokeButtons[0]!)
    await user.click(revokeButtons[1]!)
    await vi.waitFor(() => {
      expect(secret).toEqual(['sec-1'])
      expect(session).toEqual(['sess-1'])
    })
  })

  it('removes a named identifier through the bundle', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withAccounts({
      removeIdentifier: (input) => {
        removed.push(input.identifier)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.type(
      await screen.findByLabelText('移除登录标识'),
      'ada@example.com',
    )
    await user.click(screen.getByRole('button', { name: '移除' }))
    await vi.waitFor(() => {
      expect(removed).toEqual(['ada@example.com'])
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccounts: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(await screen.findByText('账户加载失败')).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    await expectNoAxeViolations()
  })
})
