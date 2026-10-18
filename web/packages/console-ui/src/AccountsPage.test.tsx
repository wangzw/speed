/**
 * AccountsPage: the account-administration page.
 *
 * It lists accounts (narrowed by an identifier prefix and its kind, a
 * display-name prefix, a status and a kind), opens one account's detail in
 * a wide side panel (its fields, factors, secrets and sessions), creates an
 * account in a second wide panel, and drives the detail writes -- status,
 * MFA requirement, patch, password reset, identifier add, secret create,
 * delete, reset MFA, revoke sessions, revoke a secret, revoke a session
 * and remove a named identifier -- through the bundle, each destructive
 * one behind a confirmation.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { AccountsPage } from './AccountsPage.js'
import { consoleOperations } from './operations.js'
import type {
  ConsoleAccount,
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
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
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

/** The zh-CN label of the side panel's built-in close control. */
const PANEL_CLOSE_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly sidePanel: { readonly closeLabel: string }
  }
).sidePanel.closeLabel

/** The create panel's heading, which is also its accessible name. */
const CREATE_PANEL_NAME = '新建账户'

/** The discard confirmation's heading. */
const DISCARD_DIALOG_NAME = '放弃未保存的更改'

/** Opens the create panel from the page header and returns its dialog. */
async function openCreate(
  user: ReturnType<typeof userEvent.setup>,
): Promise<HTMLElement> {
  await user.click(screen.getByRole('button', { name: CREATE_PANEL_NAME }))
  return screen.findByRole('dialog', { name: CREATE_PANEL_NAME })
}

/**
 * The body of the `@container (min-width:600px)` rule emotion emitted for
 * `element`'s own class, or undefined when the element carries no such
 * rule. Scoping to the element's class keeps the assertion proof for this
 * form rather than for any earlier render whose styles are still injected.
 */
function elementContainerRule(
  css: string,
  element: Element,
): string | undefined {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return undefined
  }
  const escaped = emotionClass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(
    `@container \\(min-width:600px\\)\\{\\.${escaped}\\{([^}]*)\\}\\}`,
  ).exec(css)?.[1]
}

/** The `width` `element` declares inside `@media (min-width:<minWidth>px)`. */
function elementWidthAt(
  css: string,
  element: Element,
  minWidth: number,
): string | undefined {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return undefined
  }
  const escaped = emotionClass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const rule = new RegExp(
    `@media \\(min-width:${minWidth}px\\)\\{\\.${escaped}\\{([^}]*)\\}\\}`,
  ).exec(css)?.[1]
  return rule === undefined ? undefined : /(?:^|;)width:([^;]+)/.exec(rule)?.[1]
}

/**
 * Every declaration block emotion emitted for `element`'s own class, base
 * rule and breakpoint/container overrides alike, so a caller can find a
 * declaration without pinning which rule carries it.
 */
function elementDeclarations(css: string, element: Element): string {
  const emotionClass = Array.from(element.classList).find((name) =>
    name.startsWith('css-'),
  )
  if (emotionClass === undefined) {
    return ''
  }
  const escaped = emotionClass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const matches = css.matchAll(new RegExp(`\\.${escaped}\\{([^}]*)\\}`, 'g'))
  return Array.from(matches, (match) => match[1] ?? '').join('\n')
}

/** Confirms the open danger dialog (the confirm button labelled 确认), then
 * waits for it to leave the tree so the next interaction is not blocked by
 * the modal's aria-hidden background. */
async function confirmDialog(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  // The page may carry an open side panel (itself a dialog), so the
  // confirmation is picked out by its own confirm button rather than by
  // being the only dialog in the tree.
  const dialog = await waitFor(() => {
    const candidate = screen
      .getAllByRole('dialog')
      .find(
        (entry) =>
          within(entry).queryByRole('button', { name: '确认' }) !== null,
      )
    expect(candidate).toBeDefined()
    return candidate as HTMLElement
  })
  await user.click(within(dialog).getByRole('button', { name: '确认' }))
  await waitFor(() => {
    expect(
      screen.queryByRole('button', { name: '确认' }),
    ).not.toBeInTheDocument()
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

/** A 403 ApiError carrying a code the console's error text lists, so the
 * failure banner has a stable line to assert on. */
const FORBIDDEN = {
  status: 403,
  code: 'authn.forbidden',
  message: 'forbidden',
}

const FORBIDDEN_TEXT = '你没有执行此操作的权限。'

const SECRET_ROW = {
  id: 'sec-1',
  label: 'ci',
  expires_at: '0001-01-01T00:00:00Z',
  revoked: false,
}

const SESSION_ROW = {
  id: 'sess-1',
  kind: 'refresh',
  assurance: 1,
  auth_time: '2026-10-15T12:00:00Z',
  created_at: '2026-10-15T12:00:00Z',
  last_seen_at: '2026-10-15T12:00:00Z',
  expires_at: '2026-10-16T12:00:00Z',
  revoked: false,
  current: false,
}

/** Every element matching `selector` outside the panel: the page-level
 * feedback regions live there, behind the open panel's aria-hidden. */
function outsideOf(panel: HTMLElement, selector: string): Element[] {
  return Array.from(document.querySelectorAll(selector)).filter(
    (element) => !panel.contains(element),
  )
}

type WriteDriver = (
  user: ReturnType<typeof userEvent.setup>,
  panel: HTMLElement,
) => Promise<void>

/** The five detail writes whose feedback moved into the panel: each case
 * builds the operations for a failing or a succeeding run, and knows how to
 * drive the write from inside the open detail panel. */
const DETAIL_WRITES: readonly {
  readonly label: string
  readonly success: string
  readonly operations: (fail: boolean) => Partial<ConsoleBackend>
  readonly drive: WriteDriver
}[] = [
  {
    label: 'reset all MFA factors',
    success: '账户「acc-1」的认证因子已重置',
    operations: (fail) => ({
      resetMFA: () => (fail ? Promise.reject(FORBIDDEN) : Promise.resolve()),
    }),
    drive: async (user, panel) => {
      await user.click(within(panel).getByRole('button', { name: '重置 MFA' }))
      await confirmDialog(user)
    },
  },
  {
    label: 'revoke all sessions',
    success: '账户「acc-1」的会话已全部撤销',
    operations: (fail) => ({
      revokeAccountSessions: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.click(
        within(panel).getByRole('button', { name: '撤销全部会话' }),
      )
      await confirmDialog(user)
    },
  },
  {
    label: 'revoke one service secret',
    success: '账户「acc-1」的服务密钥「ci」已撤销',
    operations: (fail) => ({
      listAccountSecrets: () => Promise.resolve([SECRET_ROW]),
      revokeSecret: () => (fail ? Promise.reject(FORBIDDEN) : Promise.resolve()),
    }),
    drive: async (user, panel) => {
      await within(panel).findByText('ci')
      await user.click(within(panel).getByRole('button', { name: '撤销密钥 ci' }))
      await confirmDialog(user)
    },
  },
  {
    label: 'revoke one session',
    success: '账户「acc-1」的会话「sess-1」已撤销',
    operations: (fail) => ({
      listAccountSessions: () => Promise.resolve([SESSION_ROW]),
      revokeSession: () => (fail ? Promise.reject(FORBIDDEN) : Promise.resolve()),
    }),
    drive: async (user, panel) => {
      await within(panel).findByText('sess-1')
      await user.click(
        within(panel).getByRole('button', { name: '撤销会话 sess-1' }),
      )
      await confirmDialog(user)
    },
  },
  {
    label: 'remove one login identifier by name',
    success: '账户「acc-1」的登录标识「ada@example.com」已移除',
    operations: (fail) => ({
      removeIdentifier: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.type(
        within(panel).getByLabelText('移除登录标识'),
        'ada@example.com',
      )
      await user.click(within(panel).getByRole('button', { name: '移除' }))
      await confirmDialog(user)
    },
  },
]

/** The six field-level writes of the detail's account-level section: each is
 * its own control group and its own feedback scope, with the region directly
 * under the group. */
const FIELD_WRITES: readonly {
  readonly label: string
  readonly success: string
  readonly operations: (fail: boolean) => Partial<ConsoleBackend>
  readonly drive: WriteDriver
}[] = [
  {
    label: 'change the account status',
    success: '账户「acc-1」的状态已保存',
    operations: (fail) => ({
      setAccountStatus: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(accountFixture()),
    }),
    drive: async (user, panel) => {
      await user.click(within(panel).getByLabelText('状态'))
      await user.click(await screen.findByRole('option', { name: '已停用' }))
      await user.click(within(panel).getByRole('button', { name: '保存状态' }))
    },
  },
  {
    label: 'change the MFA requirement',
    success: '账户「acc-1」的 MFA 要求已保存',
    operations: (fail) => ({
      setMFARequired: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.click(within(panel).getByLabelText('MFA 要求'))
      await user.click(await screen.findByRole('option', { name: '必须' }))
      await user.click(within(panel).getByRole('button', { name: '保存要求' }))
    },
  },
  {
    label: 'patch the account',
    success: '账户「acc-1」的资料已保存',
    operations: (fail) => ({
      patchAccount: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(accountFixture()),
    }),
    drive: async (user, panel) => {
      const name = within(panel).getByLabelText('新显示名')
      await user.clear(name)
      await user.type(name, 'Ada II')
      await user.click(within(panel).getByRole('button', { name: '保存改动' }))
    },
  },
  {
    label: 'reset the password',
    success: '账户「acc-1」的口令已重置',
    operations: (fail) => ({
      resetPassword: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.type(within(panel).getByLabelText('临时口令'), 'secret123')
      await user.click(within(panel).getByRole('button', { name: '重置口令' }))
    },
  },
  {
    label: 'add a login identifier',
    success: '账户「acc-1」已添加登录标识「ada@example.com」',
    operations: (fail) => ({
      addIdentifier: () =>
        fail ? Promise.reject(FORBIDDEN) : Promise.resolve(),
    }),
    drive: async (user, panel) => {
      await user.type(within(panel).getByLabelText('取值'), 'ada@example.com')
      await user.click(within(panel).getByRole('button', { name: '新增标识' }))
    },
  },
  {
    label: 'create a service secret',
    success: '账户「acc-1」已创建服务密钥「ci」',
    operations: (fail) => ({
      createSecret: () =>
        fail
          ? Promise.reject(FORBIDDEN)
          : Promise.resolve({
              id: 'secret-9',
              label: 'ci',
              secret: 'one-time-value',
              expires_at: '0001-01-01T00:00:00Z',
            }),
    }),
    drive: async (user, panel) => {
      await user.type(within(panel).getByLabelText('标签'), 'ci')
      await user.click(within(panel).getByRole('button', { name: '新建密钥' }))
    },
  },
]

/** Both tables drive one write per control group through the panel. */
const ALL_DETAIL_WRITES = [...DETAIL_WRITES, ...FIELD_WRITES]

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
            current: false,
          },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations })
    // The page header actions are present before any search; the panel's
    // controls join them once it is open.
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: '刷新' }),
      ),
    ).toBe(true)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: CREATE_PANEL_NAME }),
      ),
    ).toBe(true)
    await search(user)
    await screen.findByText('Ada Lovelace')
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: '管理账户 acc-1' }),
      ),
    ).toBe(true)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: '删除账户 acc-1' }),
      ),
    ).toBe(true)
    await user.click(screen.getByRole('button', { name: '管理账户 acc-1' }))
    await screen.findByText('ci')
    const gated = [
      screen.getByRole('button', { name: PANEL_CLOSE_LABEL }),
      screen.getByRole('button', { name: '撤销密钥 ci' }),
      screen.getByRole('button', { name: '撤销会话 sess-1' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  }, 20_000)

  it('opens the account detail in a dialog instead of expanding the row', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    // Nothing detail-shaped is inline before the panel opens.
    expect(
      screen.queryByRole('heading', { level: 2, name: '账户「acc-1」' }),
    ).toBeNull()
    const manage = screen.getByRole('button', { name: '管理账户 acc-1' })
    expect(manage).toHaveAttribute('aria-haspopup', 'dialog')

    await user.click(manage)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    // Both panels take the `wide` tier: 52rem from the viewport's `sm` up.
    expect(elementWidthAt(emittedStyleText(), panel, 600)).toBe('52rem')
    // The title names the account, and the detail's sections live inside the
    // dialog -- not in the page flow below the table.
    expect(
      within(panel).getByRole('heading', { level: 2, name: '账户「acc-1」' }),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('heading', { name: '账户写入' }),
    ).toBeInTheDocument()
    // The row control only opens: no in-page collapse control exists.
    expect(
      screen.queryByRole('button', { name: '收起账户 acc-1' }),
    ).not.toBeInTheDocument()
  })

  it('keeps the selected account rendered until the panel has exited', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

    // Request the close. The panel is still sliding out, and during the
    // transition it must still render the account: clearing the selection
    // on the close request would leave a blank panel.
    fireEvent.click(within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }))
    const exiting = screen.getByRole('dialog', { name: '账户「acc-1」' })
    expect(within(exiting).getByText('显示名：Operator')).toBeInTheDocument()

    // Only the exit transition's end removes the panel (and the account).
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: '账户「acc-1」' }),
      ).not.toBeInTheDocument()
    })
  })

  it('opens the create panel from the header with two columns and an actions-row submit', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    const { container } = renderWithProviders(<AccountsPage />, {
      operations,
    })
    // The create form is not part of the page flow any more.
    expect(container.querySelector('form')).toBeNull()

    const panel = await openCreate(user)
    expect(panel).toHaveAttribute('aria-modal', 'true')
    // The create panel takes the `wide` tier as well.
    expect(elementWidthAt(emittedStyleText(), panel, 600)).toBe('52rem')
    const form = panel.querySelector('form')
    expect(form).not.toBeNull()
    // The fields flow through FormLayout's two-column grid, which the
    // panel's own container width keys (one column below `sm`).
    expect(form).toHaveStyle({ display: 'grid' })
    const css = emittedStyleText()
    expect(elementContainerRule(css, form as Element)).toMatch(
      /grid-template-columns:\s*repeat\(2,\s*1fr\)/,
    )
    // The submit sits in FormLayout's actions row, which spans both tracks
    // instead of taking a field cell.
    const submit = within(panel).getByRole('button', { name: '新建' })
    const actions = submit.parentElement as HTMLElement
    expect(actions).toHaveStyle({ justifyContent: 'flex-end' })
    expect(elementDeclarations(css, actions)).toContain('grid-column:1/-1')
  })

  it('asks before closing a modified create form and closes an untouched one directly', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })

    // Untouched: the close request goes straight out, with no dialog.
    let panel = await openCreate(user)
    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    expect(
      screen.queryByRole('dialog', { name: DISCARD_DIALOG_NAME }),
    ).not.toBeInTheDocument()
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })

    // Modified: the close request asks first. Cancelling keeps the panel
    // and the typed values.
    panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(within(confirm).getByRole('button', { name: '取消' }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DISCARD_DIALOG_NAME }),
      ).not.toBeInTheDocument()
    })
    panel = screen.getByRole('dialog', { name: CREATE_PANEL_NAME })
    expect(within(panel).getByLabelText('显示名')).toHaveValue('Grace')

    // Confirming discards and closes.
    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const again = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(within(again).getByRole('button', { name: '放弃' }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
  })

  it('marks the create panel busy while submitting and ignores close requests', async () => {
    const user = userEvent.setup()
    let finishCreate: (() => void) | undefined
    const operations = withAccounts({
      createAccount: () =>
        new Promise((resolve) => {
          finishCreate = () => resolve(accountFixture())
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    await user.click(within(panel).getByRole('button', { name: '新建' }))

    // In flight: the panel is busy, so the close control is disabled and
    // Escape reports no close request at all.
    const close = within(panel).getByRole('button', {
      name: PANEL_CLOSE_LABEL,
    })
    await waitFor(() => expect(close).toBeDisabled())
    fireEvent.keyDown(panel, { key: 'Escape' })
    expect(
      screen.getByRole('dialog', { name: CREATE_PANEL_NAME }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('dialog', { name: DISCARD_DIALOG_NAME }),
    ).not.toBeInTheDocument()

    await act(async () => {
      finishCreate?.()
    })
  })

  it('starts the create form at its initial values each time the panel opens', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })

    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(within(confirm).getByRole('button', { name: '放弃' }))
    // Both the confirmation and the panel must finish exiting before the
    // page behind is reachable again.
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DISCARD_DIALOG_NAME }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })

    // The re-opened panel shows the initial values, not the discarded draft.
    const reopened = await openCreate(user)
    expect(within(reopened).getByLabelText('显示名')).toHaveValue('')
    expect(within(reopened).getByLabelText('用户名')).toHaveValue('')
  })

  it('returns focus into the panel when the nested confirmation dialog closes', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    const reset = within(panel).getByRole('button', { name: '重置 MFA' })

    await user.click(reset)
    const confirm = await screen.findByRole('dialog', { name: '重置 MFA' })
    await user.click(within(confirm).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(reset).toHaveFocus())
  })

  it('returns focus into the panel when the nested step-up dialog closes', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const operations = withAccounts({
      setAccountStatus: () => Promise.reject(stepUpRefusal()),
    })
    renderWithProviders(<AccountsPage />, { operations, session })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.click(within(panel).getByLabelText('状态'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    const save = within(panel).getByRole('button', { name: '保存状态' })

    await user.click(save)
    const challenge = await screen.findByRole('dialog', {
      name: '需要重新验证',
    })
    // The refusal handed to the gate settles null: the status group's slot
    // stays empty, so no failure banner stands beside the step-up dialog.
    // The control leaves its busy state when the attempt settles, which is
    // also the moment a wrongly settled failure would render.
    await waitFor(() => expect(save).toBeEnabled())
    // Let the rejected attempt's promise chain settle before judging: the
    // dialog opens inside the failure handler, ahead of the attempt's own
    // settlement.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    // The step-up dialog hides the panel from assistive technology, so the
    // assertion reads the panel's DOM directly: a wrongly settled failure
    // still renders its alert there and must show up here.
    expect(panel.querySelectorAll('[role="alert"]')).toHaveLength(0)
    await user.click(within(challenge).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(save).toHaveFocus())
  })

  it('keeps the panel content reachable while the page behind is hidden', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    const { container } = renderWithProviders(<AccountsPage />, {
      operations,
    })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

    // The modal hides the page behind from assistive technology; the panel
    // is the part that stays in the accessibility tree.
    expect(container).toHaveAttribute('aria-hidden', 'true')
    expect(
      within(panel).getByRole('heading', { name: '已注册因子' }),
    ).toBeInTheDocument()
  })

  it('has no axe violations with the detail panel open', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await expectNoAxeViolations()
  })

  it('has no axe violations with the one-time secret displayed', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.type(within(panel).getByLabelText('标签'), 'ci')
    await user.click(within(panel).getByRole('button', { name: '新建密钥' }))
    await within(panel).findByText(
      '服务密钥「label」（仅显示这一次）：one-time',
    )
    await expectNoAxeViolations()
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
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await within(panel).findByText('sess-cur')

    // The marker sits on the current row only, in the primary identity
    // (created time) rather than the low-priority id column, so it stays
    // visible when that column hides. The id cell shows the prefix; the
    // complete id stays on the title and copy control.
    const marker = within(panel).getByText('当前会话')
    const currentRow = marker.closest('tr') as HTMLElement
    expect(currentRow).toHaveTextContent('sess-cur')
    expect(currentRow).not.toHaveTextContent('sess-oth')
    const currentCells = currentRow.querySelectorAll('td')
    expect(currentCells[0]).not.toHaveTextContent('当前会话')
    expect(currentCells[1]).toHaveTextContent('当前会话')
    const otherRow = within(panel).getByText('sess-oth').closest('tr')
    expect(within(otherRow as HTMLElement).queryByText('当前会话')).toBeNull()

    // Revoking everything says the caller is signed out too.
    await user.click(
      within(panel).getByRole('button', { name: '撤销全部会话' }),
    )
    const bulk = await screen.findByRole('dialog', { name: '撤销全部会话' })
    expect(
      within(bulk).getByText(/其中包括你当前的会话/),
    ).toBeInTheDocument()
    await user.click(within(bulk).getByRole('button', { name: '取消' }))

    // The current row's own revocation says the same.
    await user.click(
      await within(panel).findByRole('button', {
        name: '撤销会话 sess-current',
      }),
    )
    const own = await screen.findByRole('dialog', { name: '撤销会话' })
    expect(
      within(own).getByText(
        '撤销会话「sess-current」。这是你当前的会话，撤销后你也会退出登录。',
      ),
    ).toBeInTheDocument()
    await user.click(within(own).getByRole('button', { name: '取消' }))

    // A row the server does not mark keeps the plain confirmation.
    await user.click(
      await within(panel).findByRole('button', {
        name: '撤销会话 sess-other',
      }),
    )
    const other = await screen.findByRole('dialog', { name: '撤销会话' })
    expect(
      within(other).getByText('撤销会话「sess-other」。'),
    ).toBeInTheDocument()
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

  it.each(ALL_DETAIL_WRITES)(
    'renders a $label failure inside the open detail panel, never at page level',
    async ({ operations, drive }) => {
      const user = userEvent.setup()
      const backend = withAccounts(operations(true))
      renderWithProviders(<AccountsPage />, { operations: backend })
      await openDetail(user)
      const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

      await drive(user, panel)

      // The failure banner renders beside the section that fired the write,
      // inside the dialog the operator can still reach.
      const banner = await within(panel).findByRole('alert')
      expect(banner).toHaveTextContent(FORBIDDEN_TEXT)
      // The page-level region is not used for a write initiated here; while
      // the panel is open it is also aria-hidden and would be unreachable.
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
      const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

      await drive(user, panel)

      const message = await within(panel).findByText(success)
      // The confirmation fills a polite live region inside the panel rather
      // than the page-level one behind the modal.
      expect(message.closest('[role="status"]')).not.toBeNull()
      for (const region of outsideOf(panel, '[role="status"]')) {
        expect(region.textContent).not.toContain(success)
      }
    },
  )

  it('keeps the detail panel busy while any of its scopes is in flight', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const operations = withAccounts({
      resetPassword: () =>
        new Promise<void>((resolve) => {
          finish = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.type(within(panel).getByLabelText('临时口令'), 'secret123')
    await user.click(within(panel).getByRole('button', { name: '重置口令' }))

    // A write in one section makes the whole panel busy: the close control
    // is disabled and Escape reports no close request at all.
    await waitFor(() =>
      expect(
        within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
      ).toBeDisabled(),
    )
    fireEvent.keyDown(panel, { key: 'Escape' })
    expect(
      screen.getByRole('dialog', { name: '账户「acc-1」' }),
    ).toBeInTheDocument()

    await act(async () => {
      finish?.()
    })
    await waitFor(() =>
      expect(
        within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
      ).toBeEnabled(),
    )
    fireEvent.click(within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: '账户「acc-1」' }),
      ).not.toBeInTheDocument()
    })
  })

  it('keeps each control group\'s outcome in its own region', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      setAccountStatus: () => Promise.reject(FORBIDDEN),
      setMFARequired: () => Promise.resolve(),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

    // The status write fails in its own region.
    await user.click(within(panel).getByLabelText('状态'))
    await user.click(await screen.findByRole('option', { name: '已停用' }))
    await user.click(within(panel).getByRole('button', { name: '保存状态' }))
    expect(await within(panel).findByRole('alert')).toHaveTextContent(FORBIDDEN_TEXT)

    // The MFA write lands in its own region; the status failure stands
    // because the two groups do not share a slot (nothing is aggregated).
    await user.click(within(panel).getByLabelText('MFA 要求'))
    await user.click(await screen.findByRole('option', { name: '必须' }))
    await user.click(within(panel).getByRole('button', { name: '保存要求' }))
    expect(
      await within(panel).findByText('账户「acc-1」的 MFA 要求已保存'),
    ).toBeInTheDocument()
    expect(within(panel).getByRole('alert')).toHaveTextContent(FORBIDDEN_TEXT)
  })

  it('renders a creation failure under the form inside the create panel', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(FORBIDDEN),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    await user.click(within(panel).getByRole('button', { name: '新建' }))

    const alert = await within(panel).findByRole('alert')
    expect(alert).toHaveTextContent(FORBIDDEN_TEXT)
    // The failure belongs to the create panel's own scope, inside the modal;
    // the page behind it carries none.
    expect(outsideOf(panel, '[role="alert"]')).toEqual([])
    // The fields stay for correction and the panel stays open.
    expect(within(panel).getByLabelText('显示名')).toHaveValue('Grace')
    expect(screen.getByRole('dialog', { name: CREATE_PANEL_NAME })).toBe(panel)
  })

  it('writes the creation success below the list only after the panel exits', async () => {
    const user = userEvent.setup()
    let finishCreate: ((account: ConsoleAccount) => void) | undefined
    const operations = withAccounts({
      createAccount: () =>
        new Promise<ConsoleAccount>((resolve) => {
          finishCreate = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    await user.click(within(panel).getByRole('button', { name: '新建' }))

    await act(async () => {
      finishCreate?.(accountFixture({ display_name: 'Grace' }))
    })

    // While the panel is still exiting, the page behind it is aria-hidden:
    // the success may not be written yet.
    expect(
      screen.getByRole('dialog', { name: CREATE_PANEL_NAME }),
    ).toBeInTheDocument()
    expect(screen.queryByText('账户「Grace」已创建')).toBeNull()

    // Only after the exit does it land, in the page's list region.
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
    const notice = await screen.findByText('账户「Grace」已创建')
    expect(notice.closest('[role="dialog"]')).toBeNull()
    expect(notice.closest('[role="status"]')).not.toBeNull()
  })

  it('keeps the last-settled result in the list scope when writes overlap', async () => {
    const user = userEvent.setup()
    let finishDelete: (() => void) | undefined
    let finishCreate: ((account: ConsoleAccount) => void) | undefined
    const operations = withAccounts({
      deleteAccount: () =>
        new Promise<void>((resolve) => {
          finishDelete = resolve
        }),
      createAccount: () =>
        new Promise<ConsoleAccount>((resolve) => {
          finishCreate = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    // Start the row delete and confirm it through the two-step guard; it
    // stays in flight (one attempt in the list scope).
    await user.click(screen.getByRole('button', { name: '删除账户 acc-1' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).toBeNull()
    })

    // While that write is in flight, a create lands in the same scope via
    // the create panel's exit.
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    await user.click(within(panel).getByRole('button', { name: '新建' }))
    await act(async () => {
      finishCreate?.(accountFixture({ display_name: 'Grace' }))
    })
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
    expect(await screen.findByText('账户「Grace」已创建')).toBeInTheDocument()

    // The delete settles last and replaces the create's confirmation: one
    // slot, the last result wins.
    await act(async () => {
      finishDelete?.()
    })
    expect(await screen.findByText('账户「acc-1」已删除')).toBeInTheDocument()
    expect(screen.queryByText('账户「Grace」已创建')).toBeNull()
  })

  it('shows a one-time secret under the create-secret group with a named close control', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createSecret: () =>
        Promise.resolve({
          id: 'secret-9',
          label: 'ci',
          secret: 'one-time-value',
          expires_at: '0001-01-01T00:00:00Z',
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.type(within(panel).getByLabelText('标签'), 'ci')
    await user.click(within(panel).getByRole('button', { name: '新建密钥' }))

    const display = await within(panel).findByText(
      '服务密钥「ci」（仅显示这一次）：one-time-value',
    )
    expect(display.closest('[role="status"]')).not.toBeNull()
    // The explicit close control names the account it belongs to.
    const close = within(panel).getByRole('button', {
      name: '关闭账户「Operator」（acc-1）的服务密钥「ci」显示',
    })
    expect(close).toBeInTheDocument()
    // The slot's success confirmation stands beside the display, not in it;
    // closing the display leaves the confirmation in place.
    expect(
      within(panel).getByText('账户「acc-1」已创建服务密钥「ci」'),
    ).toBeInTheDocument()
    await user.click(close)
    expect(within(panel).queryByText(/仅显示这一次/)).toBeNull()
    expect(
      within(panel).getByText('账户「acc-1」已创建服务密钥「ci」'),
    ).toBeInTheDocument()
  })

  it('keeps the one-time secret through a reset of the creating scope', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const operations = withAccounts({
      createSecret: () => {
        attempts += 1
        return attempts === 1
          ? Promise.resolve({
              id: 'secret-9',
              label: 'ci',
              secret: 'one-time-value',
              expires_at: '0001-01-01T00:00:00Z',
            })
          : Promise.reject(FORBIDDEN)
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.type(within(panel).getByLabelText('标签'), 'ci')
    await user.click(within(panel).getByRole('button', { name: '新建密钥' }))
    await within(panel).findByText(
      '服务密钥「ci」（仅显示这一次）：one-time-value',
    )

    // A second attempt resets the creating scope and settles as a failure.
    // The display is page state, not slot state: it survives both.
    await user.type(within(panel).getByLabelText('标签'), 'ci')
    await user.click(within(panel).getByRole('button', { name: '新建密钥' }))
    expect(await within(panel).findByRole('alert')).toHaveTextContent(FORBIDDEN_TEXT)
    expect(
      within(panel).getByText('服务密钥「ci」（仅显示这一次）：one-time-value'),
    ).toBeInTheDocument()
    expect(
      within(panel).queryByText('账户「acc-1」已创建服务密钥「ci」'),
    ).toBeNull()
  })

  it('asks before discarding an unclosed secret when the detail panel closes', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createSecret: () =>
        Promise.resolve({
          id: 'secret-9',
          label: 'ci',
          secret: 'one-time-value',
          expires_at: '0001-01-01T00:00:00Z',
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    let panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.type(within(panel).getByLabelText('标签'), 'ci')
    await user.click(within(panel).getByRole('button', { name: '新建密钥' }))
    await within(panel).findByText(
      '服务密钥「ci」（仅显示这一次）：one-time-value',
    )

    // Cancelling the discard keeps the panel and the unclosed secret.
    fireEvent.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    let discard = await screen.findByRole('dialog', {
      name: '放弃未关闭的服务密钥',
    })
    await user.click(within(discard).getByRole('button', { name: '取消' }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: '放弃未关闭的服务密钥' }),
      ).not.toBeInTheDocument()
    })
    panel = screen.getByRole('dialog', { name: '账户「acc-1」' })
    expect(
      within(panel).getByText('服务密钥「ci」（仅显示这一次）：one-time-value'),
    ).toBeInTheDocument()

    // Confirming closes the panel and discards; reopening starts without it.
    fireEvent.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    discard = await screen.findByRole('dialog', {
      name: '放弃未关闭的服务密钥',
    })
    await user.click(within(discard).getByRole('button', { name: '放弃' }))
    // Both the confirmation and the panel must finish exiting before the
    // page behind the modal is reachable again.
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: '放弃未关闭的服务密钥' }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('dialog', { name: '账户「acc-1」' }),
      ).not.toBeInTheDocument()
    })
    await user.click(screen.getByRole('button', { name: '管理账户 acc-1' }))
    panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    expect(within(panel).queryByText(/仅显示这一次/)).toBeNull()
  })

  it('does not warn about a Fragment in the create form\'s select options', async () => {
    const user = userEvent.setup()
    const fragmentWarnings: string[] = []
    const original = console.error
    const spy = vi.spyOn(console, 'error').mockImplementation((...args) => {
      const text = args.map(String).join(' ')
      if (text.includes("doesn't accept a Fragment")) {
        fragmentWarnings.push(text)
        return
      }
      original(...args)
    })
    try {
      renderWithProviders(<AccountsPage />, { operations: withAccounts() })
      const panel = await openCreate(user)
      // The warning belongs to the render that mounts the two
      // verification-source selects, so it is checked as soon as the panel
      // opens -- before the option list below, whose absence is the same
      // defect's user-visible half but a slower, less precise signal.
      expect(fragmentWarnings).toEqual([])
      await user.click(within(panel).getByLabelText('邮箱验证方式'))
      await screen.findByRole('option', { name: '管理员断言' })
    } finally {
      spy.mockRestore()
    }
  })

  it('carries a destructive write\'s confirmation in the already-aria-hidden page\'s panel', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      revokeAccountSessions: () => Promise.resolve(),
    })
    const { container } = renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.click(within(panel).getByRole('button', { name: '撤销全部会话' }))
    await confirmDialog(user)

    // MUI marks the page behind an open modal aria-hidden: the page-level
    // notice would not be reachable, so the panel's own region must carry
    // the confirmation.
    await waitFor(() => {
      expect(container).toHaveAttribute('aria-hidden', 'true')
    })
    const region = await waitFor(() => {
      const found = within(panel)
        .getAllByRole('status')
        .find((entry) => entry.textContent === '账户「acc-1」的会话已全部撤销')
      expect(found).toBeDefined()
      return found as HTMLElement
    })
    expect(region.closest('[role="dialog"]')).toBe(panel)
  })

  it('clears the detail panel feedback when it exits, so reopening starts clean', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountSecrets: () => Promise.resolve([SECRET_ROW]),
      revokeSecret: () => Promise.resolve(),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    let panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await within(panel).findByText('ci')
    await user.click(within(panel).getByRole('button', { name: '撤销密钥 ci' }))
    await confirmDialog(user)
    expect(
      await within(panel).findByText('账户「acc-1」的服务密钥「ci」已撤销'),
    ).toBeInTheDocument()

    fireEvent.click(within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: '账户「acc-1」' }),
      ).not.toBeInTheDocument()
    })

    // The exit cleared the panel's feedback: reopening shows no stale
    // message from the previous visit.
    await user.click(screen.getByRole('button', { name: '管理账户 acc-1' }))
    panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    expect(within(panel).queryByText('账户「acc-1」的服务密钥「ci」已撤销')).toBeNull()
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
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    await user.click(within(panel).getByRole('button', { name: '新建' }))
    await vi.waitFor(() => {
      expect(created).toEqual([
        { kind: 'user', display_name: 'Grace', username: 'grace' },
      ])
    })
    // A landed create closes the panel; the next opening starts from the
    // initial values.
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
  })

  it('sends the trimmed identifier values the local guard judged', async () => {
    const user = userEvent.setup()
    const created: ConsoleCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    // The guard trims before judging, so a padded value passes it; the
    // request must carry the same trimmed values the guard judged rather
    // than the raw input the server then refuses.
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    await user.type(within(panel).getByLabelText('用户名'), '  grace  ')
    await user.type(within(panel).getByLabelText('手机号'), '  +8613800000000  ')
    await user.click(within(panel).getByRole('button', { name: '新建' }))
    await vi.waitFor(() => {
      expect(created).toEqual([
        {
          kind: 'user',
          display_name: 'Grace',
          username: 'grace',
          phone: '+8613800000000',
        },
      ])
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
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    const submit = within(panel).getByRole('button', { name: '新建' })
    const form = panel.querySelector('form') as HTMLFormElement

    // An empty form names nothing an account can sign in with: the submit
    // is disabled, and a submit of the form itself sends no request either
    // -- the disabled button is not the only guard.
    expect(submit).toBeDisabled()
    fireEvent.submit(form)
    expect(created).toEqual([])

    // A display name alone is not an account either.
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
    expect(submit).toBeDisabled()
    fireEvent.submit(form)
    expect(created).toEqual([])

    // Nor is an identifier alone.
    await user.clear(within(panel).getByLabelText('显示名'))
    await user.type(within(panel).getByLabelText('用户名'), 'grace')
    expect(submit).toBeDisabled()
    fireEvent.submit(form)
    expect(created).toEqual([])

    // Both together make the account creatable.
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')
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
    const panel = await openCreate(user)
    const submit = within(panel).getByRole('button', { name: '新建' })

    // A service account signs in with a service secret, so the server
    // exempts it from the login-identifier requirement, and the submit must
    // be enabled on a display name alone before any identifier is given.
    await user.click(within(panel).getByLabelText('账户类型'))
    await user.click(await screen.findByRole('option', { name: '服务' }))
    await user.type(within(panel).getByLabelText('显示名'), 'Nightly')
    await vi.waitFor(() => expect(submit).toBeEnabled())
    await user.click(submit)
    await vi.waitFor(() => {
      expect(created).toEqual([{ kind: 'service', display_name: 'Nightly' }])
    })
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
    expect(await screen.findByText('账户「acc-1」的状态已保存')).toBeInTheDocument()
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
    expect(
      screen.queryByRole('dialog', { name: '需要重新验证' }),
    ).not.toBeInTheDocument()
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
      await screen.findByText('服务密钥「ci」（仅显示这一次）：one-time-value'),
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

  it('renders the unavailable placeholder with a retry when the detail read answers 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getAccount = () => {
      calls += 1
      return calls === 1
        ? Promise.reject({ status: 404, code: 'client.http.404' })
        : Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' }))
    }
    const operations = withAccounts({ getAccount })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

    // A by-id 404 replaces the panel's content with the "not found or
    // unavailable" placeholder and keeps the retry that re-reads.
    expect(
      await within(panel).findByText('记录不存在或暂不可用'),
    ).toBeInTheDocument()
    await user.click(within(panel).getByRole('button', { name: '重试' }))
    expect(
      await within(panel).findByText('显示名：Ada Lovelace'),
    ).toBeInTheDocument()
    expect(calls).toBe(2)
  })

  it('withdraws the stale marker while the list retry is in flight', async () => {
    const user = userEvent.setup()
    let searchedReads = 0
    let releaseRetry: (() => void) | undefined
    const listAccounts = (
      params?: Parameters<ConsoleBackend['listAccounts']>[0],
    ): ReturnType<ConsoleBackend['listAccounts']> => {
      // The page's own opening read (no filter) is not the one under test.
      if (params?.identifier_prefix === undefined) {
        return Promise.resolve({
          accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
          next_cursor: '',
        })
      }
      searchedReads += 1
      if (searchedReads === 1) {
        return Promise.resolve({
          accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
          next_cursor: '',
        })
      }
      if (searchedReads === 2) {
        return Promise.reject(new Error('refresh failed'))
      }
      // The retry stays in flight: the marker must already be withdrawn.
      return new Promise((resolve) => {
        releaseRetry = () =>
          resolve({
            accounts: [accountFixture({ display_name: 'Ada Lovelace' })],
            next_cursor: '',
          })
      })
    }
    const operations = withAccounts({ listAccounts })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    // A failed refresh keeps the settled list and marks it stale.
    await user.click(screen.getByRole('button', { name: '刷新' }))
    const stale = await screen.findByRole('alert')
    expect(stale).toHaveTextContent('所示内容可能已过期，请重试刷新。')

    // The retry is in flight: the marker goes away rather than staying
    // beside content the page is already re-reading.
    await user.click(within(stale).getByRole('button', { name: '重试' }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument()

    await act(async () => {
      releaseRetry?.()
    })
  })

  it('returns focus to the create trigger once a discarded form leaves the panel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<AccountsPage />, { operations: withAccounts() })
    const trigger = screen.getByRole('button', { name: CREATE_PANEL_NAME })
    await user.click(trigger)
    const panel = await screen.findByRole('dialog', { name: CREATE_PANEL_NAME })
    await user.type(within(panel).getByLabelText('显示名'), 'Grace')

    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const discard = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(within(discard).getByRole('button', { name: '放弃' }))
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument(),
    )

    // The confirmation closed together with the panel; the panel's own
    // close control unmounted with it, so focus belongs on the control
    // that opened the panel.
    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('returns focus to the row control once a discarded secret leaves the panel', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createSecret: () =>
        Promise.resolve({
          id: 'secret-9',
          label: 'ci',
          secret: 'one-time-value',
          expires_at: '0001-01-01T00:00:00Z',
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    const trigger = screen.getByRole('button', { name: '管理账户 acc-1' })
    await user.click(trigger)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })
    await user.type(within(panel).getByLabelText('标签'), 'ci')
    await user.click(within(panel).getByRole('button', { name: '新建密钥' }))
    await within(panel).findByText(
      '服务密钥「ci」（仅显示这一次）：one-time-value',
    )

    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const discard = await screen.findByRole('dialog', {
      name: '放弃未关闭的服务密钥',
    })
    await user.click(within(discard).getByRole('button', { name: '放弃' }))
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: '账户「acc-1」' }),
      ).not.toBeInTheDocument(),
    )

    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('declares the designed column priorities and caps the primary identity', async () => {
    const user = userEvent.setup()
    const id = 'acc-0123456789abcdef'
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [accountFixture({ id, display_name: 'Ada Lovelace' })],
            next_cursor: '',
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    const header = (label: string): Element =>
      screen.getByRole('columnheader', { name: label })
    // Identifier columns are low and shed first; the display name (the
    // primary identity) and the row actions never hide.
    expect(columnVisibility(header('标识'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header('类型'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header('显示名'))).toBeNull()
    expect(columnVisibility(header('状态'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header('强制 MFA'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
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
    // The identifier column's cap counts that copy control: the whole
    // wrapper is 160px below `md`, 200px from `md` up.
    expect(containerMaxWidthOf(idCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    // The display name (no copy control) keeps its text cap: 160px below
    // `md`, 200px from `md` up.
    expect(containerMaxWidthOf(screen.getByText('Ada Lovelace'))).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it("applies the designed priorities and caps to the panel's three tables", async () => {
    const user = userEvent.setup()
    const sessionId = 'sess-0123456789abcdef'
    const operations = withAccounts({
      listAccountFactors: () =>
        Promise.resolve([
          { kind: 'totp', label: 'Authenticator', status: 'active' },
        ]),
      listAccountSecrets: () => Promise.resolve([SECRET_ROW]),
      listAccountSessions: () =>
        Promise.resolve([{ ...SESSION_ROW, id: sessionId }]),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: '账户「acc-1」' })

    const tableOf = async (rowText: string): Promise<HTMLElement> => {
      const row = await within(panel).findByText(rowText)
      const table = row.closest('table')
      expect(table).not.toBeNull()
      return table as HTMLElement
    }

    // Service secrets: the label is the primary identity, state high,
    // expiry low.
    const secrets = await tableOf('ci')
    expect(
      columnVisibility(within(secrets).getByRole('columnheader', { name: '标签' })),
    ).toBeNull()
    expect(
      columnVisibility(within(secrets).getByRole('columnheader', { name: '状态' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(
        within(secrets).getByRole('columnheader', { name: '过期时间' }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(
      containerMaxWidthOf(within(secrets).getByText('ci')),
    ).toEqual(new Map([[0, 160], [900, 200]]))

    // Sessions: created time is the primary identity, state high and the
    // identifier low with its prefix as the visible text.
    const sessions = await tableOf(sessionId.slice(0, 8))
    expect(
      columnVisibility(within(sessions).getByRole('columnheader', { name: '标识' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(
      columnVisibility(
        within(sessions).getByRole('columnheader', { name: '创建时间' }),
      ),
    ).toBeNull()
    expect(
      columnVisibility(
        within(sessions).getByRole('columnheader', { name: '状态' }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    const sessionIdCell = within(sessions)
      .getByText(sessionId.slice(0, 8))
      .closest('td')
    expect(sessionIdCell).toHaveAttribute('title', sessionId)
    expect(containerMaxWidthOf(sessionIdCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    // Factors: the label is the primary identity, status high, kind medium.
    const factors = await tableOf('Authenticator')
    expect(
      columnVisibility(within(factors).getByRole('columnheader', { name: '标签' })),
    ).toBeNull()
    expect(
      columnVisibility(within(factors).getByRole('columnheader', { name: '状态' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(within(factors).getByRole('columnheader', { name: '类型' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
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
