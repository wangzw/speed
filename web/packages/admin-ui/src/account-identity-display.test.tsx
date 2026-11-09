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
/** An account id has the shape of a UUID; the value is opaque beyond that. */
const UUID = '019a2d4f-1c3e-7b52-9f00-6d7a1b2c3d4e'

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

  it('renders an empty placeholder when there is no actor', () => {
    const getAccount = vi.fn(() => Promise.resolve(account()))
    const operations = createAdminOperations(makeAdminBackend({ getAccount }))
    const { container } = renderWithProviders(<AccountIdentity account="" />, {
      operations,
    })
    // No actor is a system row, not an account that failed to resolve.
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
    // The name column carries the display name; the column beside it shows
    // the member record's login username as it is.
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
    // The name column keeps the display name; the username column beside it
    // must not repeat it, and it never falls back to the account id.
    const names = await screen.findAllByText('Ada Lovelace')
    expect(names.length).toBe(1)
    expect(
      await screen.findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(screen.queryByText(UUID)).toBeNull()
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
                name: TENANT,
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
    // The column header names the identity the cell shows, not one of its
    // parts: the cell composes the display name with the login username.
    expect(
      screen.getByRole('columnheader', {
        name: zhCN.accounts.column.identity,
      }),
    ).toBeInTheDocument()
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
    // The account has a username but no display name, so the row reads the
    // username alone. Its detail panel must not claim the account is
    // unavailable: the missing display name is an empty field, not a failed
    // resolution.
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

      // The actor and the object both resolve to the account's identity.
      const identities = await screen.findAllByText('Ada Lovelace (ada)')
      expect(identities.length).toBeGreaterThanOrEqual(2)
      // The id is neither rendered nor handed over as a title or a copy
      // value by either cell.
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
        // The same action names an account on one row and a session on the
        // next. Only the marked object is an account; the session id has the
        // same opaque shape but no marker, so it renders as recorded.
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
    expect(
      (await screen.findAllByText('Ada Lovelace (ada)')).length,
    ).toBeGreaterThanOrEqual(2)
    // The session id was never handed to the account read.
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

    // The marker says the object names an account, so a deleted one renders
    // the neutral label with the deleted note -- never the recorded id.
    expect(
      await screen.findByTitle(zhCN.common.accountMissing),
    ).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(screen.queryByText(deleted)).toBeNull()
    // The actor still resolves to its identity.
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
    // No actor means no identity, not an unresolved account: the neutral copy
    // never appears for the row.
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
    // A configured static subject is not an account the library holds.
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

    // No read is issued without the feature, so neither the actor nor the
    // object can resolve; the value may still name an account, so both cells
    // keep the neutral copy and never the recorded identifier.
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

    // `self` is the actor class of the factor event, not an account id: it
    // must render as recorded rather than as the neutral identity.
    expect(await screen.findByText('self')).toBeInTheDocument()
    // Wait for the actor and the object to resolve before the absence of the
    // neutral copy means anything: a pending read shows that copy.
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

    // On a revoke the `by` value is the account that ended the grant, so it
    // resolves like `on_behalf_of`; the object and the actor resolve too.
    const identities = await screen.findAllByText('Ada Lovelace (ada)')
    expect(identities.length).toBeGreaterThanOrEqual(3)
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

    // The detail key `account` is a keyed account field carrying a bare
    // identifier: it resolves like the actor, and the uuid never renders.
    await waitFor(() => {
      expect(
        screen.getAllByText('Ada Lovelace (ada)').length,
      ).toBeGreaterThanOrEqual(2)
    })
    expect(screen.queryByText(UUID)).toBeNull()
    // The object names a role, not an account, and stays as recorded.
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
    // A bare identifier of the same shape is not an account: it renders as
    // recorded, marker or not.
    expect(screen.getByText(UUID)).toBeInTheDocument()
  })
})
