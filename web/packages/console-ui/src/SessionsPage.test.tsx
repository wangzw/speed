/**
 * SessionsPage: the account-session administration page.
 *
 * Its read lists one account's sessions (assurance rendered as a label),
 * the page prompts for an account until one is chosen through the account
 * picker, a session revokes through the bundle, and a failed read settles
 * into the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SessionsPage } from './SessionsPage.js'
import type { ConsoleAccount, ConsoleAccountSession } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { chooseAccount } from '../test-utils/account-selector.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

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
    current: false,
    ...overrides,
  }
}

/** A row an older server answered with: the current flag is absent. */
function sessionWithoutCurrent(): ConsoleAccountSession {
  const legacy: Partial<ConsoleAccountSession> = session()
  delete legacy.current
  return legacy as ConsoleAccountSession
}

/** The account the picker finds: named "Ada", identified by an opaque id. */
function account(overrides: Partial<ConsoleAccount> = {}): ConsoleAccount {
  return {
    id: 'acc-9',
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

/** The scripted bundle with the account search answer the picker needs. */
function withSessions(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listAccounts: () =>
        Promise.resolve({ accounts: [account()], next_cursor: '' }),
      ...overrides,
    }),
  )
}

async function loadAccount(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await chooseAccount(user, '账户', 'Ada', 'Ada (acc-9)')
}

/** A promise the test settles by hand, for observing an in-flight write. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

describe('SessionsPage', () => {
  it('prompts for an account before listing', () => {
    const operations = withSessions()
    renderWithProviders(<SessionsPage />, { operations })
    expect(screen.getByText('请指定账户')).toBeInTheDocument()
  })

  it('does not read an empty account scope on refresh', () => {
    const queried: string[] = []
    const operations = withSessions({
      listAccountSessions: (query) => {
        queried.push(query.id ?? '')
        return Promise.resolve([session()])
      },
    })
    renderWithProviders(<SessionsPage />, { operations })

    // The read is disabled while no account is chosen; the refresh control
    // follows it instead of issuing the read with an empty id.
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose an
    // account first" text, not by a second copy of it.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      '会话按账户列出，请搜索并选择要列出的账户。',
    )
    // MUI's Tooltip listens on its child element; a native disabled button
    // receives no pointer events, so the control gets a span wrapper.
    expect(refresh.parentElement?.tagName).toBe('SPAN')
    // A programmatic click, because user-event refuses a disabled target:
    // the control must stay inert even when reached from a script.
    fireEvent.click(refresh)
    expect(queried).toEqual([])
  })

  it('enables the refresh and drops its explanation once an account is chosen', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    const disabled = screen.getByRole('button', { name: '刷新' })
    expect(disabled).toBeDisabled()

    // The tooltip names the same text the page already renders.
    await user.hover(disabled.parentElement as HTMLElement)
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      '会话按账户列出，请搜索并选择要列出的账户。',
    )

    await loadAccount(user)
    await vi.waitFor(() => {
      expect(screen.getByRole('button', { name: '刷新' })).not.toBeDisabled()
    })
    const enabled = screen.getByRole('button', { name: '刷新' })
    expect(enabled).not.toHaveAttribute('aria-describedby')
    expect(enabled.parentElement?.tagName).not.toBe('SPAN')
  })

  it('keeps a refresh in flight focusable and drops a second activation', async () => {
    const user = userEvent.setup()
    let reads = 0
    let finishRefresh!: (sessions: readonly ConsoleAccountSession[]) => void
    const operations = withSessions({
      listAccountSessions: () => {
        reads += 1
        if (reads === 1) {
          return Promise.resolve([session()])
        }
        return new Promise<readonly ConsoleAccountSession[]>((resolve) => {
          finishRefresh = resolve
        })
      },
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')

    await user.click(screen.getByRole('button', { name: '刷新' }))
    const refresh = screen.getByRole('button', { name: '刷新' })
    await vi.waitFor(() => {
      expect(refresh).toHaveAttribute('aria-busy', 'true')
    })
    // A busy read keeps the control enabled and focused: the read itself
    // must not take the operator's place away.
    expect(refresh).not.toBeDisabled()
    expect(refresh).toBe(document.activeElement)
    // A forced activation while the read is in flight issues no second
    // request and queues none.
    fireEvent.click(refresh)
    expect(reads).toBe(2)

    await act(async () => {
      finishRefresh([session()])
    })
    await vi.waitFor(() => {
      expect(refresh).not.toHaveAttribute('aria-busy')
    })
    expect(reads).toBe(2)
    expect(refresh).toBe(document.activeElement)
  })

  it('disables the refresh while a revoke is in flight and issues no read from it', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const reads: string[] = []
    const operations = withSessions({
      listAccountSessions: (query) => {
        reads.push(query.id ?? '')
        return Promise.resolve([session()])
      },
      revokeSession: () => pending.promise,
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')
    expect(reads).toEqual(['acc-9'])

    await user.click(screen.getByRole('button', { name: '撤销会话 sess-1' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    // While the dialog's exit transition runs the page behind it is
    // hidden from the accessibility tree; the revoke is already in flight.
    await vi.waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    const refresh = screen.getByRole('button', { name: '刷新' })
    await vi.waitFor(() => {
      expect(refresh).toBeDisabled()
    })
    // The scope is chosen, so the disabled control does not claim it is
    // empty; it is disabled because the revoke it would race is in flight.
    expect(refresh).not.toHaveAttribute('aria-describedby')
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(reads).toEqual(['acc-9'])

    await act(async () => {
      pending.resolve()
    })
    await vi.waitFor(() => {
      expect(refresh).not.toBeDisabled()
    })
  })

  it('reads an account\'s sessions as soon as it is chosen', async () => {
    const user = userEvent.setup()
    const queried: string[] = []
    const operations = withSessions({
      listAccountSessions: (query) => {
        queried.push(query.id ?? '')
        return Promise.resolve([session()])
      },
    })
    renderWithProviders(<SessionsPage />, { operations })
    // Choosing the account is the whole gesture: no further control stands
    // between the picker and the read.
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-9)')
    await vi.waitFor(() => {
      expect(queried).toEqual(['acc-9'])
    })
    expect(await screen.findByText('sess-1')).toBeInTheDocument()
  })

  it('marks the caller\'s current session and says its revoke signs them out', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () =>
        Promise.resolve([
          session({ id: 'sess-current', current: true }),
          session({ id: 'sess-other', current: false }),
        ]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-cur')
    // The marker sits on the current row only. The cell shows the id
    // prefix; the complete id stays on the title and copy control.
    const marker = screen.getByText('当前会话')
    expect(marker.closest('tr')).toHaveTextContent('sess-cur')
    expect(marker.closest('tr')).not.toHaveTextContent('sess-oth')
    // The current row's revocation says the caller is signed out too.
    await user.click(
      screen.getByRole('button', { name: '撤销会话 sess-current' }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(
        '撤销会话「sess-current」。这是你当前的会话，撤销后你也会退出登录。',
      ),
    ).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    // A row the server does not mark keeps the plain confirmation.
    await user.click(
      await screen.findByRole('button', { name: '撤销会话 sess-other' }),
    )
    const other = await screen.findByRole('dialog')
    expect(
      within(other).getByText('撤销会话「sess-other」。'),
    ).toBeInTheDocument()
  })

  it('treats an answer without current as not the caller\'s session', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([sessionWithoutCurrent()]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')
    expect(screen.queryByText('当前会话')).toBeNull()
    await user.click(screen.getByRole('button', { name: '撤销会话 sess-1' }))
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText('撤销会话「sess-1」。'),
    ).toBeInTheDocument()
  })

  it('lists the sessions of the chosen account', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('sess-1')).toBeInTheDocument()
    expect(screen.getByText('多因子')).toBeInTheDocument()
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
    })
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
    const operations = withSessions({
      listAccountSessions: () =>
        Promise.resolve([
          session({ id: 'a', assurance: 0 }),
          session({ id: 'b', assurance: 1 }),
          session({ id: 'c', assurance: 2 }),
        ]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('无')).toBeInTheDocument()
    expect(screen.getByText('单因子')).toBeInTheDocument()
    expect(screen.getByText('多因子')).toBeInTheDocument()
  })

  it('revokes a session through the bundle', async () => {
    const user = userEvent.setup()
    const revoked: string[] = []
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
      revokeSession: (input) => {
        revoked.push(input.sessionId)
        return Promise.resolve()
      },
    })
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
    const operations = withSessions({
      listAccountSessions: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('会话加载失败')).toBeInTheDocument()
    expect(screen.getByText('会话面未能完成这次请求。')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('declares the designed column priorities and caps the primary identity', async () => {
    const user = userEvent.setup()
    const id = 'sess-0123456789abcdef'
    const operations = withSessions({
      listAccountSessions: () =>
        Promise.resolve([session({ id, current: true })]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText(id.slice(0, 8))

    const header = (label: string): Element =>
      screen.getByRole('columnheader', { name: label })
    // The created time is the primary identity: no priority, shared cap.
    // The identifier and the descriptive columns shed first; state stays
    // to `sm`; the row actions never hide.
    expect(columnVisibility(header('标识'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header('类型'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header('保证级别'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header('创建时间'))).toBeNull()
    expect(columnVisibility(header('最近活动'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header('过期时间'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header('状态'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header('操作'))).toBeNull()

    // The identifier cell shows the 8-character prefix; the complete id
    // rides the cell title and the copy control.
    const prefix = screen.getByText(id.slice(0, 8))
    const idCell = prefix.closest('td')
    expect(idCell).not.toBeNull()
    expect(within(idCell as HTMLElement).queryByText(id)).toBeNull()
    expect(idCell).toHaveAttribute('title', id)
    expect(
      within(idCell as HTMLElement).getByRole('button', {
        name: `复制 ${id}`,
      }),
    ).toBeInTheDocument()

    const headerLabels = screen
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent)
    const createdIndex = headerLabels.indexOf('创建时间')
    expect(createdIndex).toBeGreaterThanOrEqual(0)
    const row = screen.getByText(id.slice(0, 8)).closest('tr')
    expect(row).not.toBeNull()
    const createdCell = within(row as HTMLElement).getAllByRole('cell')[
      createdIndex
    ]
    expect(createdCell).toBeDefined()

    // The current-session mark rides the primary identity (created time),
    // which declares no priority and therefore stays visible at every
    // tier; the id column it left hides below `lg`. The mark is therefore
    // reachable at all three tiers even where the id cell is not.
    expect(
      within(createdCell as HTMLElement).getByText('当前会话'),
    ).toBeInTheDocument()
    expect(within(idCell as HTMLElement).queryByText('当前会话')).toBeNull()

    // The shared cap on the created-time text: 160px below `md`, 200px
    // from `md` up. The mark sits beside it, outside that text cap.
    const createdText = createdCell?.querySelector('p')
    expect(createdText).not.toBeNull()
    expect(containerMaxWidthOf(createdText as Element)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('re-reads the chosen account sessions on refresh', async () => {
    const user = userEvent.setup()
    const listAccountSessions = vi.fn(() => Promise.resolve([session()]))
    const operations = withSessions({ listAccountSessions })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')

    const before = listAccountSessions.mock.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await vi.waitFor(() =>
      expect(listAccountSessions.mock.calls.length).toBe(before + 1),
    )
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')
    await expectNoAxeViolations()
  })
})
