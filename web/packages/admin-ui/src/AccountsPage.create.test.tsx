/** AccountsPage suite: create form and account attribute cases. */

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
    // The failure belongs to the create panel's own scope, inside the modal;
    // the page behind it carries none.
    expect(outsideOf(panel, '[role="alert"]')).toEqual([])
    // The fields stay for correction and the panel stays open.
    expect(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel)).toHaveValue('Grace')
    expect(screen.getByRole('dialog', { name: CREATE_PANEL_NAME })).toBe(panel)
  })

  it('names a taken email address in a creation failure instead of the fallback', async () => {
    // Creating an account reserves the address, so a 409 email-taken is a
    // refusal the operator can act on: the banner states it and names the
    // value to change, never the generic unknown line.
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
    // The operator still holds the fields to correct the address.
    expect(within(panel).getByLabelText(zhCN.accounts.create.emailLabel)).toHaveValue(
      'grace@x.test',
    )
  })

  it('names a taken username on its field in a creation failure', async () => {
    // A user account is opened with a login name, so a 409 username-taken is
    // a refusal the field carries: the name is the value the operator
    // changes, with focus moved there and no whole-form banner.
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
    // A service account signs in with a service secret, so the username
    // refusals are the user kind's own.
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
    // authn.bad_request on this route may be the password policy or the
    // email address, not the username's shape (that is username-invalid),
    // so it keeps the scope's own sentence.
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

    // The page writes the success only once the panel has gone. That is an
    // ordering fact, and the exit is a real transition: a snapshot taken the
    // instant the create settles races it (under load the transition can
    // finish first, leaving nothing to look at). So the ordering is
    // recorded as it happens -- every DOM batch is checked for the success
    // text existing while a dialog is still mounted.
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

    // Only after the exit does it land, in the page's list region.
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
    const notice = await screen.findByText(CREATED_GRACE)
    observer.disconnect()

    // The success was never on screen while the panel was: no batch saw
    // both, and it lands outside the dialog, in the list's status region.
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

    // Start the row delete and confirm it through the two-step guard; it
    // stays in flight (one attempt in the list scope).
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

    // While that write is in flight, a create lands in the same scope via
    // the create panel's exit.
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

    // The delete settles last and replaces the create's confirmation: one
    // slot, the last result wins.
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
    // The secret itself is the panel's value, offered beside a copy
    // control: the message states only what was handed out.
    expect(within(panel).getByText('one-time-value')).toBeInTheDocument()
    expect(
      within(panel).getByRole('button', {
        name: zhCN.accounts.createSecret.copy,
      }),
    ).toBeInTheDocument()
    // The explicit close control names the account it belongs to.
    const close = within(panel).getByRole('button', {
      name: fill(zhCN.accounts.createSecret.closeAria, {
        name: 'Operator',
        id: 'acc-1',
        label: 'ci',
      }),
    })
    expect(close).toBeInTheDocument()
    // The slot's success confirmation stands beside the display, not in it;
    // closing the display leaves the confirmation in place.
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

    // A second attempt resets the creating scope and settles as a failure.
    // The display is page state, not slot state: it survives both.
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

    // Cancelling the discard keeps the panel and the unclosed secret.
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

    // Confirming closes the panel and discards; reopening starts without it.
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
    // Both the confirmation and the panel must finish exiting before the
    // page behind the modal is reachable again.
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
      // The warning belongs to the render that mounts the two
      // verification-source selects, so it is checked as soon as the panel
      // opens -- before the option list below, whose absence is the same
      // defect's user-visible half but a slower, less precise signal.
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

    // MUI marks the page behind an open modal aria-hidden: the page-level
    // notice would not be reachable, so the panel's own region must carry
    // the confirmation.
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

    // The exit cleared the panel's feedback: reopening shows no stale
    // message from the previous visit.
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
    const created: AdminCreateAccountInput[] = []
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

    // An empty form names nothing an account can be opened with: the submit
    // is unavailable, and a submit of the form itself sends no request either
    // -- the control is not the only guard.
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    fireEvent.submit(form)
    expect(created).toEqual([])

    // A display name alone is not an account either, and the reason names
    // the missing piece: a user account still needs its username.
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.accounts.create.usernameUnavailable,
    )
    fireEvent.submit(form)
    expect(created).toEqual([])

    // A user account is opened with a username: an email address beside the
    // display name does not stand in for it.
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.emailLabel), 'grace@x.test')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    fireEvent.submit(form)
    expect(created).toEqual([])

    // Nor is the username alone an account without a display name.
    await user.clear(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel))
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.accounts.create.submitUnavailable,
    )
    fireEvent.submit(form)
    expect(created).toEqual([])

    // Both together make the account creatable.
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

    // A service account signs in with a service secret, so the server
    // exempts it from the login-identifier requirement, and the submit must
    // be enabled on a display name alone before any identifier is given.
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
    // The raw-JSON editor is replaced, not duplicated.
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
    // The declared key takes the typed value; the key the host did not
    // declare survives the patch.
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
