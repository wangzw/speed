/**
 * Account identity display across the console.
 *
 * The account id is the internal key: it may be a path parameter or a row
 * key, never visible text, a copy target, an accessible name or a page
 * title. Accounts render as the display name with the login username, the
 * username alone, the display name alone, or the neutral
 * "account unavailable" copy. These cases pin that rule on the shared unit
 * and on every surface that previously fell back to the raw id.
 */

import { describe, expect, it } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { uiKitResources } from '@speed/ui-kit'
import { AccountIdentity, TenantAccountIdentity } from './internal/account-identity.js'
import { AccountSelector } from './internal/account-selector.js'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import { OrganizationMembersPage } from './OrganizationMembersPage.js'
import { InvitationsPage } from './InvitationsPage.js'
import { AccountsPage } from './AccountsPage.js'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { PermissionsPage } from './PermissionsPage.js'
import { AuditPage } from './AuditPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import type { AdminAccount, AdminAuditRecord } from './operations.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'
const UUID = 'acc-1'

/** One username identifier row, in the shape the account read carries. */
function usernameIdentifier(value: string): AdminAccount['identifiers'][number] {
  return {
    kind: 'username',
    value,
    proven: true,
    sign_in: true,
    contact: false,
    default_contact: false,
  }
}

/** One email identifier row, which must never stand in for the username. */
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
  function renderMembers() {
    const member = {
      account: UUID,
      tenant: TENANT,
      active: true,
      display_name: 'Ada Lovelace',
      username: 'ada',
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

  it('shows the composed identity and copies the username, never the id', async () => {
    renderMembers()
    const identity = await screen.findByText('Ada Lovelace (ada)')
    const row = identity.closest('tr')
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
})

describe('InvitationsPage', () => {
  it('renders the neutral copy for an unresolved inviter and offers no id copy', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=acme')
    try {
      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () =>
            Promise.resolve([
              {
                alias: TENANT,
                displayName: 'Acme',
                kind: 'business',
                ownerAccount: UUID,
                status: 'active',
                contactEmail: '',
                createdAt: '2026-10-15T12:00:00Z',
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
                  invited_by: 'ghost-1',
                  expires_at: '2099-01-01T00:00:00Z',
                  accepted_at: '0001-01-01T00:00:00Z',
                  created_at: '2026-10-15T12:00:00Z',
                },
              ],
              next_cursor: '',
            }),
          listMembers: () => Promise.resolve({ members: [], next_cursor: '' }),
        }),
      )
      renderWithProviders(<InvitationsPage />, { operations })
      expect(await screen.findByText('new@example.com')).toBeInTheDocument()
      expect(
        screen.getAllByText(zhCN.common.accountUnavailable).length,
      ).toBeGreaterThan(0)
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
    // The identity column is the row handle; open the detail from it.
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
      ended_at: '0001-01-01T00:00:00Z',
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
      object: UUID,
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
    expect(
      (await screen.findAllByText('Ada Lovelace (ada)')).length,
    ).toBeGreaterThanOrEqual(2)
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
    // The actor still resolves; only the object stays as recorded.
    expect(await screen.findByText('Ada Lovelace (ada)')).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
  })
})
