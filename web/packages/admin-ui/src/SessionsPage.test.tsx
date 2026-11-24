import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { uiKitResources } from '@speed/ui-kit'
import { SessionsPage } from './SessionsPage.js'
import type {
  AdminAccount,
  AdminAccountSession,
  AdminRevokeSessionInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { chooseAccount } from '../test-utils/account-selector.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import {
  createAdminOperations,
  makeAdminBackend,
  type AdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const COPY_VALUE = (
  uiKitResources['zh-CN'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue

function session(overrides: Partial<AdminAccountSession> = {}): AdminAccountSession {
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

function sessionWithoutCurrent(): AdminAccountSession {
  const legacy: Partial<AdminAccountSession> = session()
  delete legacy.current
  return legacy as AdminAccountSession
}

function account(overrides: Partial<AdminAccount> = {}): AdminAccount {
  return {
    id: 'acc-9',
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

function withSessions(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listAccounts: () =>
        Promise.resolve({ accounts: [account()], next_cursor: '' }),
      ...overrides,
    }),
  )
}

async function loadAccount(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await chooseAccount(user, zhCN.sessions.accountLabel, 'Ada', 'Ada (ada)')
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function revokeName(id: string): string {
  return zhCN.sessions.revokeAriaWithId.replace('{{id}}', id)
}

async function confirmRevoke(
  user: ReturnType<typeof userEvent.setup>,
  id: string,
): Promise<void> {
  await user.click(screen.getByRole('button', { name: revokeName(id) }))
  const dialog = await screen.findByRole('dialog')
  await user.click(
    within(dialog).getByRole('button', { name: zhCN.common.confirm }),
  )
  await waitForCondition(() => {
    expect(screen.queryByRole('dialog')).toBeNull()
  })
}

describe('SessionsPage', () => {
  it('prompts for an account before listing', () => {
    const operations = withSessions()
    renderWithProviders(<SessionsPage />, { operations })
    expect(screen.getByText(zhCN.sessions.needAccount.title)).toBeInTheDocument()
  })

  it('puts the empty list placeholder below the list section heading', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)

    expect(
      screen.getByRole('heading', { name: zhCN.sessions.section.title }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', {
        name: zhCN.sessions.empty.title,
        level: 3,
      }),
    ).toBeInTheDocument()
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

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    // The disabled control points at the page's own text, not a second copy.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      zhCN.sessions.needAccount.description,
    )
    // Never natively disabled, so the tooltip attaches to the button itself.
    expect(refresh.parentElement?.tagName).not.toBe('SPAN')
    fireEvent.click(refresh)
    expect(queried).toEqual([])
  })

  it('enables the refresh and drops its explanation once an account is chosen', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    const disabled = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(disabled).toHaveAttribute('aria-disabled', 'true')

    await user.hover(disabled)
    expect(await screen.findByRole('tooltip')).toHaveTextContent(
      zhCN.sessions.needAccount.description,
    )

    await loadAccount(user)
    await waitForCondition(() => {
      expect(
        screen.getByRole('button', { name: zhCN.common.refresh }),
      ).not.toHaveAttribute('aria-disabled', 'true')
    })
    const enabled = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(enabled).not.toHaveAttribute('aria-describedby')
    expect(enabled.parentElement?.tagName).not.toBe('SPAN')
  })

  it('keeps a refresh in flight focusable and drops a second activation', async () => {
    const user = userEvent.setup()
    let reads = 0
    let finishRefresh!: (sessions: readonly AdminAccountSession[]) => void
    const operations = withSessions({
      listAccountSessions: () => {
        reads += 1
        if (reads === 1) {
          return Promise.resolve([session()])
        }
        return new Promise<readonly AdminAccountSession[]>((resolve) => {
          finishRefresh = resolve
        })
      },
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')

    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    await waitForCondition(() => {
      expect(refresh).toHaveAttribute('aria-busy', 'true')
    })
    expect(refresh).not.toBeDisabled()
    expect(refresh).toBe(document.activeElement)
    fireEvent.click(refresh)
    expect(reads).toBe(2)

    await act(async () => {
      finishRefresh([session()])
    })
    await waitForCondition(() => {
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

    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitForCondition(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    await waitForCondition(() => {
      expect(refresh).toHaveAttribute('aria-disabled', 'true')
    })
    expect(refresh).not.toHaveAttribute('aria-describedby')
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(reads).toEqual(['acc-9'])

    await act(async () => {
      pending.resolve()
    })
    await waitForCondition(() => {
      expect(refresh).not.toHaveAttribute('aria-disabled', 'true')
    })
  })

  it('holds the account picker while a revoke is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withSessions({
      listAccounts: () =>
        Promise.resolve({
          accounts: [
            account(),
            account({
              id: 'acc-10',
              display_name: 'Bob',
              identifiers: [
                { kind: 'username', value: 'bob', proven: false, sign_in: true, contact: false, default_contact: false },
              ],
            }),
          ],
          next_cursor: '',
        }),
      listAccountSessions: () => Promise.resolve([session()]),
      revokeSession: () => pending.promise,
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')

    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitForCondition(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    // A change while the revoke settles would land its result under another
    // account's list, so the picker is held for the whole attempt.
    const picker = screen.getByRole('combobox', {
      name: zhCN.sessions.accountLabel,
    })
    await waitForCondition(() => {
      expect(picker).toBeDisabled()
    })

    await act(async () => {
      pending.resolve()
    })
    await waitForCondition(() => {
      expect(picker).not.toBeDisabled()
    })
    expect(
      await screen.findByText(zhCN.sessions.revokedNotice),
    ).toBeInTheDocument()
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
    await chooseAccount(user, zhCN.sessions.accountLabel, 'Ada', 'Ada (ada)')
    await waitForCondition(() => {
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
    const marker = screen.getByText(zhCN.sessions.current)
    expect(marker.closest('tr')).toHaveTextContent('sess-cur')
    expect(marker.closest('tr')).not.toHaveTextContent('sess-oth')
    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-current'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(
        zhCN.sessions.revokeMessageCurrent.replace('{{id}}', 'sess-current'),
      ),
    ).toBeInTheDocument()
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.cancel }),
    )
    await user.click(
      await screen.findByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-other'),
      }),
    )
    const other = await screen.findByRole('dialog')
    expect(
      within(other).getByText(
        zhCN.sessions.revokeMessage.replace('{{id}}', 'sess-other'),
      ),
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
    expect(screen.queryByText(zhCN.sessions.current)).toBeNull()
    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByText(
        zhCN.sessions.revokeMessage.replace('{{id}}', 'sess-1'),
      ),
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
    expect(screen.getByText(zhCN.sessions.assurance.multi)).toBeInTheDocument()
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    const revoke = await screen.findByRole('button', {
      name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
    })
    expect(hasCoarsePointerTouchTarget(revoke)).toBe(true)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: zhCN.common.refresh }),
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
    expect(await screen.findByText(zhCN.sessions.assurance.none)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sessions.assurance.single)).toBeInTheDocument()
    expect(screen.getByText(zhCN.sessions.assurance.multi)).toBeInTheDocument()
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
    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    await waitForCondition(() => {
      expect(revoked).toEqual(['sess-1'])
    })
    expect(
      await screen.findByText(zhCN.sessions.revokedNotice),
    ).toBeInTheDocument()
  })

  it('keeps focus on the revoke control while its write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    let rows = [session({ id: 'sess-a' }), session({ id: 'sess-b' })]
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve(rows),
      revokeSession: (input) => {
        rows = rows.filter((row) => row.id !== input.sessionId)
        return pending.promise
      },
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-a')

    await confirmRevoke(user, 'sess-a')

    const revoke = screen.getByRole('button', { name: revokeName('sess-a') })
    expect(revoke).toBe(document.activeElement)
    expect(revoke).toHaveAttribute('aria-disabled', 'true')
    expect(revoke).toHaveAttribute('aria-busy', 'true')

    await act(async () => {
      pending.resolve()
    })
  })

  it('holds sibling revoke controls back while one row is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    let rows = [
      session({ id: 'sess-a' }),
      session({ id: 'sess-b' }),
      session({ id: 'sess-c' }),
    ]
    const revokeSession = vi.fn((input: AdminRevokeSessionInput) => {
      rows = rows.filter((row) => row.id !== input.sessionId)
      return pending.promise
    })
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve(rows),
      revokeSession,
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-b')

    await confirmRevoke(user, 'sess-b')
    expect(revokeSession).toHaveBeenCalledTimes(1)

    const sibling = screen.getByRole('button', { name: revokeName('sess-a') })
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy', 'true')
    expect(sibling).not.toHaveAccessibleDescription()

    await user.click(sibling)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(revokeSession).toHaveBeenCalledTimes(1)

    await act(async () => {
      pending.resolve()
    })
  })

  it('clears a landed revoke confirmation on the page refresh', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
      revokeSession: () => Promise.resolve(),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')

    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    expect(
      await screen.findByText(zhCN.sessions.revokedNotice),
    ).toBeInTheDocument()
    await waitForCondition(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitForCondition(() => {
      expect(screen.queryByText(zhCN.sessions.revokedNotice)).toBeNull()
    })
  })

  it('clears a refused revoke through the page refresh', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.resolve([session()]),
      revokeSession: () => Promise.reject({ code: 'authn.conflict' }),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    await screen.findByText('sess-1')

    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.conflict)
    await waitForCondition(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitForCondition(() => {
      expect(screen.queryByRole('alert')).toBeNull()
    })
  })

  it('clears the revoke result when another account is chosen', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccounts: () =>
        Promise.resolve({
          accounts: [
            account(),
            account({
              id: 'acc-10',
              display_name: 'Bob',
              identifiers: [
                { kind: 'username', value: 'bob', proven: false, sign_in: true, contact: false, default_contact: false },
              ],
            }),
          ],
          next_cursor: '',
        }),
      listAccountSessions: () => Promise.resolve([session()]),
      revokeSession: () => Promise.resolve(),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await chooseAccount(user, zhCN.sessions.accountLabel, 'Ada', 'Ada (ada)')
    await screen.findByText('sess-1')

    await user.click(
      screen.getByRole('button', {
        name: zhCN.sessions.revokeAriaWithId.replace('{{id}}', 'sess-1'),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    expect(
      await screen.findByText(zhCN.sessions.revokedNotice),
    ).toBeInTheDocument()
    await waitForCondition(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    await chooseAccount(user, zhCN.sessions.accountLabel, 'Bob', 'Bob (bob)')
    await waitForCondition(() => {
      expect(screen.queryByText(zhCN.sessions.revokedNotice)).toBeNull()
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText(zhCN.sessions.error.title)).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.sessions.error.description),
    ).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withSessions({
      listAccountSessions: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<SessionsPage />, { operations })
    await loadAccount(user)
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.sessions.retry }),
    ).toBeNull()
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
    expect(columnVisibility(header(zhCN.sessions.column.id))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.sessions.column.kind))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.sessions.column.assurance))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(zhCN.sessions.column.created))).toBeNull()
    expect(columnVisibility(header(zhCN.sessions.column.lastSeen))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.sessions.column.expires))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.sessions.column.state))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header(zhCN.sessions.column.actions))).toBeNull()

    const prefix = screen.getByText(id.slice(0, 8))
    const idCell = prefix.closest('td')
    expect(idCell).not.toBeNull()
    expect(within(idCell as HTMLElement).queryByText(id)).toBeNull()
    expect(idCell).toHaveAttribute('title', id)
    expect(
      within(idCell as HTMLElement).getByRole('button', {
        name: COPY_VALUE.replace('{{value}}', id),
      }),
    ).toBeInTheDocument()

    const headerLabels = screen
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent)
    const createdIndex = headerLabels.indexOf(zhCN.sessions.column.created)
    expect(createdIndex).toBeGreaterThanOrEqual(0)
    const row = screen.getByText(id.slice(0, 8)).closest('tr')
    expect(row).not.toBeNull()
    const createdCell = within(row as HTMLElement).getAllByRole('cell')[
      createdIndex
    ]
    expect(createdCell).toBeDefined()

    expect(
      within(createdCell as HTMLElement).getByText(zhCN.sessions.current),
    ).toBeInTheDocument()
    expect(
      within(idCell as HTMLElement).queryByText(zhCN.sessions.current),
    ).toBeNull()

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
    await waitForCondition(() =>
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
