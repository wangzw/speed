/**
 * AccountsPage: the account-administration page.
 *
 * It lists accounts (narrowed by an identifier prefix and its kind, a
 * display-name prefix, a status and a kind),
 * opens one account's detail (its fields, factors, secrets and sessions),
 * creates an account, and drives the detail writes -- status, MFA
 * requirement, patch, password reset, identifier add, secret create,
 * delete, reset MFA, revoke sessions, revoke a secret, revoke a session
 * and remove a named identifier -- through the bundle, each destructive
 * one behind a confirmation.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { AccountsPage } from './AccountsPage.js'
import { consoleOperations } from './operations.js'
import type {
  ConsoleAddIdentifierInput,
  ConsoleCreateAccountInput,
  ConsoleCreateSecretInput,
  ConsolePatchAccountInput,
  ConsoleResetPasswordInput,
  ConsoleSetAccountStatusInput,
  ConsoleSetMFARequiredInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { bindConsoleRequest } from '../test-utils/request-rig.js'
import { createStepUpSession } from '../test-utils/session.js'
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
  await user.click(screen.getByRole('button', { name: '管理账户 acc-1' }))
}

/** The zh-CN label the confirm button shows once the two-step guard arms. */
const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

/** Confirms the open danger dialog (the confirm button labelled 确认), then
 * waits for it to leave the tree so the next interaction is not blocked by
 * the modal's aria-hidden background. */
async function confirmDialog(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: '确认' }))
  await waitFor(() => {
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
}

/** A 401 ApiError as the api-client delivers it: the authn step-up refusal
 * carries no readable envelope, so the client synthesizes client.http.401,
 * and the session discriminates it from a session end by the live snapshot. */
function stepUpRefusal(): {
  readonly status: number
  readonly code: string
  readonly message: string
  readonly auth: boolean
} {
  return { status: 401, code: 'client.http.401', message: 'refused', auth: true }
}

describe('AccountsPage', () => {
  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          {
            id: 'sec-1',
            label: 'ci',
            expires_at: '0001-01-01T00:00:00Z',
            revoked: false,
          },
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
    // The page header refresh is present before any search; the row and
    // detail controls join it once the account is opened.
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: '刷新' }),
      ),
    ).toBe(true)
    await openDetail(user)
    await screen.findByText('ci')
    const gated = [
      screen.getByRole('button', { name: '收起账户 acc-1' }),
      screen.getByRole('button', { name: '删除账户 acc-1' }),
      screen.getByRole('button', { name: '撤销密钥 ci' }),
      screen.getByRole('button', { name: '撤销会话 sess-1' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  }, 20_000)

  it('opens the detail at its heading and toggles it from the row control', async () => {
    const user = userEvent.setup()
    const scrollIntoView = vi.fn()
    const original = Object.getOwnPropertyDescriptor(
      HTMLElement.prototype,
      'scrollIntoView',
    )
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      writable: true,
      value: scrollIntoView,
    })
    try {
      const operations = withAccounts()
      renderWithProviders(<AccountsPage />, { operations })
      await search(user)
      await screen.findByText('Ada Lovelace')
      expect(
        screen.queryByRole('heading', { level: 2, name: '账户「acc-1」' }),
      ).toBeNull()

      await user.click(screen.getByRole('button', { name: '管理账户 acc-1' }))
      // The detail renders below the list and the create form, so opening it
      // must move the viewport and focus to its heading rather than leave the
      // row highlight as the only visible change.
      const heading = await screen.findByRole('heading', {
        level: 2,
        name: '账户「acc-1」',
      })
      expect(heading).toHaveFocus()
      expect(scrollIntoView).toHaveBeenCalledTimes(1)

      // The same control closes the detail, and the row returns to its
      // "Manage" label.
      await user.click(screen.getByRole('button', { name: '收起账户 acc-1' }))
      expect(
        screen.queryByRole('heading', { level: 2, name: '账户「acc-1」' }),
      ).toBeNull()
      expect(
        screen.getByRole('button', { name: '管理账户 acc-1' }),
      ).toBeInTheDocument()
    } finally {
      if (original === undefined) {
        delete (HTMLElement.prototype as { scrollIntoView?: unknown })
          .scrollIntoView
      } else {
        Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', original)
      }
    }
  })

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

  it('keeps its short-value and identifier columns on one line and the identifier reachable in full', async () => {
    // The status cell must not split '已启用' across two lines, a
    // display name with a space must not break at it, and an identifier
    // must not break mid-token. jsdom does no layout, so the proof is
    // the declared `white-space: nowrap` on the rendered cells; the
    // table's TableContainer scroll is what absorbs the resulting width
    // (asserted in ui-kit's DataTable suite). The complete identifier
    // stays reachable without that scroll: the cell carries it as its
    // title and a focusable copy control copies it.
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    const statusCell = (await screen.findByText('已启用')).closest('td')
    const nameCell = screen.getByText('Ada Lovelace').closest('td')
    const idCell = screen.getByText('acc-1').closest('td')
    for (const cell of [statusCell, nameCell, idCell]) {
      expect(cell).not.toBeNull()
      expect(cell).toHaveStyle({ whiteSpace: 'nowrap' })
    }
    expect(idCell).toHaveAttribute('title', 'acc-1')
    expect(
      within(idCell as HTMLElement).getByRole('button', { name: /acc-1/ }),
    ).toBeInTheDocument()
  })

  it('sends the identifier prefix and its kind when the search is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/auth/admin/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: consoleOperations })
    await user.click(screen.getByLabelText('标识类型'))
    await user.click(await screen.findByRole('option', { name: '邮箱' }))
    await user.type(screen.getByLabelText('登录标识'), 'ada')
    await user.click(screen.getByRole('button', { name: '查询' }))
    await vi.waitFor(() => {
      expect(calls).toContainEqual({
        method: 'GET',
        path: '/auth/admin/accounts',
        query: { identifier_prefix: 'ada', identifier_kind: 'email' },
      })
    })
  })

  it('sends the status and kind filters when the search is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/auth/admin/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: consoleOperations })
    await user.click(screen.getByLabelText('状态筛选'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    await user.click(screen.getByLabelText('类型筛选'))
    await user.click(await screen.findByRole('option', { name: '服务' }))
    await user.click(screen.getByRole('button', { name: '查询' }))
    await vi.waitFor(() => {
      expect(calls).toContainEqual({
        method: 'GET',
        path: '/auth/admin/accounts',
        query: { status: 'disabled', kind: 'service' },
      })
    })
  })

  it('sends the display-name prefix when the search is submitted', async () => {
    const user = userEvent.setup()
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/auth/admin/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: consoleOperations })
    await user.type(screen.getByLabelText('显示名前缀'), 'Ada')
    await user.click(screen.getByRole('button', { name: '查询' }))
    await vi.waitFor(() => {
      expect(calls).toContainEqual({
        method: 'GET',
        path: '/auth/admin/accounts',
        query: { display_name_prefix: 'Ada' },
      })
    })
  })

  it('omits the status and kind filters when they are left at all', async () => {
    const user = userEvent.setup()
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/auth/admin/accounts') {
        return { accounts: [], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    renderWithProviders(<AccountsPage />, { operations: consoleOperations })
    await user.click(screen.getByRole('button', { name: '查询' }))
    await vi.waitFor(() => {
      const call = calls.find((entry) => entry.path === '/auth/admin/accounts')
      expect(call).toBeDefined()
      // No filter is set, so the generated call carries no query parameter.
      expect(call?.query ?? {}).toEqual({})
    })
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

  it('surfaces a detail sub-read failure instead of an empty table', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountFactors: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    expect(await screen.findByText('因子加载失败')).toBeInTheDocument()
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
    await user.click(screen.getByRole('button', { name: '删除账户 acc-1' }))
    // The dialog gates the write: nothing fires until it is confirmed.
    expect(deleted).toEqual([])
    // Deleting an account is irreversible and takes dependent records with
    // it, so its confirmation carries the heavier two-step guard: the first
    // click only arms, the deliberate second fires.
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
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
    await vi.waitFor(() => {
      expect(deleted).toEqual(['acc-1'])
    })
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
    await user.click(await screen.findByRole('button', { name: '重置 MFA' }))
    await confirmDialog(user)
    await user.click(screen.getByRole('button', { name: '撤销全部会话' }))
    await confirmDialog(user)
    await vi.waitFor(() => {
      expect(reset).toEqual(['acc-1'])
      expect(revoked).toEqual(['acc-1'])
    })
  })

  it('confirms before revoking a secret and a session', async () => {
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
    // Each revoke control names its own row (the secret's label, the
    // session's id), so the two are picked apart by object, not by order.
    await user.click(screen.getByRole('button', { name: '撤销密钥 ci' }))
    await confirmDialog(user)
    await vi.waitFor(() => {
      expect(secret).toEqual(['sec-1'])
    })
    await user.click(screen.getByRole('button', { name: '撤销会话 sess-1' }))
    await confirmDialog(user)
    await vi.waitFor(() => {
      expect(session).toEqual(['sess-1'])
    })
  })

  it('confirms before removing a named identifier', async () => {
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
    await confirmDialog(user)
    await vi.waitFor(() => {
      expect(removed).toEqual(['ada@example.com'])
    })
  })

  it('creates an account through the bundle', async () => {
    const user = userEvent.setup()
    const created: ConsoleCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await user.type(screen.getByLabelText('显示名'), 'Grace')
    await user.type(screen.getByLabelText('用户名'), 'grace')
    await user.click(screen.getByRole('button', { name: '新建' }))
    await vi.waitFor(() => {
      expect(created).toEqual([
        { kind: 'user', display_name: 'Grace', username: 'grace' },
      ])
    })
    // A landed create clears the form, so the next one starts empty.
    await vi.waitFor(() => {
      expect(screen.getByLabelText('显示名')).toHaveValue('')
      expect(screen.getByLabelText('用户名')).toHaveValue('')
    })
  })

  it('keeps the create submit disabled until a display name and an identifier are given', async () => {
    const user = userEvent.setup()
    const created: ConsoleCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    const { container } = renderWithProviders(<AccountsPage />, { operations })
    const submit = screen.getByRole('button', { name: '新建' })
    const form = container.querySelector('form') as HTMLFormElement

    // An empty form names nothing an account can sign in with: the submit
    // is disabled, and a submit of the form itself sends no request either
    // -- the disabled button is not the only guard.
    expect(submit).toBeDisabled()
    fireEvent.submit(form)
    expect(created).toEqual([])

    // A display name alone is not an account either.
    await user.type(screen.getByLabelText('显示名'), 'Grace')
    expect(submit).toBeDisabled()
    fireEvent.submit(form)
    expect(created).toEqual([])

    // Nor is an identifier alone.
    await user.clear(screen.getByLabelText('显示名'))
    await user.type(screen.getByLabelText('用户名'), 'grace')
    expect(submit).toBeDisabled()
    fireEvent.submit(form)
    expect(created).toEqual([])

    // Both together make the account creatable.
    await user.type(screen.getByLabelText('显示名'), 'Grace')
    await vi.waitFor(() => expect(submit).toBeEnabled())
    await user.click(submit)
    await vi.waitFor(() => expect(created).toHaveLength(1))
  })

  it('creates an identifier-less service account from a display name alone', async () => {
    const user = userEvent.setup()
    const created: ConsoleCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const submit = screen.getByRole('button', { name: '新建' })

    // A service account signs in with a service secret, so the server
    // exempts it from the login-identifier requirement, and the submit must
    // be enabled on a display name alone before any identifier is given.
    await user.click(screen.getByLabelText('账户类型'))
    await user.click(await screen.findByRole('option', { name: '服务' }))
    await user.type(screen.getByLabelText('显示名'), 'Nightly')
    await vi.waitFor(() => expect(submit).toBeEnabled())
    await user.click(submit)
    await vi.waitFor(() => {
      expect(created).toEqual([{ kind: 'service', display_name: 'Nightly' }])
    })
  })

  it('lays the create fields out through the form family instead of one crowded row', async () => {
    const operations = withAccounts()
    const { container } = renderWithProviders(<AccountsPage />, { operations })
    // The create fields flow through ui-kit's FormLayout: a real <form>
    // whose field flow is a grid, so the many fields wrap into rows
    // rather than crowding a single flex line.
    const form = container.querySelector('form')
    expect(form).not.toBeNull()
    expect(form).toHaveStyle({ display: 'grid' })
    expect(within(form as HTMLElement).getByLabelText('显示名')).toBeInTheDocument()
  })

  it('changes an account status through the bundle', async () => {
    const user = userEvent.setup()
    const calls: ConsoleSetAccountStatusInput[] = []
    const operations = withAccounts({
      setAccountStatus: (input) => {
        calls.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    // The status control is seeded from the account (active), so the save
    // is disabled until the operator picks a different status: a stray
    // click can never disable an account the operator did not mean to.
    const save = await screen.findByRole('button', { name: '保存状态' })
    expect(save).toBeDisabled()
    await user.click(screen.getByLabelText('状态'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    await user.click(save)
    await vi.waitFor(() => {
      expect(calls).toEqual([{ id: 'acc-1', status: 'disabled' }])
    })
  })

  it('announces a landed status change with a success notice', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      setAccountStatus: () => Promise.resolve(accountFixture()),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.click(screen.getByLabelText('状态'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    await user.click(screen.getByRole('button', { name: '保存状态' }))
    // The landed write fills the page's standing status region, so a
    // successful save is confirmed rather than silent.
    expect(await screen.findByText('状态已保存')).toBeInTheDocument()
  })

  it('names each account row control with its own account', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              accountFixture({ id: 'acc-1', display_name: 'Ada' }),
              accountFixture({ id: 'acc-2', display_name: 'Grace' }),
            ],
            next_cursor: '',
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await screen.findByText('Ada')

    // The two rows' manage/delete controls carry the account id in their
    // accessible name -- no two identical "Delete" buttons.
    expect(
      screen.getByRole('button', { name: '管理账户 acc-1' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '删除账户 acc-2' }),
    ).toBeInTheDocument()
  })

  it('opens the step-up dialog on a refused write and replays it once elevated', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const startFlow = vi.spyOn(session, 'startAuthenticatedFlow')
    const calls: ConsoleSetAccountStatusInput[] = []
    let attempts = 0
    const operations = withAccounts({
      setAccountStatus: (input) => {
        calls.push(input)
        attempts += 1
        // The first write is refused for a strength shortfall (a 401 while
        // the session is still established); the replay after elevation
        // succeeds.
        return attempts === 1 ? Promise.reject(stepUpRefusal()) : Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations, session })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: '保存状态' })
    await user.click(screen.getByLabelText('状态'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    await user.click(save)
    // The refusal opens the re-auth dialog rather than a plain error, and
    // the write is replayed exactly once.
    await vi.waitFor(() => {
      expect(startFlow).toHaveBeenCalled()
    })
    await vi.waitFor(() => {
      expect(calls).toEqual([
        { id: 'acc-1', status: 'disabled' },
        { id: 'acc-1', status: 'disabled' },
      ])
    })
    // The step-up elevated the session; it did not sign the operator out.
    expect(session.getSnapshot().state).toBe('authenticated')
  })

  it('does not replay a 401 when the session itself ended', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    session.signOut()
    const startFlow = vi.spyOn(session, 'startAuthenticatedFlow')
    const calls: ConsoleSetAccountStatusInput[] = []
    const operations = withAccounts({
      setAccountStatus: (input) => {
        calls.push(input)
        return Promise.reject(stepUpRefusal())
      },
    })
    renderWithProviders(<AccountsPage />, { operations, session })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: '保存状态' })
    await user.click(screen.getByLabelText('状态'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    await user.click(save)
    // A 401 on a session that already ended is not a strength shortfall:
    // the write fires once, no re-auth dialog opens, and no replay happens.
    await vi.waitFor(() => {
      expect(calls.length).toBe(1)
    })
    expect(startFlow).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('sets the MFA requirement through the bundle', async () => {
    const user = userEvent.setup()
    const calls: ConsoleSetMFARequiredInput[] = []
    const operations = withAccounts({
      setMFARequired: (input) => {
        calls.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    // The control is seeded from the account (not required), so the save is
    // disabled until the requirement changes.
    const save = await screen.findByRole('button', { name: '保存要求' })
    expect(save).toBeDisabled()
    await user.click(screen.getByLabelText('MFA 要求'))
    await user.click(await screen.findByRole('option', { name: '必须' }))
    await user.click(save)
    await vi.waitFor(() => {
      expect(calls).toEqual([{ id: 'acc-1', required: true }])
    })
  })

  it('patches an account through the bundle', async () => {
    const user = userEvent.setup()
    const calls: ConsolePatchAccountInput[] = []
    const operations = withAccounts({
      patchAccount: (input) => {
        calls.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    // The display-name field is seeded from the account, and the save is
    // disabled until it changes.
    const name = await screen.findByLabelText('新显示名')
    expect(name).toHaveValue('Operator')
    expect(screen.getByRole('button', { name: '保存改动' })).toBeDisabled()
    await user.clear(name)
    await user.type(name, 'Ada II')
    await user.click(screen.getByRole('button', { name: '保存改动' }))
    await vi.waitFor(() => {
      expect(calls).toEqual([{ id: 'acc-1', display_name: 'Ada II' }])
    })
  })

  it('does not write the protected flag for the control\'s unchanged sentinel', async () => {
    const user = userEvent.setup()
    const calls: ConsolePatchAccountInput[] = []
    const operations = withAccounts({
      // A protected account: selecting the 「保持不变」 sentinel must not
      // write the opposite (protected:false), which would silently drop the
      // account's impersonation guard.
      getAccount: () => Promise.resolve(accountFixture({ protected: true })),
      patchAccount: (input) => {
        calls.push(input)
        return Promise.resolve(accountFixture({ protected: true }))
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    // Move the display name so the save fires; leave the protected control at
    // its sentinel.
    const name = await screen.findByLabelText('新显示名')
    await user.clear(name)
    await user.type(name, 'Ada II')
    await user.click(screen.getByLabelText('受保护'))
    await user.click(await screen.findByRole('option', { name: '保持不变' }))
    await user.click(screen.getByRole('button', { name: '保存改动' }))
    await vi.waitFor(() => {
      expect(calls).toEqual([{ id: 'acc-1', display_name: 'Ada II' }])
    })
  })

  it('resets a password through the bundle', async () => {
    const user = userEvent.setup()
    const calls: ConsoleResetPasswordInput[] = []
    const operations = withAccounts({
      resetPassword: (input) => {
        calls.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.type(await screen.findByLabelText('临时口令'), 'secret123')
    await user.click(screen.getByRole('button', { name: '重置口令' }))
    await vi.waitFor(() => {
      expect(calls).toEqual([{ id: 'acc-1', password: 'secret123' }])
    })
  })

  it('adds an identifier through the bundle', async () => {
    const user = userEvent.setup()
    const calls: ConsoleAddIdentifierInput[] = []
    const operations = withAccounts({
      addIdentifier: (input) => {
        calls.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.type(await screen.findByLabelText('取值'), 'ada@example.com')
    await user.click(screen.getByRole('button', { name: '新增标识' }))
    await vi.waitFor(() => {
      expect(calls).toEqual([
        { id: 'acc-1', kind: 'username', value: 'ada@example.com' },
      ])
    })
  })

  it('creates a service secret and shows the one-time value', async () => {
    const user = userEvent.setup()
    const calls: ConsoleCreateSecretInput[] = []
    const operations = withAccounts({
      createSecret: (input) => {
        calls.push(input)
        return Promise.resolve({
          id: 'secret-9',
          label: input.label,
          secret: 'one-time-value',
          expires_at: '0001-01-01T00:00:00Z',
        })
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.type(await screen.findByLabelText('标签'), 'ci')
    await user.click(screen.getByRole('button', { name: '新建密钥' }))
    await vi.waitFor(() => {
      expect(calls).toEqual([{ id: 'acc-1', label: 'ci' }])
    })
    expect(
      await screen.findByText('密钥（仅显示这一次）：one-time-value'),
    ).toBeInTheDocument()
  })

  it('renders the retryable error placeholder when the list read fails', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccounts: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(await screen.findByText('账户加载失败')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the list read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccounts: () => Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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
