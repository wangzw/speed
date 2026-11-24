import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AccountsPage } from './AccountsPage.js'
import type {
  AdminAccountFactor,
  AdminAddIdentifierInput,
  AdminCreateSecretInput,
  AdminPatchAccountInput,
  AdminResetPasswordInput,
  AdminSetAccountStatusInput,
  AdminSetMFARequiredInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createStepUpSession } from '../test-utils/session.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import {
  fill,
  DETAIL_PANEL_NAME,
  MANAGE_ACCOUNT_NAME,
  STATUS_SAVED,
  HANDED_OVER_CI,
  DISPLAY_NAME_OPERATOR,
  DISCARD_SECRET_TITLE,
  withAccounts,
  search,
  openDetail,
  PANEL_CLOSE_LABEL,
  CREATE_PANEL_NAME,
  DISCARD_DIALOG_NAME,
  stepUpRefusal,
  SSO_REFUSAL,
} from '../test-utils/accounts-page.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

describe('AccountsPage', () => {
  it('changes an account status through the bundle', async () => {
    const user = userEvent.setup()
    const calls: AdminSetAccountStatusInput[] = []
    const operations = withAccounts({
      setAccountStatus: (input) => {
        calls.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: zhCN.accounts.status.submit })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    await user.click(screen.getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(save)
    await waitForCondition(() => {
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
    await user.click(screen.getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(screen.getByRole('button', { name: zhCN.accounts.status.submit }))
    expect(await screen.findByText(STATUS_SAVED)).toBeInTheDocument()
  })

  it('names each account row control with its own account', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
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

    expect(
      screen.getByRole('button', {
        name: fill(zhCN.accounts.manageAria, { name: 'Ada' }),
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: fill(zhCN.accounts.deleteAria, { name: 'Grace' }),
      }),
    ).toBeInTheDocument()
  })

  it('opens the step-up dialog on a refused write and replays it once elevated', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const startFlow = vi.spyOn(session, 'startAuthenticatedFlow')
    const calls: AdminSetAccountStatusInput[] = []
    let attempts = 0
    const operations = withAccounts({
      setAccountStatus: (input) => {
        calls.push(input)
        attempts += 1
        return attempts === 1 ? Promise.reject(stepUpRefusal()) : Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations, session })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: zhCN.accounts.status.submit })
    await user.click(screen.getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(save)
    await waitForCondition(() => {
      expect(startFlow).toHaveBeenCalled()
    })
    await waitForCondition(() => {
      expect(calls).toEqual([
        { id: 'acc-1', status: 'disabled' },
        { id: 'acc-1', status: 'disabled' },
      ])
    })
    expect(session.getSnapshot().state).toBe('authenticated')
  })

  it('shows the enterprise sign-in exit for an SSO step-up refusal', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const startFlow = vi.spyOn(session, 'startAuthenticatedFlow')
    const operations = withAccounts({
      setAccountStatus: () => Promise.reject(SSO_REFUSAL),
    })
    renderWithProviders(<AccountsPage />, { operations, session })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: zhCN.accounts.status.submit })
    await user.click(screen.getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(save)

    expect(await screen.findByText(zhCN.stepUp.ssoRequired)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeInTheDocument()
    expect(screen.queryByText(zhCN.stepUp.description)).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.submit }),
    ).not.toBeInTheDocument()
    expect(startFlow).not.toHaveBeenCalled()
    expect(session.getSnapshot().state).toBe('authenticated')
  })

  it('does not replay a 401 when the session itself ended', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    session.signOut()
    const startFlow = vi.spyOn(session, 'startAuthenticatedFlow')
    const calls: AdminSetAccountStatusInput[] = []
    const operations = withAccounts({
      setAccountStatus: (input) => {
        calls.push(input)
        return Promise.reject(stepUpRefusal())
      },
    })
    renderWithProviders(<AccountsPage />, { operations, session })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: zhCN.accounts.status.submit })
    await user.click(screen.getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    await user.click(save)
    await waitForCondition(() => {
      expect(calls.length).toBe(1)
    })
    expect(startFlow).not.toHaveBeenCalled()
    expect(
      screen.queryByRole('dialog', { name: zhCN.stepUp.title }),
    ).not.toBeInTheDocument()
  })

  it('sets the MFA requirement through the bundle', async () => {
    const user = userEvent.setup()
    const calls: AdminSetMFARequiredInput[] = []
    const operations = withAccounts({
      setMFARequired: (input) => {
        calls.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const save = await screen.findByRole('button', { name: zhCN.accounts.mfa.submit })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    await user.click(screen.getByLabelText(zhCN.accounts.mfa.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.mfa.required }))
    await user.click(save)
    await waitForCondition(() => {
      expect(calls).toEqual([{ id: 'acc-1', required: true }])
    })
  })

  it('patches an account through the bundle', async () => {
    const user = userEvent.setup()
    const calls: AdminPatchAccountInput[] = []
    const operations = withAccounts({
      patchAccount: (input) => {
        calls.push(input)
        return Promise.resolve(accountFixture())
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const name = await screen.findByLabelText(zhCN.accounts.patch.displayNameLabel)
    expect(name).toHaveValue('Operator')
    expect(
      screen.getByRole('button', { name: zhCN.accounts.patch.submit }),
    ).toHaveAttribute('aria-disabled', 'true')
    await user.clear(name)
    await user.type(name, 'Ada II')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.patch.submit }))
    await waitForCondition(() => {
      expect(calls).toEqual([{ id: 'acc-1', display_name: 'Ada II' }])
    })
  })

  it('does not write the protected flag for the control\'s unchanged sentinel', async () => {
    const user = userEvent.setup()
    const calls: AdminPatchAccountInput[] = []
    const operations = withAccounts({
      getAccount: () => Promise.resolve(accountFixture({ protected: true })),
      patchAccount: (input) => {
        calls.push(input)
        return Promise.resolve(accountFixture({ protected: true }))
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const name = await screen.findByLabelText(zhCN.accounts.patch.displayNameLabel)
    await user.clear(name)
    await user.type(name, 'Ada II')
    await user.click(screen.getByLabelText(zhCN.accounts.patch.protectedLabel))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.patch.protected.unchanged }))
    await user.click(screen.getByRole('button', { name: zhCN.accounts.patch.submit }))
    await waitForCondition(() => {
      expect(calls).toEqual([{ id: 'acc-1', display_name: 'Ada II' }])
    })
  })

  it('resets a password through the bundle', async () => {
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
    await user.type(await screen.findByLabelText(zhCN.accounts.password.label), 'secret123')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.password.submit }))
    await waitForCondition(() => {
      expect(calls).toEqual([{ id: 'acc-1', password: 'secret123' }])
    })
  })

  it('adds an identifier through the bundle', async () => {
    const user = userEvent.setup()
    const calls: AdminAddIdentifierInput[] = []
    const operations = withAccounts({
      addIdentifier: (input) => {
        calls.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.type(await screen.findByLabelText(zhCN.accounts.addIdentifier.valueLabel), 'ada@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.addIdentifier.submit }))
    await waitForCondition(() => {
      expect(calls).toEqual([
        { id: 'acc-1', kind: 'username', value: 'ada@example.com' },
      ])
    })
  })

  it('creates a service secret and shows the one-time value', async () => {
    const user = userEvent.setup()
    const calls: AdminCreateSecretInput[] = []
    const operations = withAccounts({
      createSecret: (input) => {
        calls.push(input)
        return Promise.resolve({
          id: 'secret-9',
          label: input.label ?? '',
          secret: 'one-time-value',
          expires_at: null,
        })
      },
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await user.type(await screen.findByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(screen.getByRole('button', { name: zhCN.accounts.createSecret.submit }))
    await waitForCondition(() => {
      expect(calls).toEqual([{ id: 'acc-1', label: 'ci' }])
    })
    expect(
      await screen.findByText(HANDED_OVER_CI),
    ).toBeInTheDocument()
  })

  it('returns focus to the create trigger once a discarded form leaves the panel', async () => {
    const user = userEvent.setup()
    renderWithProviders(<AccountsPage />, { operations: withAccounts() })
    const trigger = screen.getByRole('button', { name: CREATE_PANEL_NAME })
    await user.click(trigger)
    const panel = await screen.findByRole('dialog', { name: CREATE_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')

    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const discard = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(
      within(discard).getByRole('button', {
        name: zhCN.accounts.create.discardConfirm,
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument(),
    )

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
          expires_at: null,
        }),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')
    const trigger = screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME })
    await user.click(trigger)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.createSecret.submit }))
    await within(panel).findByText(
      HANDED_OVER_CI,
    )

    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const discard = await screen.findByRole('dialog', {
      name: DISCARD_SECRET_TITLE,
    })
    await user.click(
      within(discard).getByRole('button', {
        name: zhCN.accounts.createSecret.discardConfirm,
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: DETAIL_PANEL_NAME }),
      ).not.toBeInTheDocument(),
    )

    await waitFor(() => expect(trigger).toHaveFocus())
  })

  it('mounts no factor, secret or session surface when their features are absent', async () => {
    const user = userEvent.setup()
    const listAccountFactors = vi.fn(() => Promise.resolve([]))
    const listAccountSecrets = vi.fn(() => Promise.resolve([]))
    const listAccountSessions = vi.fn(() => Promise.resolve([]))
    const operations = withAccounts({
      listAccountFactors,
      listAccountSecrets,
      listAccountSessions,
    })
    renderWithProviders(<AccountsPage />, {
      operations,
      hasFeature: () => false,
    })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await within(panel).findByText(DISPLAY_NAME_OPERATOR)

    for (const title of [
      zhCN.accounts.factors.title,
      zhCN.accounts.secrets.title,
      zhCN.accounts.sessions.title,
    ]) {
      expect(within(panel).queryByRole('heading', { name: title })).toBeNull()
    }
    for (const label of [
      zhCN.accounts.resetMFA,
      zhCN.accounts.revokeSessions,
      zhCN.accounts.mfa.submit,
      zhCN.accounts.password.submit,
      zhCN.accounts.createSecret.submit,
    ]) {
      expect(within(panel).queryByRole('button', { name: label })).toBeNull()
    }
    expect(listAccountFactors).not.toHaveBeenCalled()
    expect(listAccountSecrets).not.toHaveBeenCalled()
    expect(listAccountSessions).not.toHaveBeenCalled()

    for (const label of [
      zhCN.accounts.status.submit,
      zhCN.accounts.patch.submit,
      zhCN.accounts.addIdentifier.submit,
    ]) {
      expect(within(panel).getByRole('button', { name: label })).toBeInTheDocument()
    }
  })

  it('renders a factor kind and status through the namespace, falling back to the raw value', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountFactors: () =>
        Promise.resolve([
          { kind: 'totp', label: 'Authenticator', status: 'active' },
          {
            kind: 'hardware',
            label: 'Key',
            status: 'retired' as AdminAccountFactor['status'],
          },
        ]),
    })
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    const factorRow = (await within(panel).findByText('Authenticator')).closest(
      'tr',
    )
    expect(factorRow).not.toBeNull()
    expect(
      within(factorRow as HTMLElement).getByText(
        zhCN.accounts.factors.kind.totp,
      ),
    ).toBeInTheDocument()
    expect(
      within(factorRow as HTMLElement).getByText(
        zhCN.accounts.factors.status.active,
      ),
    ).toBeInTheDocument()
    expect(within(panel).getByText('hardware')).toBeInTheDocument()
    expect(within(panel).getByText('retired')).toBeInTheDocument()
  })
})
