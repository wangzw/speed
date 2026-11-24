import { describe, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  emittedStyleText,
  hasCoarsePointerTouchTarget,
} from '@speed/test-utils/emitted-css'
import { AccountsPage } from './AccountsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { createStepUpSession } from '../test-utils/session.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  fill,
  DETAIL_PANEL_NAME,
  MANAGE_ACCOUNT_NAME,
  DELETE_ACCOUNT_NAME,
  REVOKE_SECRET_CI_NAME,
  REVOKE_SESSION_SESS_1_NAME,
  DISPLAY_NAME_OPERATOR,
  COPY_VALUE_TEMPLATE,
  withAccounts,
  search,
  openDetail,
  PANEL_CLOSE_LABEL,
  CREATE_PANEL_NAME,
  DISCARD_DIALOG_NAME,
  COLLAPSE_ACCOUNT_NAME,
  openCreate,
  elementContainerRule,
  elementWidthAt,
  elementDeclarations,
  stepUpRefusal,
  SECRET_ROW,
  SESSION_ROW,
} from '../test-utils/accounts-page.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

describe('AccountsPage', () => {
  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withAccounts({
      listAccountSecrets: () =>
        Promise.resolve([
          {
            id: 'sec-1',
            label: 'ci',
            expires_at: null,
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
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: zhCN.common.refresh }),
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
        screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME }),
      ),
    ).toBe(true)
    expect(
      hasCoarsePointerTouchTarget(
        screen.getByRole('button', { name: DELETE_ACCOUNT_NAME }),
      ),
    ).toBe(true)
    await user.click(screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME }))
    await screen.findByText('ci')
    const gated = [
      screen.getByRole('button', { name: PANEL_CLOSE_LABEL }),
      screen.getByRole('button', { name: REVOKE_SECRET_CI_NAME }),
      screen.getByRole('button', { name: REVOKE_SESSION_SESS_1_NAME }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('opens the account detail in a dialog instead of expanding the row', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    await screen.findByText('Ada Lovelace')

    expect(
      screen.queryByRole('heading', { level: 2, name: DETAIL_PANEL_NAME }),
    ).toBeNull()
    const manage = screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME })
    expect(manage).toHaveAttribute('aria-haspopup', 'dialog')

    await user.click(manage)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    expect(elementWidthAt(emittedStyleText(), panel, 600)).toBe('52rem')
    expect(
      within(panel).getByRole('heading', { level: 2, name: DETAIL_PANEL_NAME }),
    ).toBeInTheDocument()
    expect(
      within(panel).getByRole('heading', { name: zhCN.accounts.writes.title }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: COLLAPSE_ACCOUNT_NAME }),
    ).not.toBeInTheDocument()
  })

  it('keeps the selected account rendered until the panel has exited', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })

    fireEvent.click(within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }))
    const exiting = screen.getByRole('dialog', { name: DETAIL_PANEL_NAME })
    expect(within(exiting).getByText(DISPLAY_NAME_OPERATOR)).toBeInTheDocument()

    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DETAIL_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })
  })

  it('opens the create panel from the header with two columns and an actions-row submit', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    const { container } = renderWithProviders(<AccountsPage />, {
      operations,
    })
    expect(container.querySelector('form')).toBeNull()

    const panel = await openCreate(user)
    expect(panel).toHaveAttribute('aria-modal', 'true')
    expect(elementWidthAt(emittedStyleText(), panel, 600)).toBe('52rem')
    const form = panel.querySelector('form')
    expect(form).not.toBeNull()
    expect(form).toHaveStyle({ display: 'grid' })
    const css = emittedStyleText()
    expect(elementContainerRule(css, form as Element)).toMatch(
      /grid-template-columns:\s*repeat\(2,\s*1fr\)/,
    )
    const submit = within(panel).getByRole('button', { name: zhCN.accounts.create.submit })
    const actions = submit.parentElement as HTMLElement
    expect(actions).toHaveStyle({ justifyContent: 'flex-end' })
    expect(elementDeclarations(css, actions)).toContain('grid-column:1/-1')
  })

  it('asks before closing a modified create form and closes an untouched one directly', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })

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

    panel = await openCreate(user)
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(within(confirm).getByRole('button', { name: zhCN.common.cancel }))
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DISCARD_DIALOG_NAME }),
      ).not.toBeInTheDocument()
    })
    panel = screen.getByRole('dialog', { name: CREATE_PANEL_NAME })
    expect(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel)).toHaveValue('Grace')

    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const again = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(
      within(again).getByRole('button', {
        name: zhCN.accounts.create.discardConfirm,
      }),
    )
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
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.create.submit }))

    const close = within(panel).getByRole('button', {
      name: PANEL_CLOSE_LABEL,
    })
    await waitFor(() =>
      expect(close).toHaveAttribute('aria-disabled', 'true'),
    )
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
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.displayNameLabel), 'Grace')
    await user.type(within(panel).getByLabelText(zhCN.accounts.create.usernameLabel), 'grace')
    await user.click(
      within(panel).getByRole('button', { name: PANEL_CLOSE_LABEL }),
    )
    const confirm = await screen.findByRole('dialog', {
      name: DISCARD_DIALOG_NAME,
    })
    await user.click(
      within(confirm).getByRole('button', {
        name: zhCN.accounts.create.discardConfirm,
      }),
    )
    await waitFor(() => {
      expect(
        screen.queryByRole('dialog', { name: DISCARD_DIALOG_NAME }),
      ).not.toBeInTheDocument()
      expect(
        screen.queryByRole('dialog', { name: CREATE_PANEL_NAME }),
      ).not.toBeInTheDocument()
    })

    const reopened = await openCreate(user)
    expect(within(reopened).getByLabelText(zhCN.accounts.create.displayNameLabel)).toHaveValue('')
    expect(within(reopened).getByLabelText(zhCN.accounts.create.usernameLabel)).toHaveValue('')
  })

  it('returns focus into the panel when the nested confirmation dialog closes', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    const reset = within(panel).getByRole('button', { name: zhCN.accounts.resetMFA })

    await user.click(reset)
    const confirm = await screen.findByRole('dialog', {
      name: zhCN.accounts.resetMFATitle,
    })
    await user.click(within(confirm).getByRole('button', { name: zhCN.common.cancel }))
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
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.click(within(panel).getByLabelText(zhCN.accounts.status.label))
    await user.click(await screen.findByRole('option', { name: zhCN.accounts.status.disabled }))
    const save = within(panel).getByRole('button', { name: zhCN.accounts.status.submit })

    await user.click(save)
    const challenge = await screen.findByRole('dialog', {
      name: zhCN.stepUp.title,
    })
    await waitFor(() =>
      expect(save).not.toHaveAttribute('aria-disabled', 'true'),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(panel.querySelectorAll('[role="alert"]')).toHaveLength(0)
    await user.click(within(challenge).getByRole('button', { name: zhCN.common.cancel }))
    await waitFor(() => expect(save).toHaveFocus())
  })

  it('keeps the panel content reachable while the page behind is hidden', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    const { container } = renderWithProviders(<AccountsPage />, {
      operations,
    })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })

    expect(container).toHaveAttribute('aria-hidden', 'true')
    expect(
      within(panel).getByRole('heading', { name: zhCN.accounts.factors.title }),
    ).toBeInTheDocument()
  })

  it('has no axe violations with the detail panel open', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await expectNoAxeViolations()
  })

  it('has no axe violations with the one-time secret displayed', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await openDetail(user)
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    await user.type(within(panel).getByLabelText(zhCN.accounts.createSecret.labelLabel), 'ci')
    await user.click(within(panel).getByRole('button', { name: zhCN.accounts.createSecret.submit }))
    await within(panel).findByText(
      fill(zhCN.accounts.createSecret.handedOver, {
        label: 'label',
        secret: 'one-time',
      }),
    )
    expect(await screen.findByRole('tooltip')).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('declares the designed column priorities and caps the primary identity', async () => {
    const user = userEvent.setup()
    const id = 'acc-0123456789abcdef'
    const operations = createAdminOperations(
      makeAdminBackend({
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
    const kind = columnVisibility(header(zhCN.accounts.column.kind))
    const status = columnVisibility(header(zhCN.accounts.column.status))
    expect(kind).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(status).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(kind!.visibleFrom).toBeGreaterThan(status!.visibleFrom)
    expect(columnVisibility(header(zhCN.accounts.column.identity))).toBeNull()
    expect(columnVisibility(header(zhCN.accounts.column.mfa))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.accounts.column.actions))).toBeNull()

    expect(screen.queryByText(id)).toBeNull()
    expect(screen.queryByText(id.slice(0, 8))).toBeNull()

    const nameCell = screen.getByText('Ada Lovelace').closest('td')
    expect(nameCell).not.toBeNull()
    expect(containerMaxWidthOf(nameCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('shows the display name with the username and never an email beside it', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              accountFixture({
                display_name: 'Ada Lovelace',
                identifiers: [
                  { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
                  { kind: 'email', value: 'ada@example.com', proven: false, sign_in: false, contact: false, default_contact: false },
                ],
              }),
            ],
            next_cursor: '',
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    const identity = await screen.findByText('Ada Lovelace (ada)')
    const cell = identity.closest('td')
    expect(cell).not.toBeNull()

    expect(
      within(cell as HTMLElement).queryByText('ada@example.com'),
    ).toBeNull()
    expect(cell).toHaveAttribute('title', 'Ada Lovelace (ada)')
    expect(
      within(cell as HTMLElement).getByRole('button', {
        name: fill(COPY_VALUE_TEMPLATE, { value: 'Ada Lovelace (ada)' }),
      }),
    ).toBeInTheDocument()
  })

  it('falls back to the display name when there is no username, never an email', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              accountFixture({
                display_name: 'Ada Lovelace',
                identifiers: [
                  { kind: 'email', value: 'ada@example.com', proven: false, sign_in: false, contact: false, default_contact: false },
                ],
              }),
            ],
            next_cursor: '',
          }),
        }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    const identity = await screen.findByText('Ada Lovelace')
    const cell = identity.closest('td')
    expect(cell).not.toBeNull()
    expect(
      within(cell as HTMLElement).queryByText('ada@example.com'),
    ).toBeNull()
    expect(cell).toHaveAttribute('title', 'Ada Lovelace')
    expect(
      within(cell as HTMLElement).getByRole('button', {
        name: fill(COPY_VALUE_TEMPLATE, { value: 'Ada Lovelace' }),
      }),
    ).toBeInTheDocument()
  })

  it('renders the neutral copy for an account with neither name nor username', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              accountFixture({
                display_name: '',
                identifiers: [
                  { kind: 'email', value: 'ada@example.com', proven: false, sign_in: false, contact: false, default_contact: false },
                ],
              }),
            ],
            next_cursor: '',
          }),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    expect(
      await screen.findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(screen.queryByText('ada@example.com')).toBeNull()
    expect(screen.queryByText('acc-1')).toBeNull()
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
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })

    const tableOf = async (rowText: string): Promise<HTMLElement> => {
      const row = await within(panel).findByText(rowText)
      const table = row.closest('table')
      expect(table).not.toBeNull()
      return table as HTMLElement
    }

    const secrets = await tableOf('ci')
    expect(
      columnVisibility(
        within(secrets).getByRole('columnheader', {
          name: zhCN.accounts.secrets.column.label,
        }),
      ),
    ).toBeNull()
    expect(
      columnVisibility(
        within(secrets).getByRole('columnheader', {
          name: zhCN.accounts.secrets.column.state,
        }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(
        within(secrets).getByRole('columnheader', {
          name: zhCN.accounts.secrets.column.expires,
        }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(
      containerMaxWidthOf(within(secrets).getByText('ci')),
    ).toEqual(new Map([[0, 160], [900, 200]]))

    const sessions = await tableOf(sessionId.slice(0, 8))
    expect(
      columnVisibility(
        within(sessions).getByRole('columnheader', {
          name: zhCN.accounts.sessions.column.id,
        }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
    expect(
      columnVisibility(
        within(sessions).getByRole('columnheader', {
          name: zhCN.accounts.sessions.column.created,
        }),
      ),
    ).toBeNull()
    expect(
      columnVisibility(
        within(sessions).getByRole('columnheader', {
          name: zhCN.accounts.sessions.column.state,
        }),
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

    const factors = await tableOf('Authenticator')
    expect(
      columnVisibility(
        within(factors).getByRole('columnheader', {
          name: zhCN.accounts.factors.column.label,
        }),
      ),
    ).toBeNull()
    expect(
      columnVisibility(
        within(factors).getByRole('columnheader', {
          name: zhCN.accounts.factors.column.status,
        }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 600 })
    expect(
      columnVisibility(
        within(factors).getByRole('columnheader', {
          name: zhCN.accounts.factors.column.kind,
        }),
      ),
    ).toEqual({ hiddenAt: 0, visibleFrom: 900 })
  })

  it('renders the account kind and status through the namespace in the list and the detail', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(<AccountsPage />, { operations })
    await search(user)
    const row = (await screen.findByText('Ada Lovelace')).closest('tr')
    expect(row).not.toBeNull()
    expect(
      within(row as HTMLElement).getByText(zhCN.accounts.create.kind.user),
    ).toBeInTheDocument()
    expect(
      within(row as HTMLElement).getByText(zhCN.accounts.status.active),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: MANAGE_ACCOUNT_NAME }))
    const panel = await screen.findByRole('dialog', { name: DETAIL_PANEL_NAME })
    expect(
      await within(panel).findByText(
        fill(zhCN.accounts.detail.kindLine, {
          value: zhCN.accounts.create.kind.user,
        }),
      ),
    ).toBeInTheDocument()
  })

  it('inserts a host column before the actions column under its own priority', async () => {
    const user = userEvent.setup()
    const operations = withAccounts()
    renderWithProviders(
      <AccountsPage
        extraColumns={[
          {
            id: 'plan',
            header: 'Plan',
            priority: 'low',
            cell: () => 'Pro',
          },
        ]}
      />,
      { operations },
    )
    await search(user)
    await screen.findByText('Ada Lovelace')

    const headers = screen
      .getAllByRole('columnheader')
      .map((header) => header.textContent)
    const plan = headers.indexOf('Plan')
    expect(plan).toBeGreaterThan(0)
    expect(plan).toBe(headers.indexOf(zhCN.accounts.column.actions) - 1)
    expect(
      columnVisibility(screen.getByRole('columnheader', { name: 'Plan' })),
    ).toEqual({ hiddenAt: 0, visibleFrom: 1200 })
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
