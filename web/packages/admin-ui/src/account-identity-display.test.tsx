import { describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { uiKitResources } from '@speed/ui-kit'
import {
  AccountIdentity,
  AuditObjectIdentity,
  TenantAccountIdentity,
} from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { OrganizationMembersPage } from './OrganizationMembersPage.js'
import { InvitationsPage } from './InvitationsPage.js'
import { AccountsPage } from './AccountsPage.js'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { PermissionsPage } from './PermissionsPage.js'
import { AuditPage } from './AuditPage.js'
import { AuditDetailPage } from './AuditDetailPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  recordFixture,
} from '../test-utils/operations.js'
import type { AdminAccount, AdminAuditRecord } from './operations.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'
const UUID = '019a2d4f-1c3e-7b52-9f00-6d7a1b2c3d4e'

function usernameIdentifier(value: string): AdminAccount['identifiers'][number] {
  return {
    kind: 'username',
    value,
    proven: false,
    sign_in: true,
    contact: false,
    default_contact: false,
  }
}

function emailIdentifier(value: string): AdminAccount['identifiers'][number] {
  return {
    kind: 'email',
    value,
    proven: false,
    sign_in: false,
    contact: false,
    default_contact: false,
  }
}

function account(overrides: Partial<AdminAccount> = {}): AdminAccount {
  return accountFixture({
    id: UUID,
    display_name: 'Ada Lovelace',
    identifiers: [usernameIdentifier('ada'), emailIdentifier('ada@example.com')],
    ...overrides,
  })
}

const COPY_VALUE_TEMPLATE = (
  uiKitResources['zh-CN'] as unknown as {
    readonly dataTable: { readonly copyValue: string }
  }
).dataTable.copyValue

function fill(template: string, values: Record<string, string>): string {
  return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) => values[key] ?? '')
}

describe('AccountIdentity', () => {
  function renderIdentity(row: AdminAccount) {
    const operations = createAdminOperations(
      makeAdminBackend({ getAccount: () => Promise.resolve(row) }),
    )
    renderWithProviders(<AccountIdentity account={row.id} />, { operations })
  }

  it('renders both the display name and the username', async () => {
    renderIdentity(account())
    expect(await screen.findByText('Ada Lovelace (ada)')).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('renders the username alone when there is no display name', async () => {
    renderIdentity(account({ display_name: '' }))
    expect(await screen.findByText('ada')).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('renders the display name alone when there is no username', async () => {
    renderIdentity(account({ identifiers: [emailIdentifier('ada@example.com')] }))
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    expect(screen.queryByText('ada@example.com')).toBeNull()
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('renders the neutral copy when neither resolves', async () => {
    renderIdentity(account({ display_name: '', identifiers: [] }))
    expect(
      await screen.findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('renders the neutral copy, never the id, when the read is refused', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () =>
          Promise.reject({ status: 403, code: 'authn.forbidden' }),
      }),
    )
    renderWithProviders(<AccountIdentity account={UUID} />, { operations })
    expect(
      await screen.findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('renders an empty placeholder when there is no actor', () => {
    const getAccount = vi.fn(() => Promise.resolve(account()))
    const operations = createAdminOperations(makeAdminBackend({ getAccount }))
    const { container } = renderWithProviders(<AccountIdentity account="" />, {
      operations,
    })
    expect(container).toHaveTextContent('')
    expect(screen.queryByText(zhCN.common.accountUnavailable)).toBeNull()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('renders a static principal as recorded and issues no account read', () => {
    const getAccount = vi.fn(() => Promise.resolve(account()))
    const operations = createAdminOperations(makeAdminBackend({ getAccount }))
    renderWithProviders(<AccountIdentity account="static.operator" />, {
      operations,
    })
    expect(screen.getByText('static.operator')).toBeInTheDocument()
    expect(getAccount).not.toHaveBeenCalled()
  })
})

describe('TenantAccountIdentity', () => {
  it('renders the display name with the username the member view carries', () => {
    renderWithProviders(
      <TenantAccountIdentity name="Ada Lovelace" username="ada" />,
    )
    expect(screen.getByText('Ada Lovelace (ada)')).toBeInTheDocument()
  })

  it('renders the username alone when there is no display name', () => {
    renderWithProviders(<TenantAccountIdentity name="" username="ada" />)
    expect(screen.getByText('ada')).toBeInTheDocument()
  })

  it('renders the display name alone when there is no username', () => {
    renderWithProviders(<TenantAccountIdentity name="Ada Lovelace" username="" />)
    expect(screen.getByText('Ada Lovelace')).toBeInTheDocument()
  })

  it('renders the neutral copy when neither resolves', () => {
    renderWithProviders(<TenantAccountIdentity name="" username="" />)
    expect(
      screen.getByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
  })
})

describe('AccountSelector', () => {
  it('labels an option by display name and username, never the account id', () => {
    renderWithProviders(
      <AccountSelector
        label="Account"
        value={account()}
        onChange={() => undefined}
      />,
    )
    expect(screen.getByRole('combobox', { name: 'Account' })).toHaveValue(
      'Ada Lovelace (ada)',
    )
    expect(screen.queryByDisplayValue(UUID)).toBeNull()
  })
})

describe('OrganizationMembersPage', () => {
  function renderMembers(username = 'ada') {
    const member = {
      account: UUID,
      tenant: TENANT,
      active: true,
      display_name: 'Ada Lovelace',
      username,
      source: 'admin',
      sso_exempt: false,
      roles: [],
      joined_at: '2026-10-15T12:00:00Z',
    }
    const operations = createAdminOperations(
      makeAdminBackend({
        listMembers: () =>
          Promise.resolve({ members: [member], next_cursor: '' }),
      }),
    )
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={operations}
        hasFeature={() => true}
      >
        <OrganizationMembersPage tenant={TENANT} />
      </OrganizationAdminProvider>,
      { operations },
    )
  }

  it('shows the username beside the display name and copies it, never the id', async () => {
    renderMembers()
    expect(await screen.findByText('Ada Lovelace')).toBeInTheDocument()
    const username = await screen.findByText('ada', { exact: true })
    const row = username.closest('tr')
    expect(row).not.toBeNull()
    expect(within(row as HTMLElement).queryByText(UUID)).toBeNull()
    expect(
      within(row as HTMLElement).getByRole('button', {
        name: fill(COPY_VALUE_TEMPLATE, { value: 'ada' }),
      }),
    ).toBeInTheDocument()
  })

  it('names the Manage control by the composed identity', async () => {
    renderMembers()
    expect(
      await screen.findByRole('button', {
        name: fill(zhCN.members.manageAria, { name: 'Ada Lovelace (ada)' }),
      }),
    ).toBeInTheDocument()
  })

  it('shows the neutral copy, not the display name again, when the username is empty', async () => {
    renderMembers('')
    await waitFor(() =>
      expect(screen.getAllByText('Ada Lovelace')).toHaveLength(1),
    )
    expect(
      await screen.findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })
})

describe('InvitationsPage', () => {
  it('leaves an unresolved inviter blank and offers no id copy', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=acme')
    try {
      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () =>
            Promise.resolve([
              {
                name: TENANT,
                display_name: 'Acme',
                kind: 'business',
                owner_account: UUID,
                status: 'active',
                contact_email: '',
                created_at: '2026-10-15T12:00:00Z',
                description: '',
                website: '',
              },
            ]),
          listInvitations: () =>
            Promise.resolve({
              invitations: [
                {
                  id: 'inv-1',
                  tenant: TENANT,
                  email: 'new@example.com',
                  status: 'pending',
                  state: 'pending',
                  invited_by: 'ghost-1',
                  expires_at: '2099-01-01T00:00:00Z',
                  accepted_at: null,
                  revoked_at: null,
                  last_sent_at: '2026-10-15T12:00:00Z',
                  send_count: 1,
                  created_at: '2026-10-15T12:00:00Z',
                },
              ],
              next_cursor: '',
            }),
          listMembers: () => Promise.resolve({ members: [], next_cursor: '' }),
        }),
      )
      renderWithProviders(<InvitationsPage />, { operations })
      const row = (await screen.findByText('new@example.com')).closest('tr')
      expect(row).not.toBeNull()
      const invitedByHeader = screen.getByRole('columnheader', {
        name: zhCN.invitations.column.invitedBy,
      })
      const invitedByCell =
        row!.children[(invitedByHeader as HTMLTableCellElement).cellIndex]!
      expect(invitedByCell.textContent?.trim()).toBe('')
      expect(screen.queryByText('ghost-1')).toBeNull()
      expect(
        screen.queryByRole('button', {
          name: fill(COPY_VALUE_TEMPLATE, { value: 'ghost-1' }),
        }),
      ).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })
})

describe('AccountsPage', () => {
  it('titles the detail panel with the composed identity and shows no id', async () => {
    const user = userEvent.setup()
    const row = account({ display_name: 'Ada Lovelace' })
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: [row], next_cursor: '' }),
        getAccount: () => Promise.resolve(row),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    fireEvent.change(screen.getByLabelText(zhCN.accounts.displayNamePrefixLabel), {
      target: { value: 'ada' },
    })
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    const identity = await screen.findByText('Ada Lovelace (ada)')
    expect(identity).toBeInTheDocument()
    expect(
      screen.getByRole('columnheader', {
        name: zhCN.accounts.column.identity,
      }),
    ).toBeInTheDocument()
    const rowElement = identity.closest('tr') as HTMLElement
    await user.click(
      within(rowElement).getByRole('button', {
        name: fill(zhCN.accounts.manageAria, { name: 'Ada Lovelace (ada)' }),
      }),
    )
    const panel = await screen.findByRole('dialog', {
      name: 'Ada Lovelace (ada)',
    })
    expect(within(panel).getByText(zhCN.accounts.detail.usernameLine.replace('{{value}}', 'ada'))).toBeInTheDocument()
    expect(within(panel).queryByText(UUID)).toBeNull()
  })

  it('shows the empty placeholder for a missing display name, not the unavailable copy', async () => {
    const user = userEvent.setup()
    const row = account({ display_name: '' })
    const operations = createAdminOperations(
      makeAdminBackend({
        listAccounts: () =>
          Promise.resolve({ accounts: [row], next_cursor: '' }),
        getAccount: () => Promise.resolve(row),
      }),
    )
    renderWithProviders(<AccountsPage />, { operations })
    fireEvent.change(screen.getByLabelText(zhCN.accounts.displayNamePrefixLabel), {
      target: { value: 'ada' },
    })
    await user.click(screen.getByRole('button', { name: zhCN.accounts.search }))
    const identity = await screen.findByText('ada')
    const rowElement = identity.closest('tr') as HTMLElement
    await user.click(
      within(rowElement).getByRole('button', {
        name: fill(zhCN.accounts.manageAria, { name: 'ada' }),
      }),
    )
    const panel = await screen.findByRole('dialog', { name: 'ada' })
    expect(
      within(panel).getByText(
        fill(zhCN.accounts.detail.displayNameLine, {
          value: zhCN.common.emptyValue,
        }),
      ),
    ).toBeInTheDocument()
    expect(within(panel).queryByText(zhCN.common.accountUnavailable)).toBeNull()
    expect(within(panel).queryByText(UUID)).toBeNull()
  })
})

describe('ImpersonationsPage', () => {
  function record() {
    return {
      id: 'imp-1',
      actor: 'actor-id-1',
      target: 'target-id-1',
      tenant: TENANT,
      session_id: 'sess-1',
      started_at: '2026-10-15T12:00:00Z',
      ends_at: '2099-01-01T00:00:00Z',
      ended_at: null,
    }
  }

  it('renders a resolved actor by display name and username', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () =>
          Promise.resolve(
            account({ id: 'actor-id-1', display_name: 'Actor One' }),
          ),
        listImpersonations: () => Promise.resolve([record()]),
      }),
    )
    renderWithProviders(<ImpersonationsPage tenant={TENANT} />, { operations })
    expect(await screen.findByText('Actor One (ada)')).toBeInTheDocument()
    expect(screen.queryByText('actor-id-1')).toBeNull()
  })
})

describe('PermissionsPage', () => {
  it('names a binding by display name and username and never the id', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () => Promise.resolve(account()),
        listBindings: () =>
          Promise.resolve([
            {
              tenant: TENANT,
              account: UUID,
              role: 'tenant-reader',
              granted_at: '2026-10-15T12:00:00Z',
            },
          ]),
      }),
    )
    renderWithProviders(<PermissionsPage tenant={TENANT} />, { operations })
    const identity = await screen.findByText('Ada Lovelace (ada)')
    const row = identity.closest('tr')
    expect(row).not.toBeNull()
    expect(within(row as HTMLElement).queryByText(UUID)).toBeNull()
    expect(
      within(row as HTMLElement).getByRole('button', {
        name: fill(zhCN.permissions.bindings.unbindAria, {
          account: 'Ada Lovelace (ada)',
          role: 'tenant-reader',
        }),
      }),
    ).toBeInTheDocument()
  })
})

describe('AuditPage', () => {
  function record(overrides: Partial<AdminAuditRecord> = {}): AdminAuditRecord {
    return {
      id: 'rec-1',
      tenant: TENANT,
      occurred_at: '2026-10-15T12:00:00Z',
      actor: UUID,
      source: 'console',
      action: 'authn.account.update',
      object: `account:${UUID}`,
      outcome: 'success',
      purpose: '',
      detail: {},
      ...overrides,
    }
  }

  it('renders the actor and the account object by identity, never the id', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () => Promise.resolve(account()),
        listRecords: () =>
          Promise.resolve({ entries: [record()], next: '' }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    await waitFor(() =>
      expect(
        screen.getAllByText('Ada Lovelace (ada)').length,
      ).toBeGreaterThanOrEqual(2),
    )
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('leaves an object that is not an account unchanged', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () => Promise.resolve(account()),
        listRecords: () =>
          Promise.resolve({
            entries: [record({ action: 'tenant.update', object: 'role:reader' })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })
    expect(await screen.findByText('role:reader')).toBeInTheDocument()
    expect(await screen.findByText('Ada Lovelace (ada)')).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it.each([
    'authn.login',
    'authn.factor.enroll',
    'authn.factor.remove',
    'authz.tenant.binding.put',
    'authz.tenant.binding.delete',
    'authn.profile.update',
    'authn.signup',
    'authn.password.reset',
  ])(
    'resolves the account object of %s, which writes the id outside the account administration vocabulary',
    async (action) => {
      const operations = createAdminOperations(
        makeAdminBackend({
          getAccount: () => Promise.resolve(account()),
          listRecords: () =>
            Promise.resolve({
              entries: [record({ action, object: `account:${UUID}` })],
              next: '',
            }),
        }),
      )
      renderWithProviders(<AuditPage />, { operations })

      await waitFor(() =>
        expect(
          screen.getAllByText('Ada Lovelace (ada)').length,
        ).toBeGreaterThanOrEqual(2),
      )
      const identities = screen.getAllByText('Ada Lovelace (ada)')
      expect(screen.queryByText(UUID)).toBeNull()
      expect(screen.queryByTitle(UUID)).toBeNull()
      expect(
        screen.queryByRole('button', { name: new RegExp(UUID) }),
      ).toBeNull()
      for (const identity of identities) {
        expect(identity.closest('td')).not.toHaveAttribute('title')
      }
    },
  )

  it('renders a bare session id as recorded and issues no account read for it', async () => {
    const sessionId = '019a2d4f-2b84-7c63-8a11-7e5f3c2d1b0a'
    const getAccount = vi.fn((query: { readonly id?: string }) =>
      query.id === UUID
        ? Promise.resolve(account())
        : Promise.reject({ status: 404, code: 'authn.account.not_found' }),
    )
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount,
        listRecords: () =>
          Promise.resolve({
            entries: [
              record({ id: 'rec-1', action: 'authn.session.revoke' }),
              record({
                id: 'rec-2',
                action: 'authn.session.revoke',
                object: sessionId,
              }),
            ],
            next: '',
          }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })

    expect(await screen.findByText(sessionId)).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
    expect(screen.queryByText(`account:${UUID}`)).toBeNull()
    await waitFor(() =>
      expect(
        screen.getAllByText('Ada Lovelace (ada)').length,
      ).toBeGreaterThanOrEqual(2),
    )
    expect(getAccount.mock.calls.some(([query]) => query.id === sessionId)).toBe(
      false,
    )
  })

  it('renders the neutral label, never the id, for a marked account the read answers 404 for', async () => {
    const deleted = '019a2d4f-3c95-7d74-9b22-8f6a4d3e2c1b'
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: (query: { readonly id?: string }) =>
          query.id === UUID
            ? Promise.resolve(account())
            : Promise.reject({ status: 404, code: 'authn.account.not_found' }),
        listRecords: () =>
          Promise.resolve({
            entries: [record({ object: `account:${deleted}` })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })

    expect(
      await screen.findByTitle(zhCN.common.accountMissing),
    ).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(screen.queryByText(deleted)).toBeNull()
    expect(await screen.findByText('Ada Lovelace (ada)')).toBeInTheDocument()
  })

  it('renders an empty placeholder for a system row with no actor', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listRecords: () =>
          Promise.resolve({
            entries: [record({ actor: '', object: 'settings.set' })],
            next: '',
          }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })

    const object = await screen.findByText('settings.set')
    expect(screen.queryByText(UUID)).toBeNull()
    expect(screen.queryByText(zhCN.common.accountUnavailable)).toBeNull()
    const actorHeader = screen.getByRole('columnheader', {
      name: zhCN.audit.column.actor,
    })
    const row = object.closest('tr')
    expect(row).not.toBeNull()
    expect(
      row!.children[(actorHeader as HTMLTableCellElement).cellIndex],
    ).toHaveTextContent('')
  })

  it('renders a static principal as recorded and issues no account read', async () => {
    const getAccount = vi.fn(() => Promise.resolve(account()))
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount,
        listRecords: () =>
          Promise.resolve({
            entries: [
              record({ actor: 'static.operator', object: 'settings.set' }),
            ],
            next: '',
          }),
      }),
    )
    renderWithProviders(<AuditPage />, { operations })

    expect(await screen.findByText('static.operator')).toBeInTheDocument()
    expect(screen.queryByText(zhCN.common.accountUnavailable)).toBeNull()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('never shows the recorded object when the admin face is absent', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getAccount: () => Promise.resolve(account()),
        listRecords: () =>
          Promise.resolve({ entries: [record()], next: '' }),
      }),
    )
    renderWithProviders(<AuditPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin',
    })

    // findAllByText resolves on the first match, so wait for the count.
    await screen.findAllByText(zhCN.common.accountUnavailable)
    expect(screen.queryByText(UUID)).toBeNull()
  })
})

describe('AuditDetailPage detail identities', () => {
  it('renders "by" by what the action makes it: a class as recorded, an account by identity', async () => {
    const factorOperations = createAdminOperations(
      makeAdminBackend({
        getRecord: () =>
          Promise.resolve(
            recordFixture({
              action: 'authn.factor.enroll',
              object: `account:${UUID}`,
              detail: { by: 'self', kind: 'totp' },
            }),
          ),
        getAccount: () => Promise.resolve(account()),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations: factorOperations })

    expect(await screen.findByText('self')).toBeInTheDocument()
    await waitFor(() => {
      expect(
        screen.getAllByText('Ada Lovelace (ada)').length,
      ).toBeGreaterThanOrEqual(2)
    })
    expect(screen.queryByText(zhCN.common.accountUnavailable)).toBeNull()

    cleanup()

    const impersonationOperations = createAdminOperations(
      makeAdminBackend({
        getRecord: () =>
          Promise.resolve(
            recordFixture({
              action: 'authn.impersonation.revoke',
              object: `account:${UUID}`,
              actor: UUID,
              detail: { by: UUID, on_behalf_of: UUID },
            }),
          ),
        getAccount: () => Promise.resolve(account()),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, {
      operations: impersonationOperations,
    })

    // Wait for actor and object to resolve before asserting the neutral copy is absent.
    await waitFor(() =>
      expect(
        screen.getAllByText('Ada Lovelace (ada)').length,
      ).toBeGreaterThanOrEqual(3),
    )
    expect(screen.queryByText(UUID)).toBeNull()
  })

  it('resolves the account detail key by identity and never renders the uuid', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getRecord: () =>
          Promise.resolve(
            recordFixture({
              action: 'authz.tenant.binding.put',
              object: 'role:reader',
              actor: UUID,
              detail: { account: UUID, role: 'reader' },
            }),
          ),
        getAccount: () => Promise.resolve(account()),
      }),
    )
    renderWithProviders(<AuditDetailPage id="rec-1" />, { operations })

    await waitFor(() => {
      expect(
        screen.getAllByText('Ada Lovelace (ada)').length,
      ).toBeGreaterThanOrEqual(2)
    })
    expect(screen.queryByText(UUID)).toBeNull()
    expect(screen.getByText('role:reader')).toBeInTheDocument()
  })
})

describe('AuditObjectIdentity', () => {
  it('resolves only a marked account object', () => {
    const operations = createAdminOperations(
      makeAdminBackend({ getAccount: () => Promise.resolve(account()) }),
    )
    const { unmount } = renderWithProviders(
      <AuditObjectIdentity object={`account:${UUID}`} />,
      { operations },
    )
    expect(screen.queryByText(`account:${UUID}`)).toBeNull()
    unmount()
    renderWithProviders(<AuditObjectIdentity object={UUID} />, { operations })
    expect(screen.getByText(UUID)).toBeInTheDocument()
  })
})
