/**
 * InvitationsPage: the tenant invitation and membership page.
 *
 * It prompts for a tenant until one is chosen (the selector is fed by the
 * roster read), then lists the tenant's invitations and members and drives
 * the issue / revoke / remove writes, the membership write and the SSO
 * exemption toggle through the bundle.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import type { FeatureName } from '@speed/api-sdk/features'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InvitationsPage } from './InvitationsPage.js'
import { adminOperations } from './operations.js'
import type {
  AdminAccount,
  AdminInvitation,
  AdminMember,
  AdminMemberPage,
  AdminOperations,
  AdminSetMemberSSOExemptInput,
  AdminWriteMemberInput,
} from './operations.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { renderWithProviders, createAdminUiI18n } from '../test-utils/render.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import {
  accountFixture,
  createAdminOperations,
  issuedInvitationFixture,
  makeAdminBackend,
  tenantFixture,
  type AdminBackend,
} from '../test-utils/operations.js'
import { AdminInvitationLinkProvider } from './internal/invitation-link.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { createStepUpSession } from '../test-utils/session.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

/**
 * The member-source labels a test injects into the namespace, so a
 * translated value is distinguishable from the raw one the record carries.
 */
const SOURCE_LABELS = {
  admin: 'member-source-admin',
  initial: 'member-source-initial',
  invite: 'member-source-invite',
  jit: 'member-source-jit:{{provider}}',
} as const

/** Fills a locale template's placeholders with the values under test. */
function fill(
  template: string,
  values: Readonly<Record<string, string>>,
): string {
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replace(`{{${name}}}`, value),
    template,
  )
}

/** The account reference a notice inserts for a resolved display name. */
function byName(name: string): string {
  return fill(zhCN.common.accountReference.name, { name })
}

/** The account reference a notice inserts for an identifier alone. */
function byIdentifier(identifier: string): string {
  return fill(zhCN.common.accountReference.identifier, { identifier })
}

/** The account reference a notice inserts when both resolve. */
function byNameAndIdentifier(name: string, identifier: string): string {
  return fill(zhCN.common.accountReference.nameWithIdentifier, {
    name,
    identifier,
  })
}

function invitation(overrides: Partial<AdminInvitation> = {}): AdminInvitation {
  return {
    id: 'inv-1',
    tenant: 'acme',
    email: 'ada@example.com',
    status: 'pending',
    invited_by: 'staff-1',
    expires_at: '2099-01-01T00:00:00Z',
    accepted_at: null,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function member(overrides: Partial<AdminMember> = {}): AdminMember {
  return {
    account: 'acc-1',
    tenant: 'acme',
    active: true,
    display_name: '',
    username: 'ada',
    sso_exempt: false,
    source: 'direct',
    roles: [],
    joined_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

/** The account the picker finds: named "Ada", identified by an opaque id. */
function account(overrides: Partial<AdminAccount> = {}): AdminAccount {
  return {
    id: 'acc-2',
    kind: 'user',
    status: 'active',
    display_name: 'Ada',
    mfa_required: false,
    protected: false,
    password_set: true,
    identifiers: [
      { kind: 'username', value: 'ada', proven: true, sign_in: true, contact: false, default_contact: false },
    ],
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText(zhCN.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

/** The titled section a heading opens, which holds its group's region. */
function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

/** jsdom implements no layout; a scroll observer installs this stub. */
function stubScrollIntoView(): ReturnType<typeof vi.fn> {
  const spy = vi.fn()
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    writable: true,
    value: spy,
  })
  return spy
}

/** A promise the test settles by hand, for observing an in-flight write. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function withTenant(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listAccounts: () =>
        Promise.resolve({ accounts: [account()], next_cursor: '' }),
      ...overrides,
    }),
  )
}

/**
 * Asserts the header refresh is inert while a write is in flight: disabled
 * without the empty-scope explanation, and a forced activation issues no
 * invitation or member read.
 */
async function expectRefreshInert(
  listInvitations: ReturnType<typeof vi.fn>,
  listMembers: ReturnType<typeof vi.fn>,
  invitationReads: number,
  memberReads: number,
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
  await vi.waitFor(() => {
    expect(refresh).toBeDisabled()
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(listInvitations).toHaveBeenCalledTimes(invitationReads)
  expect(listMembers).toHaveBeenCalledTimes(memberReads)
}

describe('InvitationsPage', () => {
  afterEach(() => {
    Reflect.deleteProperty(navigator, 'clipboard')
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
  })

  it('prompts for a tenant before listing', () => {
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    expect(screen.getByText(zhCN.invitations.needTenant.title)).toBeInTheDocument()
  })

  it('does not read an empty tenant scope on refresh', async () => {
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [], next_cursor: '' }),
    )
    const listMembers = vi.fn(() => Promise.resolve([]))
    const operations = withTenant({ listInvitations, listMembers })
    renderWithProviders(<InvitationsPage />, { operations })

    // Both reads are disabled while no tenant is chosen; the refresh
    // control follows them instead of refetching with an empty tenant.
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose a tenant
    // first" text.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      zhCN.invitations.needTenant.description,
    )
    // A programmatic click, because user-event refuses a disabled target:
    // the control must stay inert even when reached from a script.
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(listInvitations).not.toHaveBeenCalled()
    expect(listMembers).not.toHaveBeenCalled()
  })

  it('disables the refresh while the invitation scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
    )
    const listMembers = vi.fn(() => Promise.resolve([member()]))
    const operations = withTenant({
      listInvitations,
      listMembers,
      issueInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    const invitationReads = listInvitations.mock.calls.length
    const memberReads = listMembers.mock.calls.length

    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await expectRefreshInert(
      listInvitations,
      listMembers,
      invitationReads,
      memberReads,
    )

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while the member scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
    )
    const listMembers = vi.fn(() =>
      Promise.resolve([member({ sso_exempt: false })]),
    )
    const operations = withTenant({
      listInvitations,
      listMembers,
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
      setMemberSsoExempt: () => pending.promise,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    // The same display name resolves for the invitation's inviter and the
    // member row; both reads have settled when it appears.
    await screen.findAllByText('Member One')
    const invitationReads = listInvitations.mock.calls.length
    const memberReads = listMembers.mock.calls.length

    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    await expectRefreshInert(
      listInvitations,
      listMembers,
      invitationReads,
      memberReads,
    )

    await act(async () => {
      pending.resolve()
    })
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=acme')
    try {
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([member()]))
      const operations = withTenant({ listInvitations, listMembers })
      renderWithProviders(<InvitationsPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: both reads run for it and the selector shows its name.
      expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
      expect(screen.queryByText(zhCN.invitations.needTenant.title)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent('Acme')
      expect(listInvitations).toHaveBeenCalledWith('acme')
      expect(listMembers).toHaveBeenCalledWith('acme', { limit: 100 })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('lists the tenant invitations and members', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      getAccount: ({ id }) =>
        Promise.resolve(
          accountFixture({
            id,
            display_name: id === 'staff-1' ? 'Staff One' : 'Member One',
          }),
        ),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
    expect(screen.getByText(zhCN.invitations.status.pending)).toBeInTheDocument()
    // The inviter resolves through the platform account read; the member row
    // carries its own display name.
    expect(screen.getByText('Staff One')).toBeInTheDocument()
    expect(screen.getByText('Member One')).toBeInTheDocument()
  })

  it("shows the inviter's display name, or the recorded id marked unresolved when the lookup is refused", async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Staff One' })),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
    first.unmount()

    const refused = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage />, { operations: refused })
    await pickTenant(user)
    // The cell renders the neutral copy with the unresolved note; the
    // recorded id never becomes the visible fallback.
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(cell).not.toHaveTextContent('staff-1')
    expect(cell).toHaveAttribute('title', zhCN.common.accountUnresolved)
  })

  it('names the platform member by the member read, never a lookup or the account id', async () => {
    const user = userEvent.setup()
    // The member table's Username column shows exactly the username the
    // member read carries: the header names that column, so the cell never
    // composes the display name into it and never issues an account read.
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Ada Lovelace' })]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const memberSection = section(zhCN.members.title)
    const nameCell = await within(memberSection).findByText('Ada Lovelace')
    const usernameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.username,
    })
    const row = nameCell.closest('tr')
    expect(row).not.toBeNull()
    const usernameCell =
      row!.children[(usernameHeader as HTMLTableCellElement).cellIndex]
    expect(usernameCell).toHaveTextContent('ada')
    expect(usernameCell).not.toHaveTextContent('Ada Lovelace (ada)')
    expect(usernameCell).not.toHaveTextContent('acc-1')
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('shows the neutral copy when the platform member has no username', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Ada Lovelace', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText(zhCN.common.accountUnavailable),
    ).toBeInTheDocument()
    expect(within(memberSection).queryByText('acc-1')).toBeNull()
  })

  it('shows the member read display name and username, never the account id', async () => {
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Ada Lovelace' })]),
      // The organization face has no platform account read; the member read's
      // display name and username are the identity even when the lookup is
      // refused.
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText('Ada Lovelace'),
    ).toBeInTheDocument()
    expect(
      within(memberSection).getByRole('columnheader', {
        name: zhCN.members.column.displayName,
      }),
    ).toBeInTheDocument()
    // The username column renders the record's username beside the display
    // name column, never the account id.
    expect(
      await within(memberSection).findByText('ada', { exact: true }),
    ).toBeInTheDocument()
    expect(within(memberSection).queryByText('acc-1')).toBeNull()
  })

  it('tenant mode names the account column by display name and username', async () => {
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({ account: 'acc-1', display_name: 'Ada Lovelace' }),
        ]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    const nameCells = await within(memberSection).findAllByText('Ada Lovelace')
    const displayNameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.displayName,
    })
    const accountHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.username,
    })
    const nameCell = nameCells.find(
      (cell) =>
        cell.closest('td')?.cellIndex ===
        (displayNameHeader as HTMLTableCellElement).cellIndex,
    )
    expect(nameCell).not.toBeUndefined()
    const row = nameCell!.closest('tr')
    expect(row).not.toBeNull()
    const accountCell = row!.children[
      (accountHeader as HTMLTableCellElement).cellIndex
    ]
    // The display name column carries the name; the username column carries
    // the member record's username, never its id.
    expect(accountCell).toHaveTextContent('ada')
    expect(accountCell).not.toHaveTextContent('acc-1')
  })

  it('pages the organization member table through every member', async () => {
    const user = userEvent.setup()
    // The read answers its rows and the cursor of the page that follows, so a
    // full first page is not mistaken for the last one.
    const firstPage = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          params?.after === undefined
            ? { members: firstPage, next_cursor: 'cursor-1' }
            : {
                members: [
                  member({ account: 'acc-100', display_name: 'Last Member' }),
                ],
                next_cursor: '',
              },
        ),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      (await within(memberSection).findAllByText('Member 0')).length,
    ).toBeGreaterThan(0)
    expect(within(memberSection).queryByText('Last Member')).toBeNull()
    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Last Member'),
    ).toBeInTheDocument()
    expect(listMembers).toHaveBeenLastCalledWith('acme', {
      limit: 100,
      after: 'cursor-1',
    })
  })

  it('blocks a second member page change while the kept previous page is loading', async () => {
    const user = userEvent.setup()
    let resolveNext: (page: AdminMemberPage) => void = () => undefined
    const listMembers = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        params?.after === 'cursor-1'
          ? new Promise<AdminMemberPage>((resolve) => {
              resolveNext = resolve
            })
          : Promise.resolve({
              members: [member({ account: 'acc-0', display_name: 'Member 0' })],
              next_cursor: 'cursor-1',
            }),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText('Member 0'),
    ).toBeInTheDocument()
    const next = within(memberSection).getByRole('button', {
      name: zhCN.common.pager.next,
    })
    await user.click(next)
    // While the next page loads, `keepPreviousData` still holds the first
    // page's envelope, whose next_cursor names the same page. The control is
    // busy for the read and the handler refuses a second advance.
    await vi.waitFor(() => expect(next).toBeDisabled())
    expect(listMembers).toHaveBeenCalledTimes(2)

    await act(async () => {
      resolveNext({
        members: [member({ account: 'acc-1', display_name: 'Last Member' })],
        next_cursor: '',
      })
    })
    expect(
      await within(memberSection).findByText('Last Member'),
    ).toBeInTheDocument()
    // Exactly one page advanced: no second read and no repeated cursor.
    expect(listMembers).toHaveBeenCalledTimes(2)
    expect(
      within(memberSection).getByText(
        zhCN.common.pager.page.replace('{{page}}', '2'),
      ),
    ).toBeInTheDocument()
  })

  it('treats an exact page as the last page without an empty next page', async () => {
    // A full page with no next cursor is the last page; the table never reads
    // past its last row into an empty page whose empty state would wrongly say
    // the tenant has no members.
    const page = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(() =>
      Promise.resolve({ members: page, next_cursor: '' }),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    expect(
      await within(memberSection).findByText('Member 0'),
    ).toBeInTheDocument()
    expect(
      within(memberSection).queryByRole('button', {
        name: zhCN.common.pager.next,
      }),
    ).toBeNull()
    expect(
      within(memberSection).queryByText(zhCN.members.empty.title),
    ).toBeNull()
    expect(listMembers).toHaveBeenCalledTimes(1)
  })

  it('returns to the first page with an empty cursor on the previous action', async () => {
    const user = userEvent.setup()
    const firstPage = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(
      (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          params?.after === undefined
            ? { members: firstPage, next_cursor: 'cursor-1' }
            : {
                members: [
                  member({ account: 'acc-100', display_name: 'Member 100' }),
                ],
                next_cursor: '',
              },
        ),
    )
    const operations = withTenant({ listMembers })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    await within(memberSection).findByText('Member 0')
    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Member 100'),
    ).toBeInTheDocument()

    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.previous,
      }),
    )
    expect(
      await within(memberSection).findByText('Member 0'),
    ).toBeInTheDocument()
    // The first page is read again by no cursor at all, not by the cursor the
    // second page left on the stack.
    await vi.waitFor(() =>
      expect(listMembers).toHaveBeenLastCalledWith('acme', { limit: 100 }),
    )
  })

  it('starts a switched tenant with no member cursor', async () => {
    const user = userEvent.setup()
    const globex = tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' })
    const firstPage = Array.from({ length: 101 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const listMembers = vi.fn(
      (
        tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          tenant === 'globex'
            ? { members: [], next_cursor: '' }
            : params?.after === undefined
              ? { members: firstPage, next_cursor: 'cursor-1' }
              : {
                  members: [
                    member({ account: 'acc-100', display_name: 'Member 100' }),
                  ],
                  next_cursor: '',
                },
        ),
    )
    const operations = withTenant({
      listTenants: () => Promise.resolve([TENANT, globex]),
      listMembers,
    })
    renderWithProviders(<InvitationsPage defaultTenant="acme" />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    await within(memberSection).findByText('Member 0')
    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Member 100'),
    ).toBeInTheDocument()

    await user.click(screen.getByLabelText(zhCN.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))

    // The new tenant's first read carries no cursor: the stack belonged to the
    // tenant the viewer left, and no frame may hand its cursor to the new one.
    await vi.waitFor(() => {
      expect(
        listMembers.mock.calls.some(([tenant]) => tenant === 'globex'),
      ).toBe(true)
    })
    for (const [tenant, params] of listMembers.mock.calls) {
      if (tenant === 'globex') {
        expect(params).toEqual({ limit: 100 })
      }
    }
  })

  it('keeps a first-page member name as an inviter after paging to the second page', async () => {
    const user = userEvent.setup()
    const firstPage = Array.from({ length: 100 }, (_, index) =>
      member({
        account: `acc-${String(index).padStart(3, '0')}`,
        display_name: `Member ${index}`,
      }),
    )
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [invitation({ invited_by: 'acc-099' })],
          next_cursor: '',
        }),
      listMembers: (
        _tenant: string,
        params?: Parameters<AdminOperations['useMembers']>[1],
      ) =>
        Promise.resolve(
          params?.after === undefined
            ? { members: firstPage, next_cursor: 'cursor-1' }
            : {
                members: [
                  member({ account: 'acc-100', display_name: 'Next Page Member' }),
                ],
                next_cursor: '',
              },
        ),
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const memberSection = section(zhCN.members.title)
    await within(memberSection).findByText('Member 0')
    const invitationSection = section(zhCN.invitations.section.title)
    expect(
      await within(invitationSection).findByText('Member 99 (ada)'),
    ).toBeInTheDocument()

    await user.click(
      within(memberSection).getByRole('button', {
        name: zhCN.common.pager.next,
      }),
    )
    expect(
      await within(memberSection).findByText('Next Page Member'),
    ).toBeInTheDocument()
    // The member who invited is not on the page now shown, but the name the
    // read resolved on page one still names the inviter.
    expect(within(memberSection).queryByText('Member 99')).toBeNull()
    expect(within(invitationSection).getByText('Member 99 (ada)')).toBeInTheDocument()
  })

  it('leaves the name cell empty and names the username when the member has no display name', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: '', username: 'ada' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    const memberSection = section(zhCN.members.title)
    const nameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.displayName,
    })
    const usernameHeader = within(memberSection).getByRole('columnheader', {
      name: zhCN.members.column.username,
    })
    const row = memberSection.querySelector('tbody tr')
    expect(row).not.toBeNull()
    const nameCell = row!.children[
      (nameHeader as HTMLTableCellElement).cellIndex
    ]
    expect(nameCell!.textContent).toBe('')
    // The username column names the row from the member read, never the
    // recorded id.
    const usernameCell =
      row!.children[(usernameHeader as HTMLTableCellElement).cellIndex]
    expect(usernameCell).toHaveTextContent('ada')
    expect(usernameCell).not.toHaveTextContent('acc-1')
  })

  it('tenant mode drops the platform account picker and resolves identities from members', async () => {
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [invitation({ invited_by: 'acc-1' })],
          next_cursor: '',
        }),
      listMembers: () =>
        Promise.resolve([
          member({ account: 'acc-1', display_name: 'Ada Lovelace' }),
        ]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    // The member row and the invitation's inviter both resolve to the one
    // display name and username pair the member read carries.
    const memberSection = section(zhCN.members.title)
    expect(
      (await within(memberSection).findAllByText('Ada Lovelace')).length,
    ).toBeGreaterThan(0)
    const invitationSection = section(zhCN.invitations.section.title)
    expect(
      (
        await within(invitationSection).findAllByText('Ada Lovelace (ada)')
      ).length,
    ).toBeGreaterThan(0)
    // The member write form searches the platform account roster, so tenant
    // mode does not render it.
    expect(screen.queryByLabelText(zhCN.members.write.accountLabel)).toBeNull()
    // Nothing resolved through the platform account read.
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('tenant mode marks an account the member read cannot resolve', async () => {
    const getAccount = vi.fn(() => Promise.reject(new Error('forbidden')))
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [invitation({ invited_by: 'ghost-1' })],
          next_cursor: '',
        }),
      listMembers: () =>
        Promise.resolve([member({ account: 'acc-1', display_name: 'Ada' })]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage tenant="acme" tenantMode />, {
      operations,
    })

    const invitationSection = section(zhCN.invitations.section.title)
    // An inviter the member read cannot resolve renders the neutral copy,
    // never the recorded identifier and never a platform lookup.
    expect(
      await within(invitationSection).findByText(
        zhCN.common.accountUnavailable,
      ),
    ).toBeInTheDocument()
    expect(within(invitationSection).queryByText('ghost-1')).toBeNull()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('reads a repeated account once across rows', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() =>
      Promise.resolve(accountFixture({ display_name: 'Staff One' })),
    )
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [
            invitation({ id: 'inv-1', invited_by: 'staff-9' }),
            invitation({ id: 'inv-2', invited_by: 'staff-9' }),
          ],
          next_cursor: '',
        }),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findAllByText('Staff One')).toHaveLength(2)
    expect(getAccount).toHaveBeenCalledTimes(1)
    expect(getAccount).toHaveBeenCalledWith({ id: 'staff-9' })
  })

  it('declares the designed priorities and caps for invitations and members', async () => {
    const user = userEvent.setup()
    const email = 'very-long-address-for-a-person@example.com'
    const invitedByName = 'A veritable paragon of administrative nomenclature'
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({
          invitations: [invitation({ email })],
          next_cursor: '',
        }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator', username: '' })]),
      getAccount: (query) =>
        Promise.resolve(
          accountFixture({
            id: query.id,
            display_name:
              query.id === 'staff-1' ? invitedByName : 'Operator',
          }),
        ),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText(email)

    const header = (scope: HTMLElement, label: string): Element =>
      within(scope).getByRole('columnheader', { name: label })

    // The invitation table: the email is the primary identity with the
    // shared cap, status stays to `sm`, the inviter to `md`, expiry the
    // first to go, and the row actions never hide.
    const inviteSection = section(zhCN.invitations.section.title)
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.email))).toBeNull()
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.status))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.invitedBy))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.expires))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(inviteSection, zhCN.invitations.column.actions))).toBeNull()
    const emailCell = screen.getByText(email).closest('td')
    expect(emailCell).toHaveAttribute('title', email)
    expect(containerMaxWidthOf(emailCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
    // The inviter is a long-text column, not the primary identity: the
    // flat 160 cap bounds the whole cell (name and copy control), so a
    // long display name cannot push the table past the md floor.
    const invitedByIdentity = await within(inviteSection).findByText(
      invitedByName,
    )
    const invitedByCell = invitedByIdentity.closest('td')
    expect(invitedByCell).not.toBeNull()
    // No copy control offers the recorded account id.
    expect(
      within(invitedByCell as HTMLElement).queryByRole('button'),
    ).toBeNull()
    const invitedByWrapper = invitedByCell!.firstElementChild as HTMLElement
    expect(invitedByWrapper).toHaveStyle({ maxWidth: '160px' })
    expect(containerMaxWidthOf(invitedByWrapper)).toEqual(new Map())

    // The members table keeps the display name as the always-visible primary
    // identity; the account identifier beside it survives from sm, active
    // yields at that tier, source stays from md, SSO exemption from lg.
    const memberSection = section(zhCN.members.title)
    expect(columnVisibility(header(memberSection, zhCN.members.column.displayName))).toBeNull()
    expect(columnVisibility(header(memberSection, zhCN.members.column.username))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.active))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.source))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.ssoExempt))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(memberSection, zhCN.members.column.actions))).toBeNull()
    const memberIdentity = await within(memberSection).findByText('Operator')
    expect(containerMaxWidthOf(memberIdentity.closest('td')!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    const gated = [
      screen.getByRole('button', { name: zhCN.common.refresh }),
      screen.getByRole('button', { name: zhCN.invitations.revoke }),
      screen.getByRole('button', { name: zhCN.members.ssoExemptSet }),
      screen.getByRole('button', { name: zhCN.members.remove }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('issues an invitation through the bundle', async () => {
    const user = userEvent.setup()
    const issued: string[] = []
    const operations = withTenant({
      issueInvitation: (input) => {
        issued.push(input.email)
        return Promise.resolve(issuedInvitationFixture())
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await vi.waitFor(() => {
      expect(issued).toEqual(['new@example.com'])
    })
  })

  it('takes the invitation expiry from a date-time field and sends the instant', async () => {
    const user = userEvent.setup()
    const expiresSent: (string | undefined)[] = []
    const operations = withTenant({
      issueInvitation: (input) => {
        expiresSent.push(input.expiresAt)
        return Promise.resolve(issuedInvitationFixture())
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    // The expiry is a date-time control, not a free-text box.
    const expires = screen.getByLabelText(zhCN.invitations.issue.expiresLabel)
    expect(expires).toHaveAttribute('type', 'datetime-local')
    fireEvent.change(expires, { target: { value: '2026-11-01T12:00' } })
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await vi.waitFor(() => {
      expect(expiresSent).toEqual([
        new Date('2026-11-01T12:00').toISOString(),
      ])
    })
  })

  it('shows the acceptance link for the invitation just issued', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            invitation: {
              ...issuedInvitationFixture().invitation,
              email: 'new@example.com',
            },
          }),
        ),
    })
    renderWithProviders(
      <AdminInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </AdminInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    // The one-time token is turned into the host's acceptance link, not dropped.
    expect(
      await screen.findByText(
        fill(zhCN.invitations.issued.link, { email: 'new@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        `${window.location.origin}/invitations/spd_inv_test_token`,
      ),
    ).toBeInTheDocument()
  })

  it('copies the complete acceptance link through an accessible control', async () => {
    const user = userEvent.setup()
    // user-event installs its own clipboard stub at setup, so the spy
    // replaces it afterwards.
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            invitation: {
              ...issuedInvitationFixture().invitation,
              email: 'new@example.com',
            },
          }),
        ),
    })
    renderWithProviders(
      <AdminInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </AdminInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    const link = `${window.location.origin}/invitations/spd_inv_test_token`
    await screen.findByText(link)
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issued.copyLink }))
    await vi.waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(link)
    })
  })

  it('shows the bare token when the host supplies no link builder', async () => {    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    // With no builder the token is still surfaced, never silently dropped.
    expect(
      await screen.findByText(fill(zhCN.invitations.issued.token, { email: 'ada@example.com' })),
    ).toBeInTheDocument()
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
  })

  it.each([
    ['failed', zhCN.invitations.issued.delivery.failed],
    ['not_configured', zhCN.invitations.issued.delivery.not_configured],
  ] as const)(
    'keeps the copy control for the %s delivery outcome',
    async (delivery, expected) => {
      const user = userEvent.setup()
      const operations = withTenant({
        issueInvitation: () =>
          Promise.resolve(issuedInvitationFixture({ email_delivery: delivery })),
      })
      renderWithProviders(<InvitationsPage />, { operations })
      await pickTenant(user)
      await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
      await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
      expect(await screen.findByText(expected)).toBeInTheDocument()
      // The carrier did not deliver the invitation, so the operator still has
      // to hand the link over: the token and its copy control stay.
      expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
      expect(
        screen.getByRole('button', {
          name: zhCN.invitations.issued.copyToken,
        }),
      ).toBeInTheDocument()
    },
  )

  it('says the invitation was emailed and offers no copy when the answer carries no token', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        // A sent notice means the recipient already holds the link, so the
        // answer carries no token.
        Promise.resolve(issuedInvitationFixture({ email_delivery: 'sent' })),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    const emailed = await screen.findByText(
      fill(zhCN.invitations.issued.emailed, { email: 'ada@example.com' }),
    )
    // Nothing was handed out, so the panel shows neither a token nor a
    // control that would copy an empty value.
    const panel = emailed.closest('[role="status"]') as HTMLElement
    expect(within(panel).queryByText('spd_inv_test_token')).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.invitations.issued.copyLink,
      }),
    ).toBeNull()
    expect(
      within(panel).queryByRole('button', {
        name: zhCN.invitations.issued.copyToken,
      }),
    ).toBeNull()
  })

  it('keeps the copy control for a sent answer that still carries a token', async () => {
    // Belt and braces: the panel keys off the value actually handed out, so a
    // response that reports `sent` but still carries a token keeps its copy
    // control rather than hiding the only copy.
    const user = userEvent.setup()
    const operations = withTenant({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            email_delivery: 'sent',
            token: 'spd_inv_override_token',
          }),
        ),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(await screen.findByText('spd_inv_override_token')).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: zhCN.invitations.issued.copyToken,
      }),
    ).toBeInTheDocument()
  })

  it('shows the acceptance value in a named, closable live region', async () => {
    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))

    const display = await screen.findByText(
      fill(zhCN.invitations.issued.token, { email: 'ada@example.com' }),
    )
    expect(display.closest('[role="status"]')).not.toBeNull()
    // The close control names the tenant and the invitee it belongs to.
    const close = screen.getByRole('button', {
      name: fill(zhCN.invitations.issued.closeAria, {
        tenant: 'acme',
        email: 'ada@example.com',
      }),
    })
    await user.click(close)
    expect(screen.queryByText('spd_inv_test_token')).toBeNull()
    // The scope's success confirmation stands beside the display and is not
    // the display: closing the display leaves it in place.
    expect(screen.getByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }))).toBeInTheDocument()
  })

  it('keeps the acceptance value through a reset of the creating scope', async () => {
    const user = userEvent.setup()
    let attempts = 0
    const operations = withTenant({
      issueInvitation: () => {
        attempts += 1
        return attempts === 1
          ? Promise.resolve(issuedInvitationFixture())
          : Promise.reject({ status: 403, code: 'authn.forbidden' })
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    await screen.findByText('spd_inv_test_token')

    // A second attempt resets the creating scope and settles as a failure.
    // The display is page state, not slot state: it survives both.
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
    expect(screen.queryByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }))).toBeNull()
  })

  it('revokes a pending invitation through the bundle', async () => {
    const user = userEvent.setup()
    const revoked: string[] = []
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      revokeInvitation: (input) => {
        revoked.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.revoke }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await vi.waitFor(() => {
      expect(revoked).toEqual(['inv-1'])
    })
  })

  it('removes a membership through the bundle', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      removeMember: (input) => {
        removed.push(input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    await vi.waitFor(() => {
      expect(removed).toEqual(['acc-1'])
    })
  })

  it('names the removed member by the member read and never the recorded id', async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    let dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(
      await screen.findByText(
        fill(zhCN.members.removed, {
          account: byNameAndIdentifier('Member One', 'ada'),
        }),
      ),
    ).toBeInTheDocument()
    first.unmount()

    // A member with neither a display name nor a username resolves nothing,
    // so the success copy stays neutral: the recorded id is a machine value
    // the operator cannot match to a roster row.
    const unnamed = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: '', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations: unnamed })
    await pickTenant(user)
    await screen.findByText(zhCN.common.accountUnavailable)
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    const notice = await screen.findByText(zhCN.members.removedUnnamed)
    expect(notice).not.toHaveTextContent('acc-1')
  })

  // The removal confirmation names the member the way the roster shows it:
  // from the member read -- display name and login username, either alone.
  // The account id is a machine value the operator cannot match against a
  // roster row, so it never enters the sentence: a member with neither a
  // display name nor a username gets the neutral sentence.
  const MEMBER_UUID = '01a170b2-7c4d-7e2f-9a11-6b0c2f9e4d31'

  async function openRemovalConfirmation(
    user: ReturnType<typeof userEvent.setup>,
    options: {
      readonly member?: Partial<AdminMember>
      readonly hasFeature?: (name: FeatureName) => boolean
    } = {},
  ): Promise<HTMLElement> {
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({
            account: MEMBER_UUID,
            display_name: 'Ada Lovelace',
            username: 'ada',
            ...options.member,
          }),
        ]),
    })
    renderWithProviders(<InvitationsPage />, {
      operations,
      ...(options.hasFeature === undefined
        ? {}
        : { hasFeature: options.hasFeature }),
    })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: zhCN.members.remove }),
    )
    return screen.findByRole('dialog', { name: zhCN.members.removeTitle })
  }

  function expectNeutralRemovalConfirmation(dialog: HTMLElement): void {
    expect(
      within(dialog).getByText(zhCN.members.removeMessageUnnamed),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  }

  it('names the member being removed by its display name and login username', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user)
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessageWithIdentifier, {
          name: 'Ada Lovelace',
          identifier: 'ada',
        }),
      ),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  })

  it('names the member being removed by its display name when there is no username', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user, {
      member: { username: '' },
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessage, { name: 'Ada Lovelace' }),
      ),
    ).toBeInTheDocument()
  })

  it('names the member being removed by its username when the display name is empty', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user, {
      member: { display_name: '' },
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessageByIdentifier, { identifier: 'ada' }),
      ),
    ).toBeInTheDocument()
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  })

  it('keeps the member UUID out of the removal confirmation', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user)
    expect(dialog).not.toHaveTextContent(MEMBER_UUID)
  })

  it('uses the neutral sentence when the member has neither display name nor username', async () => {
    const user = userEvent.setup()
    const dialog = await openRemovalConfirmation(user, {
      member: { display_name: '', username: '' },
    })
    expectNeutralRemovalConfirmation(dialog)
  })

  it('names the member from the member read without the authn.admin feature', async () => {
    const user = userEvent.setup()
    const getAccount = vi.fn(() =>
      Promise.reject(new Error('the member row must not issue a read')),
    )
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({
            account: MEMBER_UUID,
            display_name: 'Ada Lovelace',
            username: 'ada',
          }),
        ]),
      getAccount,
    })
    renderWithProviders(<InvitationsPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin',
    })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: zhCN.members.remove }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.members.removeTitle,
    })
    expect(
      within(dialog).getByText(
        fill(zhCN.members.removeMessageWithIdentifier, {
          name: 'Ada Lovelace',
          identifier: 'ada',
        }),
      ),
    ).toBeInTheDocument()
    expect(getAccount).not.toHaveBeenCalled()
  })

  it('writes the chosen account id, not the typed name', async () => {
    const user = userEvent.setup()
    const written: AdminWriteMemberInput[] = []
    const operations = withTenant({
      writeMember: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    // No account chosen: the picker holds no id yet.
    expect(
      screen.getByRole('button', { name: zhCN.members.write.submit }),
    ).toBeDisabled()
    // A display name finds the account; the write carries its id ("acc-2"),
    // never the typed name.
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    await vi.waitFor(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true, sso_exempt: false },
      ])
    })
    // The landed write clears both the selection and the box that named it.
    expect(screen.getByLabelText(zhCN.members.write.accountLabel)).toHaveValue('')
  })

  // The four success notices a member row's writes produce name the account
  // in the shared four shapes and never by its opaque id. Each case asserts
  // the recorded id is absent from the notice.
  const MEMBER_ACTIONS = [
    {
      action: 'remove',
      named: zhCN.members.removed,
      unnamed: zhCN.members.removedUnnamed,
    },
    {
      action: 'sso-saved',
      named: zhCN.members.ssoExemptSaved,
      unnamed: zhCN.members.ssoExemptSavedUnnamed,
    },
    {
      action: 'sso-cleared',
      named: zhCN.members.ssoExemptCleared,
      unnamed: zhCN.members.ssoExemptClearedUnnamed,
    },
    {
      action: 'write',
      named: zhCN.members.write.saved,
      unnamed: zhCN.members.write.savedUnnamed,
    },
  ] as const

  const NOTICE_IDENTIFIERS: AdminAccount['identifiers'] = [
    { kind: 'username', value: 'ada', proven: true, sign_in: true, contact: false, default_contact: false },
  ]

  /** Runs one member write with the given identity and returns its notice. */
  async function driveMemberNotice(
    user: ReturnType<typeof userEvent.setup>,
    action: (typeof MEMBER_ACTIONS)[number]['action'],
    displayName: string,
    identifiers: AdminAccount['identifiers'],
    expected: string,
  ): Promise<HTMLElement> {
    // A member row names its account from the member read: the display name
    // and login username it carries, never the opaque id.
    const username =
      identifiers.find((identifier) => identifier.kind === 'username')?.value ??
      ''
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([
          member({
            sso_exempt: action === 'sso-cleared',
            display_name: displayName,
            username,
          }),
        ]),
      listAccounts: () =>
        Promise.resolve({
          accounts: [account({ display_name: displayName, identifiers })],
          next_cursor: '',
        }),
      removeMember: () => Promise.resolve(),
      setMemberSsoExempt: () => Promise.resolve(),
      writeMember: () => Promise.resolve(),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    if (action === 'write') {
      // The picker's option names the account by its display name beside the
      // id, or by the id alone when it has none.
      const optionName =
        displayName === '' && identifiers.length === 0
          ? zhCN.common.accountUnavailable
          : displayName === ''
            ? 'ada'
            : `${displayName} (ada)`
      await chooseAccount(
        user,
        zhCN.members.write.accountLabel,
        'Ada',
        optionName,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.members.write.submit }),
      )
    } else {
      // The row's identity comes from the member read, so the action names
      // the account as soon as the row renders.
      await screen.findByRole('button', { name: zhCN.members.remove })
      if (action === 'remove') {
        await user.click(
          screen.getByRole('button', { name: zhCN.members.remove }),
        )
        const dialog = await screen.findByRole('dialog')
        await user.click(
          within(dialog).getByRole('button', { name: zhCN.common.confirm }),
        )
      } else {
        await user.click(
          screen.getByRole('button', {
            name:
              action === 'sso-saved'
                ? zhCN.members.ssoExemptSet
                : zhCN.members.ssoExemptClear,
          }),
        )
      }
    }

    return screen.findByText(expected)
  }

  describe.each(MEMBER_ACTIONS)('members notice: $action', ({ action, named, unnamed }) => {
    it('names the account by display name and first identifier, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveMemberNotice(
        user,
        action,
        'Ada',
        NOTICE_IDENTIFIERS,
        fill(named, { account: byNameAndIdentifier('Ada', 'ada') }),
      )
      expect(notice).toBeInTheDocument()
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })

    it('names the account by its first identifier when the display name is empty, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveMemberNotice(
        user,
        action,
        '',
        NOTICE_IDENTIFIERS,
        fill(named, { account: byIdentifier('ada') }),
      )
      expect(notice).toBeInTheDocument()
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })

    it('uses the neutral sentence when neither resolves, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveMemberNotice(user, action, '', [], unnamed)
      expect(notice).toBeInTheDocument()
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })
  })

  it('re-reads the member list after a landed write and leaves it alone after a refusal', async () => {
    const user = userEvent.setup()
    let memberRows = 0
    let writeFails = false
    const calls = bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/system/tenants') {
        return { tenants: [TENANT] }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/invitations'
      ) {
        return { invitations: [], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/tenants/acme/members'
      ) {
        memberRows += 1
        return {
          members:
            memberRows > 1
              ? [member({ account: 'acc-2', display_name: 'Ada', username: 'ada' })]
              : [],
          next_cursor: '',
        }
      }
      if (call.method === 'GET' && call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [account()], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === '/api/v1/system/auth/accounts/acc-2'
      ) {
        return { account: account() }
      }
      if (
        call.method === 'PUT' &&
        call.path === '/api/v1/tenants/acme/members/acc-2'
      ) {
        if (writeFails) {
          throw { status: 403, code: 'client.http.403' }
        }
        return undefined
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(<InvitationsPage />, { operations: adminOperations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.members.empty.title)).toBeInTheDocument()
    const listReads = (): number =>
      calls.filter(
        (entry) =>
          entry.method === 'GET' &&
          entry.path === '/api/v1/tenants/acme/members',
      ).length

    // A landed write re-runs the member read, so the row it created is on
    // screen without the operator pressing refresh.
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    expect(
      await screen.findByText(
        fill(zhCN.members.write.saved, {
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      ),
    ).toBeInTheDocument()
    // The member row renders from the member read: display name and login
    // username in their own columns.
    const memberSection = section(zhCN.members.title)
    expect(await within(memberSection).findByText('Ada')).toBeInTheDocument()
    expect(within(memberSection).getByText('ada')).toBeInTheDocument()
    expect(listReads()).toBe(2)

    // A refused write leaves the list as it was: no read follows it.
    writeFails = true
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    expect(await within(section(zhCN.members.title)).findByRole('alert')).toBeInTheDocument()
    expect(listReads()).toBe(2)
  })

  it('toggles a member SSO exemption through the bundle', async () => {
    const user = userEvent.setup()
    const toggled: AdminSetMemberSSOExemptInput[] = []
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ sso_exempt: false, display_name: 'Member One' })]),
      setMemberSsoExempt: (input) => {
        toggled.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    await vi.waitFor(() => {
      expect(toggled).toEqual([
        { tenant: 'acme', account: 'acc-1', sso_exempt: true },
      ])
    })
  })

  it('renders the retryable error placeholder when a read fails', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.invitations.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.invitations.error.description)).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when a read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    // The refusal is a permission denial, not a load failure: the
    // permission text, and no retry (a retry would only be refused again).
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.invitations.retry })).toBeNull()
    // A read's refusal never settles into a write scope's slot.
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it("reports the invitation and member failures in their own sections", async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
      issueInvitation: () => Promise.reject({ code: 'authn.conflict' }),
      removeMember: () => Promise.reject({ code: 'authn.forbidden' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    // The invitation section refuses the issue.
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(await within(section(zhCN.invitations.section.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.conflict,
    )

    // The member section refuses the removal.
    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    // The modal hides the page from assistive technology until its exit
    // transition ends; the region is readable after that.
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await within(section(zhCN.members.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )

    // Neither section carries the other's text, and no page-level banner
    // aggregates them.
    expect(
      within(section(zhCN.invitations.section.title)).queryByText(zhCN.errors.authn.forbidden),
    ).toBeNull()
    expect(
      within(section(zhCN.members.title)).queryByText(zhCN.errors.authn.conflict),
    ).toBeNull()
    expect(screen.getAllByRole('alert')).toHaveLength(2)
  })

  it('offers the enterprise re-login exit for an SSO refusal on an invitation write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      issueInvitation: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )

    // The tenant admission layer's refusal names the enterprise sign-in and
    // offers the re-login exit. No local factor dialog opens, so the exit is
    // the only banner on the page.
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.submit }),
    ).toBeNull()
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })
    await expectNoAxeViolations()

    // The exit ends the session so the host's session gate returns to the
    // sign-in surface, where the operator signs in through the provider.
    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it("keeps a plain invitation refusal's own text and offers no SSO exit", async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      issueInvitation: () =>
        Promise.reject({ status: 409, code: 'authn.conflict' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)

    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.conflict,
    )
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeNull()
  })

  it('offers the enterprise re-login exit for an SSO refusal on the invitation list read', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const logout = vi.spyOn(session, 'logout').mockResolvedValue(undefined)
    const operations = withTenant({
      listInvitations: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)

    // A read the tenant admission layer refuses names the enterprise sign-in
    // and offers the re-login exit, as a refused write does. No retry stands
    // beside it: the same request would be refused again while the session is
    // a local one.
    expect(
      await screen.findByText(zhCN.errors.authn.sso_required),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.invitations.retry }),
    ).toBeNull()
    const relogin = screen.getByRole('button', { name: zhCN.stepUp.relogin })

    // The exit ends the session so the host's session gate returns to the
    // sign-in surface, where the operator signs in through the provider.
    await user.click(relogin)
    expect(logout).toHaveBeenCalledTimes(1)
  })

  it('shows one banner with the re-login exit for an SSO refusal on a member write', async () => {
    const user = userEvent.setup()
    const session = createStepUpSession()
    const operations = withTenant({
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator' })]),
      setMemberSsoExempt: () =>
        Promise.reject({ status: 401, code: 'authn.sso_required' }),
    })
    renderWithProviders(<InvitationsPage />, { operations, session })
    await pickTenant(user)
    await screen.findAllByText('Operator')

    await user.click(
      screen.getByRole('button', { name: zhCN.members.ssoExemptSet }),
    )

    const memberSection = section(zhCN.members.title)
    const alert = await within(memberSection).findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.authn.sso_required)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(
      within(memberSection).getByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeInTheDocument()
  })

  it('marks the refused invitation row inline and brings its region into view', async () => {
    mockMatchMedia(false)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      revokeInvitation: () => Promise.reject({ code: 'authn.forbidden' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    const revoke = await screen.findByRole('button', { name: zhCN.invitations.revoke })
    await user.click(revoke)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))

    // The failing row carries the marker and the resolved reason; the
    // region carries the same text.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(revoke).toHaveAttribute('aria-invalid', 'true')
    const hintId = revoke.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    expect(document.getElementById(hintId as string)?.textContent).toBe(
      screen.getByRole('alert').textContent,
    )
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    })
    await expectNoAxeViolations()
  })

  it('drops the refused row marker when the next issue starts', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      revokeInvitation: () => Promise.reject({ code: 'authn.forbidden' }),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    const revoke = await screen.findByRole('button', {
      name: zhCN.invitations.revoke,
    })
    await user.click(revoke)
    const dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: zhCN.common.confirm }),
    )
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authn.forbidden,
    )
    expect(revoke).toHaveAttribute('aria-invalid', 'true')

    // The issue and the revoke share the invitation scope: a new attempt
    // starts from a clean result, so the refused row's marker and its inline
    // reason do not outlive the result it described.
    await user.type(
      screen.getByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    expect(
      await screen.findByText(
        fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }),
      ),
    ).toBeInTheDocument()
    expect(revoke).toHaveAttribute('aria-invalid', 'false')
  })

  it('names the changed object on each landed write', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator', username: '' })]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    // Issue names the invitee.
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))
    expect(
      await screen.findByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' })),
    ).toBeInTheDocument()

    // The membership write names the chosen account.
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    expect(
      await screen.findByText(
        fill(zhCN.members.write.saved, {
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      ),
    ).toBeInTheDocument()

    // The SSO exemption names the member by its readable identity.
    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    expect(
      await screen.findByText(fill(zhCN.members.ssoExemptSaved, { account: byName('Operator') })),
    ).toBeInTheDocument()

    // The removal names the removed member by the same identity.
    await user.click(screen.getByRole('button', { name: zhCN.members.remove }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.common.confirm }))
    expect(
      await screen.findByText(fill(zhCN.members.removed, { account: byName('Operator') })),
    ).toBeInTheDocument()
  })

  it('disables the tenant selector while a write is in flight and clears both scopes when the tenant changes', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({
      listTenants: () =>
        Promise.resolve([
          TENANT,
          tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
        ]),
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Operator', username: '' })]),
      issueInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.invitations.issue.emailLabel), 'new@example.com')
    await user.click(screen.getByRole('button', { name: zhCN.invitations.issue.submit }))

    // The tenant feeds every scope on the page: it is out of reach while
    // the issue is in flight.
    expect(screen.getByLabelText(zhCN.common.tenant)).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' })),
    ).toBeInTheDocument()

    // A member write lands in the other scope, so both carry a result.
    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: zhCN.members.ssoExemptSet }))
    expect(
      await screen.findByText(fill(zhCN.members.ssoExemptSaved, { account: byName('Operator') })),
    ).toBeInTheDocument()

    // Another tenant makes both results describe a tenant left behind.
    await user.click(screen.getByLabelText(zhCN.common.tenant))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))
    expect(screen.queryByText(fill(zhCN.invitations.issued.confirm, { email: 'ada@example.com' }))).toBeNull()
    expect(screen.queryByText(fill(zhCN.members.ssoExemptSaved, { account: byName('Operator') }))).toBeNull()
  })

  it('re-reads the invitation and member lists on refresh', async () => {
    const user = userEvent.setup()
    const listInvitations = vi.fn(() =>
      Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
    )
    const listMembers = vi.fn(() => Promise.resolve([member()]))
    const operations = withTenant({ listInvitations, listMembers })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')

    const invitationBefore = listInvitations.mock.calls.length
    const memberBefore = listMembers.mock.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await vi.waitFor(() =>
      expect(listInvitations.mock.calls.length).toBe(invitationBefore + 1),
    )
    await vi.waitFor(() =>
      expect(listMembers.mock.calls.length).toBe(memberBefore + 1),
    )
  })

  it('locks the scope to a controlled tenant and hides the selector', async () => {
    // The URL names a different tenant: a controlled page ignores it.
    window.history.replaceState({}, '', '/admin/invitations?tenant=globex')
    try {
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([]))
      const operations = withTenant({ listInvitations, listMembers })
      renderWithProviders(<InvitationsPage tenant="acme" />, { operations })

      await vi.waitFor(() => {
        expect(listInvitations).toHaveBeenCalledWith('acme')
      })
      expect(listMembers).toHaveBeenCalledWith('acme', { limit: 100 })
      expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('seeds from defaultTenant ahead of the URL and reports a re-selection', async () => {
    window.history.replaceState({}, '', '/admin/invitations?tenant=globex')
    try {
      const user = userEvent.setup()
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([]))
      const onTenantChange = vi.fn()
      const operations = withTenant({
        listTenants: () =>
          Promise.resolve([
            TENANT,
            tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
          ]),
        listInvitations,
        listMembers,
      })
      renderWithProviders(
        <InvitationsPage defaultTenant="acme" onTenantChange={onTenantChange} />,
        { operations },
      )

      await vi.waitFor(() => {
        expect(listInvitations).toHaveBeenCalledWith('acme')
      })
      await vi.waitFor(() => {
        expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent(
          'Acme',
        )
      })

      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await vi.waitFor(() => {
        expect(listInvitations).toHaveBeenCalledWith('globex')
      })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('keeps the SSO exemption out of the member surface when its feature is absent', async () => {
    const user = userEvent.setup()
    const written: AdminWriteMemberInput[] = []
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([member({ display_name: 'Member One' })]),
      writeMember: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.admin.sso',
    })
    await pickTenant(user)
    await screen.findByText('Member One')

    // The column, the row toggle and the form's checkbox all belong to the
    // absent feature; nothing of the exemption remains.
    expect(
      screen.queryByRole('columnheader', {
        name: zhCN.members.column.ssoExempt,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.members.ssoExemptSet }),
    ).toBeNull()
    expect(
      screen.queryByLabelText(zhCN.members.write.ssoExemptLabel),
    ).toBeNull()

    // The membership write still works, and carries no exemption field.
    await chooseAccount(user, zhCN.members.write.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.members.write.submit }))
    await vi.waitFor(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true },
      ])
    })
  })

  it('translates the member sources it knows and leaves an unknown one as recorded', async () => {
    const user = userEvent.setup()
    const i18n = createAdminUiI18n()
    i18n.addResourceBundle(
      'zh-CN',
      ADMIN_UI_NAMESPACE,
      { members: { source: SOURCE_LABELS } },
      true,
      true,
    )
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [], next_cursor: '' }),
      listMembers: () =>
        Promise.resolve([
          member({ account: 'acc-admin', source: 'admin' }),
          member({ account: 'acc-initial', source: 'initial' }),
          member({ account: 'acc-invite', source: 'invite' }),
          member({ account: 'acc-jit', source: 'jit:acme-idp' }),
          member({ account: 'acc-import', source: 'import' }),
        ]),
      getAccount: ({ id }) =>
        Promise.resolve(accountFixture({ id, display_name: id })),
    })
    renderWithProviders(<InvitationsPage />, { operations, i18n })
    await pickTenant(user)

    expect(await screen.findByText(SOURCE_LABELS.admin)).toBeInTheDocument()
    expect(screen.getByText(SOURCE_LABELS.initial)).toBeInTheDocument()
    expect(screen.getByText(SOURCE_LABELS.invite)).toBeInTheDocument()
    expect(
      screen.getByText(fill(SOURCE_LABELS.jit, { provider: 'acme-idp' })),
    ).toBeInTheDocument()
    // A source outside the closed set keeps the recorded text.
    expect(screen.getByText('import')).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('ada@example.com')
    await expectNoAxeViolations()
  })
})
