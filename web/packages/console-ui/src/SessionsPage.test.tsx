/**
 * SessionsPage: the account-session administration page.
 *
 * Its read lists one account's sessions (assurance rendered as a label),
 * the page prompts for an account until one is named, a session revokes
 * through the bundle, and a failed read settles into the retryable error
 * placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SessionsPage } from './SessionsPage.js'
import type { ConsoleAccountSession } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

function session(overrides: Partial<ConsoleAccountSession> = {}): ConsoleAccountSession {
  return {
    id: 'sess-1',
    kind: 'refresh',
    assurance: 2,
    auth_time: '2026-10-15T12:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
    last_seen_at: '2026-10-15T12:30:00Z',
    expires_at: '2026-10-16T12:00:00Z',
    revoked: false,
    ...overrides,
  }
}

async function loadAccount(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.type(screen.getByLabelText('账户标识'), 'acc-1')
  await user.click(screen.getByRole('button', { name: '加载' }))
}

describe('SessionsPage', () => {
  it('prompts for an account before listing', () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<SessionsPage />, { operations })
    expect(screen.getByText('请指定账户')).toBeInTheDocument()
  })

  it('lists the sessions of the named account', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () => Promise.resolve([session()]),
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('sess-1')).toBeInTheDocument()
    expect(screen.getByText('多因子')).toBeInTheDocument()
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () => Promise.resolve([session()]),
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    const revoke = await screen.findByRole('button', {
      name: '撤销会话 sess-1',
    })
    expect(hasCoarsePointerTouchTarget(revoke)).toBe(true)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: '刷新' }),
      ),
    ).toBe(true)
  })

  it('labels each assurance level', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () =>
          Promise.resolve([
            session({ id: 'a', assurance: 0 }),
            session({ id: 'b', assurance: 1 }),
            session({ id: 'c', assurance: 2 }),
          ]),
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('无')).toBeInTheDocument()
    expect(screen.getByText('单因子')).toBeInTheDocument()
    expect(screen.getByText('多因子')).toBeInTheDocument()
  })

  it('revokes a session through the bundle', async () => {
    const user = userEvent.setup()
    const revoked: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () => Promise.resolve([session()]),
        revokeSession: (input) => {
          revoked.push(input.sessionId)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')
    // The revoke control names its own session id, so two rows' buttons are
    // distinguishable.
    await user.click(screen.getByRole('button', { name: '撤销会话 sess-1' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(revoked).toEqual(['sess-1'])
    })
    // The landed revoke fills the page's standing status region.
    expect(await screen.findByText('会话已撤销')).toBeInTheDocument()
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('会话加载失败')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccountSessions: () => Promise.resolve([session()]),
      }),
    )
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')
    await expectNoAxeViolations()
  })
})
