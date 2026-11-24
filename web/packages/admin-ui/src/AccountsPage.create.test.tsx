import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS } from '@speed/ui-kit'
import { AccountsPage } from './AccountsPage.js'
import type {
  AdminAccount,
  AdminCreateAccountInput,
  AdminPatchAccountInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { accountFixture } from '../test-utils/operations.js'
import {
  fill,
  ACCOUNT_ID,
  DETAIL_PANEL_NAME,
  MANAGE_ACCOUNT_NAME,
  DELETE_ACCOUNT_NAME,
  REVOKE_SECRET_CI_NAME,
  CREATED_GRACE,
  DELETED_ACC_1,
  SECRET_REVOKED_CI,
  SECRET_CREATED_CI,
  SESSIONS_REVOKED,
  HANDED_OVER_CI,
  DISCARD_SECRET_TITLE,
  withAccounts,
  search,
  openDetail,
  CONFIRM_AGAIN_LABEL,
  PANEL_CLOSE_LABEL,
  CONFIRM_LABEL,
  CREATE_PANEL_NAME,
  openCreate,
  confirmDialog,
  FORBIDDEN,
  FORBIDDEN_TEXT,
  EMAIL_TAKEN,
  EMAIL_TAKEN_TEXT,
  USERNAME_TAKEN,
  USERNAME_TAKEN_TEXT,
  USERNAME_RESERVED,
  USERNAME_RESERVED_TEXT,
  USERNAME_INVALID,
  USERNAME_INVALID_TEXT,
  BAD_REQUEST,
  BAD_REQUEST_TEXT,
  SECRET_ROW,
  outsideOf,
} from '../test-utils/accounts-page.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

describe('AccountsPage', () => {
  it('renders a creation failure under the form inside the create panel', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(FORBIDDEN),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const alert = await within(panel).findByRole('alert')
    expect(alert).toHaveTextContent(FORBIDDEN_TEXT)
    expect(outsideOf(panel, '[role="alert"]')).toEqual([])
    expect(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel)).toHaveValue('Grace')
    expect(screen.getByRole('dialog', { name: CREATE_PANEL_NAME })).toBe(panel)
  })

  it('names a taken email address in a creation failure instead of the fallback', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(EMAIL_TAKEN),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.emailLabel), 'grace@x.test')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const alert = await within(panel).findByRole('alert')
    expect(alert).toHaveTextContent(EMAIL_TAKEN_TEXT)
    expect(alert).not.toHaveTextContent(zhCN.errors.unknown)
    expect(within(panel).getByLabelText(zhCN.accounts.create.emailLabel)).toHaveValue(
      'grace@x.test',
    )
  })

  it('names a taken username on its field in a creation failure', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(USERNAME_TAKEN),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const field = within(panel).getByLabelText(zhCN.accounts.create.usernameLabel)
    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'))
    expect(within(panel).getByText(USERNAME_TAKEN_TEXT)).toBeInTheDocument()
    expect(field).toHaveFocus()
    expect(field).toHaveValue('grace')
    expect(within(panel).queryByRole('alert')).toBeNull()
  })

  it('names a reserved username on its field in a creation failure', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(USERNAME_RESERVED),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'admin')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const field = within(panel).getByLabelText(zhCN.accounts.create.usernameLabel)
    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'))
    expect(within(panel).getByText(USERNAME_RESERVED_TEXT)).toBeInTheDocument()
    expect(field).toHaveFocus()
    expect(field).toHaveValue('admin')
    expect(within(panel).queryByRole('alert')).toBeNull()
  })

  it('names an ill-formed username on its field in a creation failure', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(USERNAME_INVALID),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), '-grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const field = within(panel).getByLabelText(zhCN.accounts.create.usernameLabel)
    await waitFor(() => expect(field).toHaveAttribute('aria-invalid', 'true'))
    expect(within(panel).getByText(USERNAME_INVALID_TEXT)).toBeInTheDocument()
    expect(field).toHaveFocus()
    expect(within(panel).queryByRole('alert')).toBeNull()
  })

  it('keeps the username copy off a service account creation', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(USERNAME_TAKEN),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Builder')
    fireEvent.mouseDown(within(panel).getByLabelText(zhCN.accounts.create.kindLabel))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.create.kind.service }))
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const alert = await within(panel).findByRole('alert')
    expect(alert).not.toHaveTextContent(USERNAME_TAKEN_TEXT)
    expect(alert).toHaveTextContent(zhCN.errors.unknown)
  })

  it('leaves a password-policy refusal out of the username copy', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createAccount: () => Promise.reject(BAD_REQUEST),
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.passwordLabel), 'short')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const alert = await within(panel).findByRole('alert')
    expect(alert).toHaveTextContent(BAD_REQUEST_TEXT)
    expect(alert).not.toHaveTextContent(USERNAME_INVALID_TEXT)
    expect(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel)).toHaveValue('grace')
  })

  it('writes the creation success below the list only after the panel exits', async () => {
    const user = userEvent.setup()
    let finishCreate: ((account: AdminAccount) => void) | undefined
    const operations = withAccounts({
      createAccount: () =>
        new Promise<AdminAccount>((resolve) => {
          finishCreate = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const successAgainstOpenPanel: number[] = []
    let batches = 0
    const observer = new MutationObserver(() => {
      batches += 1
      const dialogMounted = document.querySelector('[role="dialog"]') !== null
      const successWritten = document.body.textContent?.includes(CREATED_GRACE) ?? false
      if (dialogMounted && successWritten) {
        successAgainstOpenPanel.push(batches)
      }
    })
    observer.observe(document.body, {
      childList: true,
      subtree: true,
      characterData: true,
    })

    await act(async () => {
      finishCreate?.(accountFixture({ display_name: 'Grace' }))
    })

    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
    const notice = await screen.findByText(CREATED_GRACE)
    observer.disconnect()

    expect(successAgainstOpenPanel).toEqual([])
    expect(notice.closest('[role="dialog"]')).toBeNull()
    expect(notice.closest('[role="status"]')).not.toBeNull()
  })

  it('keeps the last-settled result in the list scope when writes overlap', async () => {
    const user = userEvent.setup()
    let finishDelete: (() => void) | undefined
    let finishCreate: ((account: AdminAccount) => void) | undefined
    const operations = withAccounts({
      deleteAccount: () =>
        new Promise<void>((resolve) => {
          finishDelete = resolve
        }),
      createAccount: () =>
        new Promise<AdminAccount>((resolve) => {
          finishCreate = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    await user.click(screen.getByRole('button', { name: DELETE_ACCOUNT_NAME }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: CONFIRM_LABEL }))
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

    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))
    await act(async () => {
      finishCreate?.(accountFixture({ display_name: 'Grace' }))
    })
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
    expect(await screen.findByText(CREATED_GRACE)).toBeInTheDocument()

    await act(async () => {
      finishDelete?.()
    })
    expect(await screen.findByText(DELETED_ACC_1)).toBeInTheDocument()
    expect(screen.queryByText(CREATED_GRACE)).toBeNull()
  })

  it('shows a one-time secret under the create-secret group with a named close control', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      createSecret: () =>
        Promise.resolve({
          id: 'secret-9',
          label: 'ci',
          secret: 'one-time-value',
          expires_at: null,
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.createSecret.submit }))

    const display = await within(panel).findByText(
      HANDED_OVER_CI,
    )
    expect(display.closest('[role="status"]')).not.toBeNull()
    expect(within(panel).getByText('one-time-value')).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', {
        name: zhCN.accounts.createSecret.copy,
      }),
    ).toBeInTheDocument()
    const close = within(panel).getByRole('button', {
      name: fill(zhCN.accounts.createSecret.closeAria, {
        name: 'Operator',
        id: 'acc-1',
        label: 'ci',
      }),
    })
    expect(close).toBeInTheDocument()
    expect(
      within(panel).getByText(SECRET_CREATED_CI),
    ).toBeInTheDocument()
    await user.click(close)
    expect(within(panel).queryByText(HANDED_OVER_CI)).toBeNull()
    expect(
      within(panel).getByText(SECRET_CREATED_CI),
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
              expires_at: null,
            })
          : Promise.reject(FORBIDDEN)
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.createSecret.submit }))
    await within(panel).findByText(
      HANDED_OVER_CI,
    )

    await user.type(within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.createSecret.submit }))
    expect(await within(panel).findByRole('alert')).toHaveTextContent(FORBIDDEN_TEXT)
    expect(
      within(panel).getByText(HANDED_OVER_CI),
    ).toBeInTheDocument()
    expect(
      within(panel).queryByText(SECRET_CREATED_CI),
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
          expires_at: null,
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    let panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.createSecret.submit }))
    await within(panel).findByText(
      HANDED_OVER_CI,
    )

    fireEvent.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    let discard = await screen.findByRole('dialog', {
      name: DISCARD_SECRET_TITLE,
    })
    await user.click(within(discard).getByRole('button', { name: zhCN.common.cancel }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DISCARD_SECRET_TITLE }),
      ).not.toBeInTheDocument()
    })
    panel = screen.getByRole('dialog', { name: DETAIL_PANEL_NAME })
    expect(
      within(panel).getByText(HANDED_OVER_CI),
    ).toBeInTheDocument()

    fireEvent.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    discard = await screen.findByRole('dialog', {
      name: DISCARD_SECRET_TITLE,
    })
    await user.click(
      within(discard).getByRole('button', {
        name: zhCN.accounts.createSecret.discardConfirm,
      }),
    )
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DISCARD_SECRET_TITLE }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('dialog', { name: DETAIL_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
    await user.click(screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME }))
    panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    expect(within(panel).queryByText(HANDED_OVER_CI)).toBeNull()
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
      expect(fragmentWarnings).toEqual([])
      await user.click(within(panel).getByLabelText(zhCN.accounts.create.emailViaLabel))
      await screen.findByRole('option', { name: zhCN.accounts.create.via.admin })
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
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.revokeSessions }))
    await confirmDialog(user)

    await waitFor(() => {
      expect(container).toHaveAttribute('aria-hidden', 'true')
    })
    const region = await waitFor(() => {
      const found = within(panel)
        .getAllByRole('status')
        .find((entry) => entry.textContent === SESSIONS_REVOKED)
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
    let panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await within(panel).findByText('ci')
    await user.click(within(panel).getByRole('button', { name: REVOKE_SECRET_CI_NAME }))
    await confirmDialog(user)
    expect(
      await within(panel).findByText(SECRET_REVOKED_CI),
    ).toBeInTheDocument()

    fireEvent.click(within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DETAIL_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })

    await user.click(screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME }))
    panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    expect(within(panel).queryByText(SECRET_REVOKED_CI)).toBeNull()
  })

  it('creates an account through the bundle', async () => {
    const user = userEvent.setup()
    const created: AdminCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))
    await waitForCondition(() => {
      expect(created).toEqual([
        { kind: 'user', display_name: 'Grace', username: 'grace' },
      ])
    })
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
  })

  it('sends the trimmed identifier values the local guard judged', async () => {
    const user = userEvent.setup()
    const created: AdminCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), '  grace  ')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.phoneLabel), '  +8613800000000  ')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))
    await waitForCondition(() => {
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

  it('keeps the create submit unavailable until a display name and a username are given', async () => {
    const user = userEvent.setup()
    const created: AdminCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    const submit = within(panel).getByRole('button', { name: zhCN.accounts.create.submit })
    const form = panel.querySelector('form') as HTMLFormElement

    expect(submit).toHaveAttribute('aria-disabled', 'true')
    fireEvent.submit(form)
    expect(created).toEqual([])

    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.accounts.create.usernameUnavailable,
    )
    fireEvent.submit(form)
    expect(created).toEqual([])

    await user.type(within(panel).getByLabelText(zhCN.accounts.create.emailLabel), 'grace@x.test')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    fireEvent.submit(form)
    expect(created).toEqual([])

    await user.clear(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel))
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.accounts.create.submitUnavailable,
    )
    fireEvent.submit(form)
    expect(created).toEqual([])

    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await waitForCondition(() =>
      expect(submit).not.toHaveAttribute('aria-disabled', 'true'),
    )
    await user.click(submit)
    await waitForCondition(() => expect(created).toHaveLength(1))
  })

  it('creates an identifier-less service account from a display name alone', async () => {
    const user = userEvent.setup()
    const created: AdminCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    const submit = within(panel).getByRole('button', { name: zhCN.accounts.create.submit })

    await user.click(within(panel).getByLabelText(zhCN.accounts.create.kindLabel))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.create.kind.service }))
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Nightly')
    await waitForCondition(() =>
      expect(submit).not.toHaveAttribute('aria-disabled', 'true'),
    )
    await user.click(submit)
    await waitForCondition(() => {
      expect(created).toEqual([{ kind: 'service', display_name: 'Nightly' }])
    })
  })

  it('edits declared attributes as one text field each on the create form', async () => {
    const user = userEvent.setup()
    const created: AdminCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(
      <AccountsPage attributeFields={[{ key: 'tier', label: 'Tier' }]} />,
      { operations },
    )
    const panel = await openCreate(user)
    expect(
      within(panel).queryByLabelText(zhCN.accounts.create.attributesLabel),
    ).toBeNull()
    await user.type(
      within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel),
      'Grace',
    )
    await user.type(
      within(panel).getByLabelText(zhCN.accounts.create.usernameLabel),
      'grace',
    )
    await user.type(within(panel).getByLabelText('Tier'), 'gold')
    await user.click(
      within(panel).getByRole('button', { name: zhCN.accounts.create.submit }),
    )
    await waitForCondition(() => {
      expect(created).toEqual([
        {
          kind: 'user',
          display_name: 'Grace',
          username: 'grace',
          attributes: { tier: 'gold' },
        },
      ])
    })
  })

  it('edits declared attributes on the patch and keeps the undeclared keys', async () => {
    const user = userEvent.setup()
    const patched: AdminPatchAccountInput[] = []
    const operations = withAccounts({
      getAccount: () =>
        Promise.resolve(
          accountFixture({
            attributes: { tier: 'gold', legacy: 'kept' },
          }),
        ),
      patchAccount: (input) => {
        patched.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(
      <AccountsPage attributeFields={[{ key: 'tier', label: 'Tier' }]} />,
      { operations },
    )
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    const tier = await within(panel).findByLabelText('Tier')
    expect(tier).toHaveValue('gold')
    expect(
      within(panel).queryByLabelText(zhCN.accounts.patch.attributesLabel),
    ).toBeNull()

    await user.clear(tier)
    await user.type(tier, 'platinum')
    await user.click(
      within(panel).getByRole('button', { name: zhCN.accounts.patch.submit }),
    )
    await waitForCondition(() => {
      expect(patched).toEqual([
        {
          id: ACCOUNT_ID,
          attributes: { tier: 'platinum', legacy: 'kept' },
        },
      ])
    })
  })
})
