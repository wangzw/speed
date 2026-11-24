import { describe, expect, it } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS } from '@speed/ui-kit'
import { AccountsPage } from './AccountsPage.js'
import type {
  AdminAccount,
  AdminCreateAccountInput,
  AdminResetPasswordInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { accountFixture } from '../test-utils/operations.js'
import {
  fill,
  byIdentifier,
  byNameAndIdentifier,
  ACCOUNT_ID,
  DETAIL_PANEL_NAME,
  MFA_SAVED,
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
  USERNAME_IMMUTABLE,
  USERNAME_IMMUTABLE_TEXT,
  ALL_DETAIL_WRITES,
} from '../test-utils/accounts-page.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

describe('AccountsPage', () => {
  it('marks the detail panel behind its open status menu inert', async () => {
    const user = userEvent.setup()
    renderWithProviders(<AccountsPage />, { operations: withAccounts() })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', {
      name: DETAIL_PANEL_NAME,
    })
    await user.click(
      within(panel).getByLabelText(zhCN.accounts.status.label),
    )
    await screen.findByRole('option', {
      name: zhCN.accounts.status.disabled,
    })
    expect(panel.closest('[inert]')).not.toBeNull()
  })

  it('describes each unmet precondition on its write control', async () => {
    const user = userEvent.setup()
    renderWithProviders(<AccountsPage />, { operations: withAccounts() })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    const submit = (name: string): HTMLElement =>
      within(panel).getByRole('button', { name })

    expect(submit(zhCN.accounts.mfa.submit)).toHaveAccessibleDescription(
      zhCN.accounts.mfa.unchanged,
    )
    expect(submit(zhCN.accounts.patch.submit)).toHaveAccessibleDescription(
      zhCN.accounts.patch.unchanged,
    )
    expect(submit(zhCN.accounts.password.submit)).toHaveAccessibleDescription(
      zhCN.accounts.password.unavailable,
    )
    expect(
      submit(zhCN.accounts.addIdentifier.submit),
    ).toHaveAccessibleDescription(zhCN.accounts.addIdentifier.unavailable)
    expect(
      submit(zhCN.accounts.createSecret.submit),
    ).toHaveAccessibleDescription(zhCN.accounts.createSecret.unavailable)
    expect(
      submit(zhCN.accounts.removeIdentifier.submit),
    ).toHaveAccessibleDescription(zhCN.accounts.removeIdentifier.unavailable)
  })

  it('points the patch control at the field error instead of repeating it', async () => {
    const user = userEvent.setup()
    renderWithProviders(<AccountsPage />, { operations: withAccounts() })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    const attributes = within(panel).getByLabelText(
      zhCN.accounts.patch.attributesLabel,
    )
    await user.clear(attributes)
    await user.type(attributes, 'not json')
    expect(
      within(panel).getByRole('button', { name: zhCN.accounts.patch.submit }),
    ).toHaveAccessibleDescription(zhCN.accounts.patch.attributesHelper)
  })

  it('refuses activation of an unavailable control by click, Enter and Space', async () => {
    const user = userEvent.setup()
    const calls: AdminResetPasswordInput[] = []
    const operations = withAccounts({
      resetPassword: (input) => {
        calls.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const submit = screen.getByRole('button', {
      name: zhCN.accounts.password.submit,
    })
    submit.focus()
    expect(submit).toHaveFocus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await user.click(submit)
    expect(calls).toEqual([])
  })

  it('sends no create request from an unavailable submit by click or Enter', async () => {
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
    const submit = within(panel).getByRole('button', {
      name: zhCN.accounts.create.submit,
    })
    expect(submit).toHaveAccessibleDescription(
      zhCN.accounts.create.submitUnavailable,
    )
    await user.click(submit)
    within(panel)
      .getByLabelText(zhCN.accounts.create.displayNameLabel)
      .focus()
    await user.keyboard('{Enter}')
    expect(created).toEqual([])
  })

  it('sends no second create request from a programmatic submit while the first is in flight', async () => {
    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const created: AdminCreateAccountInput[] = []
    const operations = withAccounts({
      createAccount: (input) => {
        created.push(input)
        return new Promise((resolve) => {
          finish = () => resolve(accountFixture())
        })
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    const panel = await openCreate(user)
    await user.type(
      within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel),
      'Grace',
    )
    await user.type(
      within(panel).getByLabelText(zhCN.accounts.create.usernameLabel),
      'grace',
    )
    const form = panel.querySelector('form') as HTMLFormElement

    fireEvent.submit(form)
    await waitFor(() => expect(created).toHaveLength(1))
    await act(async () => {
      fireEvent.submit(form)
    })
    expect(created).toHaveLength(1)

    await act(async () => {
      finish?.()
    })
  })

  it('renders a username-immutable refusal as its own text, not the generic conflict', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      removeIdentifier: () => Promise.reject(USERNAME_IMMUTABLE),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(
      within(panel).getByLabelText(zhCN.accounts.removeIdentifier.label),
      'ada',
    )
    await user.click(
      within(panel).getByRole('button', {
        name: zhCN.accounts.removeIdentifier.submit,
      }),
    )
    await confirmDialog(user)

    const alert = await within(panel).findByRole('alert')
    expect(alert).toHaveTextContent(USERNAME_IMMUTABLE_TEXT)
    expect(alert).not.toHaveTextContent(zhCN.errors.authn.conflict)
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
      await screen.findByLabelText(zhCN.accounts.removeIdentifier.label),
      'ada@example.com',
    )
    await user.click(screen.getByRole('button', { name: zhCN.accounts.removeIdentifier.submit }))
    await confirmDialog(user)
    await waitForCondition(() => {
      expect(removed).toEqual(['ada@example.com'])
    })
  })

  const WRITE_NOTICES: Readonly<
    Record<
      string,
      {
        readonly notice: string
        readonly unnamed: string
        readonly values?: Readonly<Record<string, string>>
      }
    >
  > = {
    'reset all MFA factors': {
      notice: zhCN.accounts.mfaReset,
      unnamed: zhCN.accounts.mfaResetUnnamed,
    },
    'revoke all sessions': {
      notice: zhCN.accounts.sessionsRevoked,
      unnamed: zhCN.accounts.sessionsRevokedUnnamed,
    },
    'revoke one service secret': {
      notice: zhCN.accounts.secretRevoked,
      unnamed: zhCN.accounts.secretRevokedUnnamed,
      values: { label: 'ci' },
    },
    'revoke one session': {
      notice: zhCN.accounts.sessionRevoked,
      unnamed: zhCN.accounts.sessionRevokedUnnamed,
      values: { session: 'sess-1' },
    },
    'remove one login identifier by name': {
      notice: zhCN.accounts.identifierRemoved,
      unnamed: zhCN.accounts.identifierRemovedUnnamed,
      values: { loginIdentifier: 'ada@example.com' },
    },
    'change the account status': {
      notice: zhCN.accounts.statusSaved,
      unnamed: zhCN.accounts.statusSavedUnnamed,
    },
    'change the MFA requirement': {
      notice: zhCN.accounts.mfaSaved,
      unnamed: zhCN.accounts.mfaSavedUnnamed,
    },
    'patch the account': {
      notice: zhCN.accounts.patchSaved,
      unnamed: zhCN.accounts.patchSavedUnnamed,
    },
    'reset the password': {
      notice: zhCN.accounts.passwordReset,
      unnamed: zhCN.accounts.passwordResetUnnamed,
    },
    'add a login identifier': {
      notice: zhCN.accounts.identifierAdded,
      unnamed: zhCN.accounts.identifierAddedUnnamed,
      values: { loginIdentifier: 'ada@example.com' },
    },
    'create a service secret': {
      notice: zhCN.accounts.secretCreated,
      unnamed: zhCN.accounts.secretCreatedUnnamed,
      values: { label: 'ci' },
    },
  }

  function noticeMeta(label: string): {
    readonly notice: string
    readonly unnamed: string
    readonly values?: Readonly<Record<string, string>>
  } {
    const meta = WRITE_NOTICES[label]
    if (meta === undefined) {
      throw new Error(`no notice metadata for ${label}`)
    }
    return meta
  }

  const NOTICE_IDENTIFIERS: AdminAccount['identifiers'] = [
    { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
  ]

  function identityLabelOf(
    displayName: string,
    identifiers: AdminAccount['identifiers'],
  ): string {
    const username =
      identifiers.find((identifier) => identifier.kind === 'username')
        ?.value ?? ''
    if (displayName !== '' && username !== '') {
      return `${displayName} (${username})`
    }
    if (username !== '') {
      return username
    }
    return displayName !== '' ? displayName : zhCN.common.accountUnavailable
  }

  async function driveDetailNotice(
    user: ReturnType<typeof userEvent.setup>,
    entry: (typeof ALL_DETAIL_WRITES)[number],
    displayName: string,
    identifiers: AdminAccount['identifiers'],
  ): Promise<HTMLElement> {
    const operations = withAccounts({
      ...entry.operations(false),
      getAccount: () =>
        Promise.resolve(
          accountFixture({ display_name: displayName, identifiers }),
        ),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', {
      name: identityLabelOf(displayName, identifiers),
    })
    await entry.drive(user, panel)
    return panel
  }

  async function confirmDelete(
    user: ReturnType<typeof userEvent.setup>,
  ): Promise<void> {
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_LABEL }),
    )
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
  }

  describe.each(ALL_DETAIL_WRITES)(
    'detail notice: $label',
    (entry) => {
      const meta = noticeMeta(entry.label)

      it('names the account by display name and first identifier, never the id', async () => {
        const user = userEvent.setup()
        const panel = await driveDetailNotice(
          user,
          entry,
          'Ada',
          NOTICE_IDENTIFIERS,
        )
        const message = await within(panel).findByText(
          fill(meta.notice, {
            account: byNameAndIdentifier('Ada', 'ada'),
            ...meta.values,
          }),
        )
        expect(message).not.toHaveTextContent(ACCOUNT_ID)
      })

      it('names the account by its first identifier when the display name is empty, never the id', async () => {
        const user = userEvent.setup()
        const panel = await driveDetailNotice(
          user,
          entry,
          '',
          NOTICE_IDENTIFIERS,
        )
        const message = await within(panel).findByText(
          fill(meta.notice, {
            account: byIdentifier('ada'),
            ...meta.values,
          }),
        )
        expect(message).not.toHaveTextContent(ACCOUNT_ID)
      })

      it('uses the neutral sentence when neither resolves, never the id', async () => {
        const user = userEvent.setup()
        const panel = await driveDetailNotice(user, entry, '', [])
        const message = await within(panel).findByText(
          fill(meta.unnamed, meta.values ?? {}),
        )
        expect(message).not.toHaveTextContent(ACCOUNT_ID)
      })
    },
  )

  describe.each<{
    label: 'create' | 'delete'
    notice: string
    unnamed: string
  }>([
    {
      label: 'create',
      notice: zhCN.accounts.created,
      unnamed: zhCN.accounts.createdUnnamed,
    },
    {
      label: 'delete',
      notice: zhCN.accounts.deleted,
      unnamed: zhCN.accounts.deletedUnnamed,
    },
  ])('list notice: $label', ({ label, notice, unnamed }) => {
    it('names the account by display name and first identifier, never the id', async () => {
      const user = userEvent.setup()
      const expected = fill(notice, {
        account: byNameAndIdentifier('Ada', 'ada'),
      })
      await driveListNotice(user, label, 'Ada', NOTICE_IDENTIFIERS)
      const message = await screen.findByText(expected)
      expect(message).not.toHaveTextContent(ACCOUNT_ID)
    })

    it('names the account by its first identifier when the display name is empty, never the id', async () => {
      const user = userEvent.setup()
      const expected = fill(notice, { account: byIdentifier('ada') })
      await driveListNotice(user, label, '', NOTICE_IDENTIFIERS)
      const message = await screen.findByText(expected)
      expect(message).not.toHaveTextContent(ACCOUNT_ID)
    })

    it('uses the neutral sentence when neither resolves, never the id', async () => {
      const user = userEvent.setup()
      await driveListNotice(user, label, '', [])
      const message = await screen.findByText(unnamed)
      expect(message).not.toHaveTextContent(ACCOUNT_ID)
    })
  })

  async function driveListNotice(
    user: ReturnType<typeof userEvent.setup>,
    label: 'create' | 'delete',
    displayName: string,
    identifiers: AdminAccount['identifiers'],
  ): Promise<void> {
    const view = accountFixture({ display_name: displayName, identifiers })
    if (label === 'create') {
      const operations = withAccounts({
        createAccount: () => Promise.resolve(view),
      })
      renderWithProviders(<AccountsPage />, { operations })
      await search(user)
      await screen.findByText('Ada Lovelace')
      const panel = await openCreate(user)
      await user.type(
        within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel),
        'Grace',
      )
      await user.type(
        within(panel).getByLabelText(zhCN.accounts.create.usernameLabel),
        'grace',
      )
      await user.click(
        within(panel).getByRole('button', { name: zhCN.accounts.create.submit }),
      )
      await waitFor(() => {
        expect(
          screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
        ).not.toBeInTheDocument()
      })
      return
    }

    const operations = withAccounts({
      listAccounts: () =>
        Promise.resolve({ accounts: [view], next_cursor: '' }),
      deleteAccount: () => Promise.resolve(),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    const deleteName = fill(zhCN.accounts.deleteAria, {
      name: identityLabelOf(displayName, identifiers),
    })
    await screen.findByRole('button', { name: deleteName })
    await user.click(screen.getByRole('button', { name: deleteName }))
    await confirmDelete(user)
  }

  it('keeps the detail panel busy while any of its scopes is in flight', async () => {    const user = userEvent.setup()
    let finish: (() => void) | undefined
    const operations = withAccounts({
      resetPassword: () =>
        new Promise<void>((resolve) => {
          finish = resolve
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.password.label), 'secret123')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.password.submit }))

    await waitFor(() =>
      expect(
        within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
      ).toHaveAttribute('aria-disabled', 'true'),
    )
    fireEvent.keyDown(panel, { key: 'Escape' })
    expect(
      screen.getByRole('dialog', { name: DETAIL_PANEL_NAME }),
    ).toBeInTheDocument()

    await act(async () => {
      finish?.()
    })
    await waitFor(() =>
      expect(
        within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
      ).not.toHaveAttribute('aria-disabled', 'true'),
    )
    fireEvent.click(within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DETAIL_PANEL_NAME }),
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
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })

    await user.click(within(panel).getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.status.submit }))
    expect(await within(panel).findByRole('alert')).toHaveTextContent(FORBIDDEN_TEXT)

    await user.click(within(panel).getByLabelText(zhCN.accounts.mfa.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.mfa.required }))
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.mfa.submit }))
    expect(
      await within(panel).findByText(MFA_SAVED),
    ).toBeInTheDocument()
    expect(within(panel).getByRole('alert')).toHaveTextContent(FORBIDDEN_TEXT)
  })
})
