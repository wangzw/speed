import { describe, expect, it } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { chipClasses } from '@mui/material/Chip'
import { CONFIRM_ARM_LOCKOUT_MS } from '@speed/ui-kit'
import { formatInstant } from '@speed/i18n'
import { AccountsPage } from './AccountsPage.js'
import type { AdminAccount } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import {
  fill,
  DETAIL_PANEL_NAME,
  DELETE_ACCOUNT_NAME,
  REVOKE_SECRET_CI_NAME,
  REVOKE_SESSION_SESS_1_NAME,
  withAccounts,
  sessionFixture,
  revokeSessionName,
  revokeSecretName,
  sessionsWithoutRevoked,
  search,
  openDetail,
  deleteRowThroughConfirm,
  CONFIRM_AGAIN_LABEL,
  CONFIRM_LABEL,
  confirmDialog,
  FORBIDDEN_TEXT,
  SESSION_ROW,
  outsideOf,
  ALL_DETAIL_WRITES,
} from '../test-utils/accounts-page.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

describe('AccountsPage', () => {
  it('opens the account detail with its factors, secrets and sessions', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountFactors: () =>
        Promise.resolve([{ kind: 'totp', label: 'Authenticator', status: 'active' }]),
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: null, revoked: false },
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
            current: false,
          },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    expect(await screen.findByText('Authenticator')).toBeInTheDocument()
    expect(screen.getByText('ci')).toBeInTheDocument()
    expect(screen.getByText('sess-1')).toBeInTheDocument()
  })

  it('shows a secret that never expires and renders a dated one in UTC', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: null, revoked: false },
          {
            id: 'sec-2',
            label: 'deploy',
            expires_at: '2026-11-20T12:00:00Z',
            revoked: false,
          },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations, timeZone: 'UTC' })
    await openDetail(user)
    expect(
      await screen.findByText(zhCN.accounts.secrets.neverExpires),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        formatInstant('2026-11-20T12:00:00Z', 'zh-CN', { timeZone: 'UTC' }),
      ),
    ).toBeInTheDocument()
  })

  it('renders the never-expires label in the active language', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: null, revoked: false },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations, language: 'en-US' })
    await user.type(
      screen.getByLabelText(enUS.accounts.identifierLabel),
      'ada',
    )
    await user.click(screen.getByRole('button', { name: enUS.accounts.search }))
    await screen.findByText('Ada Lovelace')
    await user.click(
      screen.getByRole('button', {
        name: fill(enUS.accounts.manageAria, { name: 'Ada Lovelace' }),
      }),
    )
    expect(
      await screen.findByText(enUS.accounts.secrets.neverExpires),
    ).toBeInTheDocument()
  })

  it('surfaces a detail sub-read failure instead of an empty table', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountFactors: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    expect(await screen.findByText(zhCN.accounts.factors.error.title)).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.accounts.factors.error.description),
    ).toBeInTheDocument()
  })

  it('confirms before deleting an account, then deletes it through the bundle', async () => {
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
    await user.click(screen.getByRole('button', { name: DELETE_ACCOUNT_NAME }))
    expect(deleted).toEqual([])
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
    expect(deleted).toEqual([])
    expect(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    ).toBeInTheDocument()
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitForCondition(() => {
      expect(deleted).toEqual(['acc-1'])
    })
  })

  const DELETE_UUID = '01a170b2-7c4d-7e2f-9a11-6b0c2f9e4d31'

  function accountsWith(
    account: AdminAccount,
  ): ReturnType<typeof createAdminOperations> {
    return withAccounts({
      listAccounts: () =>
        Promise.resolve({ accounts: [account], next_cursor: '' }),
    })
  }

  it('names the account being deleted by its display name', async () => {
    const user = userEvent.setup()
    const operations = accountsWith(
      accountFixture({ id: DELETE_UUID, display_name: 'Ada Lovelace' }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await user.click(
      await screen.findByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: 'Ada Lovelace' }),
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.accounts.deleteTitle,
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.accounts.deleteMessage, { name: 'Ada Lovelace' }),
      ),
    ).toBeInTheDocument()
  })

  it('names the account being deleted by its display name and first identifier', async () => {
    const user = userEvent.setup()
    const operations = accountsWith(
      accountFixture({
        id: DELETE_UUID,
        display_name: 'Ada Lovelace',
        identifiers: [
          { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
          { kind: 'email', value: 'ada@example.com', proven: false, sign_in: false, contact: false, default_contact: false },
        ],
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await user.click(
      await screen.findByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: 'Ada Lovelace (ada)' }),
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.accounts.deleteTitle,
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.accounts.deleteMessageWithIdentifier, {
          name: 'Ada Lovelace',
          identifier: 'ada',
        }),
      ),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(DELETE_UUID)
  })

  it('names the account being deleted by its first identifier when the display name is empty', async () => {
    const user = userEvent.setup()
    const operations = accountsWith(
      accountFixture({
        id: DELETE_UUID,
        display_name: '',
        identifiers: [
          { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
          { kind: 'email', value: 'ada@example.com', proven: false, sign_in: false, contact: false, default_contact: false },
        ],
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await user.click(
      await screen.findByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: 'ada' }),
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.accounts.deleteTitle,
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.accounts.deleteMessageByIdentifier, { identifier: 'ada' }),
      ),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(DELETE_UUID)
  })

  it('keeps the account UUID out of the delete confirmation', async () => {
    const user = userEvent.setup()
    const operations = accountsWith(
      accountFixture({ id: DELETE_UUID, display_name: 'Ada Lovelace' }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await user.click(
      await screen.findByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: 'Ada Lovelace' }),
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.accounts.deleteTitle,
    })
    expect(dialog).not.toHaveTextContent(DELETE_UUID)
  })

  it('keeps the account UUID out of the delete confirmation when the account has no display name', async () => {
    const user = userEvent.setup()
    const operations = accountsWith(
      accountFixture({ id: DELETE_UUID, display_name: '' }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await user.click(
      await screen.findByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: '账户不可用' }),
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.accounts.deleteTitle,
    })
    expect(
      within(dialog).getByText(zhCN.accounts.deleteMessageUnnamed),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(DELETE_UUID)
  })

  it('confirms before resetting MFA and revoking sessions', async () => {
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
    await user.click(await screen.findByRole('button', { name: zhCN.accounts.resetMFA }))
    await confirmDialog(user)
    await user.click(screen.getByRole('button', { name: zhCN.accounts.revokeSessions }))
    await confirmDialog(user)
    await waitForCondition(() => {
      expect(reset).toEqual(['acc-1'])
      expect(revoked).toEqual(['acc-1'])
    })
  })

  it('marks the caller\'s own session and names the sign-out in both confirmations', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountSessions: () =>
        Promise.resolve([
          { ...SESSION_ROW, id: 'sess-current', current: true },
          { ...SESSION_ROW, id: 'sess-other' },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await within(panel).findByText('sess-cur')

    const marker = within(panel).getByText(zhCN.accounts.sessions.current)
    expect(
      marker.closest(`.${chipClasses.root}`)?.classList.contains(
        chipClasses.colorPrimary,
      ),
    ).toBe(true)
    const currentRow = marker.closest('tr') as HTMLElement
    expect(currentRow).toHaveTextContent('sess-cur')
    expect(currentRow).not.toHaveTextContent('sess-oth')
    const currentCells = currentRow.querySelectorAll('td')
    expect(currentCells[0]).not.toHaveTextContent(zhCN.accounts.sessions.current)
    expect(currentCells[1]).toHaveTextContent(zhCN.accounts.sessions.current)
    const otherRow = within(panel).getByText('sess-oth').closest('tr')
    expect(within(otherRow as HTMLElement).queryByText(zhCN.accounts.sessions.current)).toBeNull()

    await user.click(
      within(panel).getByRole('button', { name: zhCN.accounts.revokeSessions }),
    )
    const bulk = await screen.findByRole('dialog', {
      name: zhCN.accounts.revokeSessionsTitle,
    })
    expect(
      within(bulk).getByText(zhCN.accounts.revokeSessionsMessageCurrent),
    ).toBeInTheDocument()
    await user.click(within(bulk).getByRole('button', { name: zhCN.common.cancel }))

    await user.click(
      await within(panel).findByRole('button', {
        name: fill(zhCN.accounts.sessions.revokeAria, {
          id: 'sess-current',
        }),
      }),
    )
    const own = await screen.findByRole('dialog', { name: zhCN.accounts.sessions.revokeTitle })
    expect(
      within(own).getByText(
        fill(zhCN.accounts.sessions.revokeMessageCurrent, {
          id: 'sess-current',
        }),
      ),
    ).toBeInTheDocument()
    await user.click(within(own).getByRole('button', { name: zhCN.common.cancel }))

    await user.click(
      await within(panel).findByRole('button', {
        name: fill(zhCN.accounts.sessions.revokeAria, { id: 'sess-other' }),
      }),
    )
    const other = await screen.findByRole('dialog', { name: zhCN.accounts.sessions.revokeTitle })
    expect(
      within(other).getByText(
        fill(zhCN.accounts.sessions.revokeMessage, { id: 'sess-other' }),
      ),
    ).toBeInTheDocument()
  })

  it('confirms before revoking a secret and a session', async () => {
    const user = userEvent.setup()
    const secret: string[] = []
    const session: string[] = []
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: null, revoked: false },
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
            current: false,
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
    await user.click(screen.getByRole('button', { name: REVOKE_SECRET_CI_NAME }))
    await confirmDialog(user)
    await waitForCondition(() => {
      expect(secret).toEqual(['sec-1'])
    })
    await user.click(screen.getByRole('button', { name: REVOKE_SESSION_SESS_1_NAME }))
    await confirmDialog(user)
    await waitForCondition(() => {
      expect(session).toEqual(['sess-1'])
    })
  })

  it('marks only the activated session busy and holds its siblings without a reason', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const calls: string[] = []
    const operations = withAccounts({
      listAccountSessions: () =>
        Promise.resolve([sessionFixture('sess-1'), sessionFixture('sess-2')]),
      revokeSession: (input) => {
        calls.push(input.sessionId)
        return new Promise<void>((resolve) => {
          finish = resolve
        })
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const active = await screen.findByRole('button', {
      name: revokeSessionName('sess-1'),
    })
    const sibling = screen.getByRole('button', {
      name: revokeSessionName('sess-2'),
    })

    await user.click(active)
    await confirmDialog(user)

    await waitFor(() => expect(active).toHaveAttribute('aria-busy', 'true'))
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy')
    expect(sibling).not.toHaveAccessibleDescription()
    await user.click(sibling)
    sibling.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(calls).toEqual(['sess-1'])

    await act(async () => {
      finish?.()
    })
  })

  it('marks only the activated secret busy and holds its siblings without a reason', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const calls: string[] = []
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: null, revoked: false },
          { id: 'sec-2', label: 'prod', expires_at: null, revoked: false },
        ]),
      revokeSecret: (input) => {
        calls.push(input.secret)
        return new Promise<void>((resolve) => {
          finish = resolve
        })
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const active = await screen.findByRole('button', {
      name: revokeSecretName('ci'),
    })
    const sibling = screen.getByRole('button', {
      name: revokeSecretName('prod'),
    })

    await user.click(active)
    await confirmDialog(user)

    await waitFor(() => expect(active).toHaveAttribute('aria-busy', 'true'))
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy')
    expect(sibling).not.toHaveAccessibleDescription()
    await user.click(sibling)
    sibling.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(calls).toEqual(['sec-1'])

    await act(async () => {
      finish?.()
    })
  })

  it('holds a revoked secret with its reason and refuses click and keyboard activation', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          { id: 'sec-1', label: 'ci', expires_at: null, revoked: true },
        ]),
      revokeSecret: (input) => {
        calls.push(input.secret)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const revoke = await screen.findByRole('button', {
      name: revokeSecretName('ci'),
    })

    expect(revoke).toHaveAttribute('aria-disabled', 'true')
    expect(revoke).toHaveAccessibleDescription(
      zhCN.accounts.secrets.revokeUnavailable,
    )
    await user.click(revoke)
    revoke.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(calls).toEqual([])
  })

  it('keeps focus on the row delete control while the delete is in flight', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const operations = withAccounts({
      deleteAccount: () =>
        new Promise<void>((resolve) => {
          finish = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    const remove = screen.getByRole('button', { name: DELETE_ACCOUNT_NAME })

    await deleteRowThroughConfirm(user, 'Ada Lovelace')
    await waitFor(() => expect(remove).toHaveFocus())
    await waitFor(() => expect(remove).toHaveAttribute('aria-busy', 'true'))

    await act(async () => {
      finish?.()
    })
  })

  function listWithoutDeleted(
    rows: readonly AdminAccount[],
    deleted: readonly string[],
  ): () => Promise<{ accounts: AdminAccount[]; next_cursor: string }> {
    return () =>
      Promise.resolve({
        accounts: rows.filter((row) => !deleted.includes(row.id)),
        next_cursor: '',
      })
  }

  it("moves focus to the next row's delete control when a middle row leaves", async () => {
    const user = userEvent.setup()
    const deleted: string[] = []
    const rows = [
      accountFixture({ id: 'acc-a', display_name: 'Ada Lovelace' }),
      accountFixture({ id: 'acc-b', display_name: 'Grace Hopper' }),
      accountFixture({ id: 'acc-c', display_name: 'Cara Diaz' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: listWithoutDeleted(rows, deleted),
        deleteAccount: (input) => {
          deleted.push(input.id)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Grace Hopper')
    const nextRemove = screen.getByRole('button', {
      name: fill(zhCN.accounts.deleteAria, { name: 'Cara Diaz' }),
    })

    await deleteRowThroughConfirm(user, 'Grace Hopper')
    await waitFor(() => expect(nextRemove).toHaveFocus())
  })

  it("moves focus to the previous row's delete control when the last row leaves", async () => {
    const user = userEvent.setup()
    const deleted: string[] = []
    const rows = [
      accountFixture({ id: 'acc-a', display_name: 'Ada Lovelace' }),
      accountFixture({ id: 'acc-b', display_name: 'Grace Hopper' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: listWithoutDeleted(rows, deleted),
        deleteAccount: (input) => {
          deleted.push(input.id)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Grace Hopper')
    const previousRemove = screen.getByRole('button', {
      name: fill(zhCN.accounts.deleteAria, { name: 'Ada Lovelace' }),
    })

    await deleteRowThroughConfirm(user, 'Grace Hopper')
    await waitFor(() => expect(previousRemove).toHaveFocus())
  })

  it('moves focus to the list container when the only row leaves', async () => {
    const user = userEvent.setup()
    const deleted: string[] = []
    const rows = [
      accountFixture({ id: 'acc-a', display_name: 'Ada Lovelace' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: listWithoutDeleted(rows, deleted),
        deleteAccount: (input) => {
          deleted.push(input.id)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada Lovelace')

    await deleteRowThroughConfirm(user, 'Ada Lovelace')
    const container = screen.getByRole('group', {
      name: zhCN.accounts.listRegion,
    })
    await waitFor(() => expect(container).toHaveFocus())
  })

  it('marks only the activated account busy and holds its siblings without a reason', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const calls: string[] = []
    const rows = [
      accountFixture({ id: 'acc-a', display_name: 'Ada Lovelace' }),
      accountFixture({ id: 'acc-b', display_name: 'Grace Hopper' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: rows, next_cursor: '' }),
        deleteAccount: (input) => {
          calls.push(input.id)
          return new Promise<void>((resolve) => {
            finish = resolve
          })
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Grace Hopper')
    const active = screen.getByRole('button', { name: DELETE_ACCOUNT_NAME })
    const sibling = screen.getByRole('button', {
      name: fill(zhCN.accounts.deleteAria, { name: 'Grace Hopper' }),
    })

    await deleteRowThroughConfirm(user, 'Ada Lovelace')

    await waitFor(() => expect(active).toHaveAttribute('aria-busy', 'true'))
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy')
    expect(sibling).not.toHaveAccessibleDescription()
    await user.click(sibling)
    sibling.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(calls).toEqual(['acc-a'])

    await act(async () => {
      finish?.()
    })
  })

  it('does not take focus back when the operator moved it while the delete was in flight', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const deleted: string[] = []
    const rows = [
      accountFixture({ id: 'acc-a', display_name: 'Ada Lovelace' }),
      accountFixture({ id: 'acc-b', display_name: 'Grace Hopper' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: listWithoutDeleted(rows, deleted),
        deleteAccount: (input) => {
          deleted.push(input.id)
          return new Promise<void>((resolve) => {
            finish = resolve
          })
        },
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    await deleteRowThroughConfirm(user, 'Ada Lovelace')
    const elsewhere = screen.getByLabelText(zhCN.accounts.identifierLabel)
    elsewhere.focus()
    expect(elsewhere).toHaveFocus()

    await act(async () => {
      finish?.()
    })
    await waitFor(() => expect(screen.queryByText('Ada Lovelace')).toBeNull())
    expect(elsewhere).toHaveFocus()
  })

  it("moves focus to the next session's revoke control when a middle session leaves", async () => {
    const user = userEvent.setup()
    const rows = [
      sessionFixture('sess-a'),
      sessionFixture('sess-b'),
      sessionFixture('sess-c'),
    ]
    const revoked: string[] = []
    const operations = withAccounts({
      listAccountSessions: sessionsWithoutRevoked(rows, revoked),
      revokeSession: (input) => {
        revoked.push(input.sessionId)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const next = await screen.findByRole('button', {
      name: revokeSessionName('sess-c'),
    })

    await user.click(
      screen.getByRole('button', { name: revokeSessionName('sess-b') }),
    )
    await confirmDialog(user)

    await waitFor(() => expect(next).toHaveFocus())
  })

  it("moves focus to the previous session's revoke control when the last session leaves", async () => {
    const user = userEvent.setup()
    const rows = [sessionFixture('sess-a'), sessionFixture('sess-b')]
    const revoked: string[] = []
    const operations = withAccounts({
      listAccountSessions: sessionsWithoutRevoked(rows, revoked),
      revokeSession: (input) => {
        revoked.push(input.sessionId)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const previous = await screen.findByRole('button', {
      name: revokeSessionName('sess-a'),
    })

    await user.click(
      screen.getByRole('button', { name: revokeSessionName('sess-b') }),
    )
    await confirmDialog(user)

    await waitFor(() => expect(previous).toHaveFocus())
  })

  it('moves focus to the sessions heading when the only session leaves', async () => {
    const user = userEvent.setup()
    const rows = [sessionFixture('sess-a')]
    const revoked: string[] = []
    const operations = withAccounts({
      listAccountSessions: sessionsWithoutRevoked(rows, revoked),
      revokeSession: (input) => {
        revoked.push(input.sessionId)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const heading = await screen.findByRole('heading', {
      name: zhCN.accounts.sessions.title,
    })

    await user.click(
      screen.getByRole('button', { name: revokeSessionName('sess-a') }),
    )
    await confirmDialog(user)

    await waitFor(() => expect(heading).toHaveFocus())
    expect(heading).toHaveAttribute('tabindex', '-1')
  })

  it('does not take focus back when the operator moved it while the session revoke was in flight', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const revoked: string[] = []
    const rows = [sessionFixture('sess-a'), sessionFixture('sess-b')]
    const operations = withAccounts({
      listAccountSessions: sessionsWithoutRevoked(rows, revoked),
      revokeSession: (input) => {
        revoked.push(input.sessionId)
        return new Promise<void>((resolve) => {
          finish = resolve
        })
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await screen.findByRole('button', { name: revokeSessionName('sess-a') })

    await user.click(
      screen.getByRole('button', { name: revokeSessionName('sess-a') }),
    )
    await confirmDialog(user)
    const elsewhere = screen.getByLabelText(
      zhCN.accounts.patch.displayNameLabel,
    )
    elsewhere.focus()
    expect(elsewhere).toHaveFocus()

    await act(async () => {
      finish?.()
    })
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: revokeSessionName('sess-a') }),
      ).toBeNull(),
    )
    expect(elsewhere).toHaveFocus()
  })

  it.each(ALL_DETAIL_WRITES)(
    'renders a $label failure inside the open detail panel, never at page level',
    async ({ operations, drive }) => {
      const user = userEvent.setup()
      const backend = withAccounts(operations(true))
      renderWithProviders(<AccountsPage />, { operations: backend })
      await openDetail(user)
      const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })

      await drive(user, panel)

      const banner = await within(panel).findByRole('alert')
      expect(banner).toHaveTextContent(FORBIDDEN_TEXT)
      expect(outsideOf(panel, '[role="alert"]')).toEqual([])
    },
  )

  it.each(ALL_DETAIL_WRITES)(
    'renders a $label success inside the open detail panel',
    async ({ success, operations, drive }) => {
      const user = userEvent.setup()
      const backend = withAccounts(operations(false))
      renderWithProviders(<AccountsPage />, { operations: backend })
      await openDetail(user)
      const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })

      await drive(user, panel)

      const message = await within(panel).findByText(success)
      expect(message.closest('[role="status"]')).not.toBeNull()
      for (const region of outsideOf(panel, '[role="status"]')) {
        expect(region.textContent).not.toContain(success)
      }
    },
  )
})
