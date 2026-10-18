/**
 * InvitationsPage: the tenant invitation and membership page.
 *
 * It prompts for a tenant until one is chosen (the selector is fed by the
 * roster read), then lists the tenant's invitations and members and drives
 * the issue / revoke / remove writes, the membership write and the SSO
 * exemption toggle through the bundle.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InvitationsPage } from './InvitationsPage.js'
import { consoleOperations } from './operations.js'
import type {
  ConsoleAccount,
  ConsoleInvitation,
  ConsoleMember,
  ConsoleSetMemberSSOExemptInput,
  ConsoleWriteMemberInput,
} from './operations.js'
import { bindConsoleRequest } from '../test-utils/request-rig.js'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import { renderWithProviders } from '../test-utils/render.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import {
  accountFixture,
  createConsoleOperations,
  issuedInvitationFixture,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { ConsoleInvitationLinkProvider } from './internal/invitation-link.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

function invitation(overrides: Partial<ConsoleInvitation> = {}): ConsoleInvitation {
  return {
    id: 'inv-1',
    tenant: 'acme',
    email: 'ada@example.com',
    status: 'pending',
    invited_by: 'staff-1',
    expires_at: '2026-10-20T00:00:00Z',
    accepted_at: '0001-01-01T00:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function member(overrides: Partial<ConsoleMember> = {}): ConsoleMember {
  return {
    account: 'acc-1',
    tenant: 'acme',
    active: true,
    sso_exempt: false,
    source: 'direct',
    ...overrides,
  }
}

/** The account the picker finds: named "Ada", identified by an opaque id. */
function account(overrides: Partial<ConsoleAccount> = {}): ConsoleAccount {
  return {
    id: 'acc-2',
    kind: 'user',
    status: 'active',
    display_name: 'Ada',
    mfa_required: false,
    protected: false,
    password_set: true,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('租户'))
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
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
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
  const refresh = screen.getByRole('button', { name: '刷新' })
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
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
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
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose a tenant
    // first" text.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      '邀请与成员都按租户存储，请先选择一个租户。',
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

    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
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

    await user.click(screen.getByRole('button', { name: '豁免 SSO' }))
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
    window.history.replaceState({}, '', '/console/invitations?tenant=acme')
    try {
      const listInvitations = vi.fn(() =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      )
      const listMembers = vi.fn(() => Promise.resolve([member()]))
      const operations = withTenant({ listInvitations, listMembers })
      renderWithProviders(<InvitationsPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: both reads run for it and the selector shows its alias.
      expect(await screen.findByText('ada@example.com')).toBeInTheDocument()
      expect(screen.queryByText('请选择租户')).toBeNull()
      expect(screen.getByLabelText('租户')).toHaveTextContent('Acme')
      expect(listInvitations).toHaveBeenCalledWith('acme')
      expect(listMembers).toHaveBeenCalledWith('acme')
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('lists the tenant invitations and members', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
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
    expect(screen.getByText('待接受')).toBeInTheDocument()
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
    // The cell keeps the recorded id but labels it unresolved rather than
    // passing it off as a resolved name.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('staff-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
  })

  it("shows the member's display name, or the recorded id marked unresolved when the lookup is refused", async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    expect(await screen.findByText('Member One')).toBeInTheDocument()
    first.unmount()

    const refused = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage />, { operations: refused })
    await pickTenant(user)
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-1')
    expect(cell).toHaveAttribute('title', '身份未解析（需账户读取权限）')
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
      listMembers: () => Promise.resolve([member()]),
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
    const inviteSection = section('邀请')
    expect(columnVisibility(header(inviteSection, '邮箱'))).toBeNull()
    expect(columnVisibility(header(inviteSection, '状态'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header(inviteSection, '邀请人'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(inviteSection, '过期时间'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(inviteSection, '操作'))).toBeNull()
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
    expect(
      await within(inviteSection).findByText(invitedByName),
    ).toBeInTheDocument()
    const invitedByCell = within(inviteSection)
      .getByRole('button', { name: '复制 staff-1' })
      .closest('td')
    expect(invitedByCell).not.toBeNull()
    const invitedByWrapper = invitedByCell!.firstElementChild as HTMLElement
    expect(invitedByWrapper).toHaveStyle({ maxWidth: '160px' })
    expect(containerMaxWidthOf(invitedByWrapper)).toEqual(new Map())

    // The members table has the same shape: account primary, active high,
    // source medium, SSO exemption low.
    const memberSection = section('成员')
    expect(columnVisibility(header(memberSection, '账户'))).toBeNull()
    expect(columnVisibility(header(memberSection, '在册'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 600,
    })
    expect(columnVisibility(header(memberSection, '来源'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(memberSection, 'SSO 豁免'))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(memberSection, '操作'))).toBeNull()
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
      screen.getByRole('button', { name: '刷新' }),
      screen.getByRole('button', { name: '撤销' }),
      screen.getByRole('button', { name: '豁免 SSO' }),
      screen.getByRole('button', { name: '移除' }),
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
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
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
    const expires = screen.getByLabelText('过期时间')
    expect(expires).toHaveAttribute('type', 'datetime-local')
    fireEvent.change(expires, { target: { value: '2026-11-01T12:00' } })
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
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
      <ConsoleInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </ConsoleInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    // The one-time token is turned into the host's acceptance link, not dropped.
    expect(
      await screen.findByText('把这条接受链接交给「new@example.com」：'),
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
      <ConsoleInvitationLinkProvider
        invitationLink={(token) => `/invitations/${token}`}
      >
        <InvitationsPage />
      </ConsoleInvitationLinkProvider>,
      { operations },
    )
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    const link = `${window.location.origin}/invitations/spd_inv_test_token`
    await screen.findByText(link)
    await user.click(screen.getByRole('button', { name: '复制接受链接' }))
    await vi.waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(link)
    })
  })

  it('shows the bare token when the host supplies no link builder', async () => {    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    // With no builder the token is still surfaced, never silently dropped.
    expect(
      await screen.findByText('「ada@example.com」的接受令牌（链接由宿主构造）：'),
    ).toBeInTheDocument()
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
  })

  it('shows the acceptance value in a named, closable live region', async () => {
    const user = userEvent.setup()
    const operations = withTenant()
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))

    const display = await screen.findByText(
      '「ada@example.com」的接受令牌（链接由宿主构造）：',
    )
    expect(display.closest('[role="status"]')).not.toBeNull()
    // The close control names the tenant and the invitee it belongs to.
    const close = screen.getByRole('button', {
      name: '关闭租户「acme」发给「ada@example.com」的接受链接',
    })
    await user.click(close)
    expect(screen.queryByText('spd_inv_test_token')).toBeNull()
    // The scope's success confirmation stands beside the display and is not
    // the display: closing the display leaves it in place.
    expect(screen.getByText('已向「ada@example.com」发出邀请')).toBeInTheDocument()
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
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    await screen.findByText('spd_inv_test_token')

    // A second attempt resets the creating scope and settles as a failure.
    // The display is page state, not slot state: it survives both.
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
    )
    expect(screen.getByText('spd_inv_test_token')).toBeInTheDocument()
    expect(screen.queryByText('已向「ada@example.com」发出邀请')).toBeNull()
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
    await user.click(screen.getByRole('button', { name: '撤销' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(revoked).toEqual(['inv-1'])
    })
  })

  it('removes a membership through the bundle', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
      removeMember: (input) => {
        removed.push(input.account)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(removed).toEqual(['acc-1'])
    })
  })

  it('names the removed member by the resolved display name, falling back to the recorded id', async () => {
    const user = userEvent.setup()
    const resolving = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
    })
    const first = renderWithProviders(<InvitationsPage />, {
      operations: resolving,
    })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: '移除' }))
    let dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(
      await screen.findByText('已移除成员「Member One」'),
    ).toBeInTheDocument()
    first.unmount()

    // A refused account read leaves the recorded id as the only name the
    // page has, so the confirmation names that instead.
    const refused = withTenant({
      listMembers: () => Promise.resolve([member()]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<InvitationsPage />, { operations: refused })
    await pickTenant(user)
    await screen.findByLabelText(/身份未解析/)
    await user.click(screen.getByRole('button', { name: '移除' }))
    dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(
      await screen.findByText('已移除成员「acc-1」'),
    ).toBeInTheDocument()
  })

  it('writes the chosen account id, not the typed name', async () => {
    const user = userEvent.setup()
    const written: ConsoleWriteMemberInput[] = []
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
      screen.getByRole('button', { name: '写入成员关系' }),
    ).toBeDisabled()
    // A display name finds the account; the write carries its id ("acc-2"),
    // never the typed name.
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '写入成员关系' }))
    await vi.waitFor(() => {
      expect(written).toEqual([
        { tenant: 'acme', account: 'acc-2', active: true, sso_exempt: false },
      ])
    })
    // The landed write clears both the selection and the box that named it.
    expect(screen.getByLabelText('账户')).toHaveValue('')
  })

  it('re-reads the member list after a landed write and leaves it alone after a refusal', async () => {
    const user = userEvent.setup()
    let memberRows = 0
    let writeFails = false
    const calls = bindConsoleRequest((call) => {
      if (call.method === 'GET' && call.path === '/tenant/admin/tenants') {
        return { tenants: [TENANT] }
      }
      if (
        call.method === 'GET' &&
        call.path === '/auth/admin/tenants/acme/invitations'
      ) {
        return { invitations: [], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === '/auth/admin/tenants/acme/members'
      ) {
        memberRows += 1
        return {
          members: memberRows > 1 ? [member({ account: 'acc-2' })] : [],
          next_cursor: '',
        }
      }
      if (call.method === 'GET' && call.path === '/auth/admin/accounts') {
        return { accounts: [account()], next_cursor: '' }
      }
      if (
        call.method === 'GET' &&
        call.path === '/auth/admin/accounts/acc-2'
      ) {
        return { account: account() }
      }
      if (
        call.method === 'PUT' &&
        call.path === '/auth/admin/tenants/acme/members/acc-2'
      ) {
        if (writeFails) {
          throw { status: 403, code: 'client.http.403' }
        }
        return undefined
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(<InvitationsPage />, { operations: consoleOperations })
    await pickTenant(user)
    expect(await screen.findByText('暂无成员')).toBeInTheDocument()
    const listReads = (): number =>
      calls.filter(
        (entry) =>
          entry.method === 'GET' &&
          entry.path === '/auth/admin/tenants/acme/members',
      ).length

    // A landed write re-runs the member read, so the row it created is on
    // screen without the operator pressing refresh.
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '写入成员关系' }))
    expect(
      await screen.findByText('账户「Ada」的成员关系已保存'),
    ).toBeInTheDocument()
    expect(await screen.findByText('Ada')).toBeInTheDocument()
    expect(listReads()).toBe(2)

    // A refused write leaves the list as it was: no read follows it.
    writeFails = true
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '写入成员关系' }))
    expect(await within(section('成员')).findByRole('alert')).toBeInTheDocument()
    expect(listReads()).toBe(2)
  })

  it('toggles a member SSO exemption through the bundle', async () => {
    const user = userEvent.setup()
    const toggled: ConsoleSetMemberSSOExemptInput[] = []
    const operations = withTenant({
      listMembers: () => Promise.resolve([member({ sso_exempt: false })]),
      getAccount: () =>
        Promise.resolve(accountFixture({ display_name: 'Member One' })),
      setMemberSsoExempt: (input) => {
        toggled.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Member One')
    await user.click(screen.getByRole('button', { name: '豁免 SSO' }))
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
    expect(await screen.findByText('邀请加载失败')).toBeInTheDocument()
    expect(screen.getByText('邀请面未能完成这次请求。')).toBeInTheDocument()
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
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    expect(await within(section('邀请')).findByRole('alert')).toHaveTextContent(
      '该改动与账户的当前状态冲突。',
    )

    // The member section refuses the removal.
    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    // The modal hides the page from assistive technology until its exit
    // transition ends; the region is readable after that.
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await within(section('成员')).findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
    )

    // Neither section carries the other's text, and no page-level banner
    // aggregates them.
    expect(
      within(section('邀请')).queryByText('你没有执行此操作的权限。'),
    ).toBeNull()
    expect(
      within(section('成员')).queryByText('该改动与账户的当前状态冲突。'),
    ).toBeNull()
    expect(screen.getAllByRole('alert')).toHaveLength(2)
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
    const revoke = await screen.findByRole('button', { name: '撤销' })
    await user.click(revoke)
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))

    // The failing row carries the marker and the resolved reason; the
    // region carries the same text.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
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

  it('names the changed object on each landed write', async () => {
    const user = userEvent.setup()
    const operations = withTenant({
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)

    // Issue names the invitee.
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))
    expect(
      await screen.findByText('已向「ada@example.com」发出邀请'),
    ).toBeInTheDocument()

    // The membership write names the chosen account.
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '写入成员关系' }))
    expect(
      await screen.findByText('账户「Ada」的成员关系已保存'),
    ).toBeInTheDocument()

    // The SSO exemption names the member by its readable identity.
    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: '豁免 SSO' }))
    expect(
      await screen.findByText('已为成员「Operator」设置 SSO 豁免'),
    ).toBeInTheDocument()

    // The removal names the removed member by the same identity.
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    expect(
      await screen.findByText('已移除成员「Operator」'),
    ).toBeInTheDocument()
  })

  it('disables the tenant selector while a write is in flight and clears both scopes when the tenant changes', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withTenant({
      listTenants: () =>
        Promise.resolve([
          TENANT,
          { id: 'globex', alias: 'Globex', status: 'active' as const },
        ]),
      listInvitations: () =>
        Promise.resolve({ invitations: [invitation()], next_cursor: '' }),
      listMembers: () => Promise.resolve([member()]),
      issueInvitation: () =>
        pending.promise.then(() => issuedInvitationFixture()),
    })
    renderWithProviders(<InvitationsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText('邮箱地址'), 'new@example.com')
    await user.click(screen.getByRole('button', { name: '发出邀请' }))

    // The tenant feeds every scope on the page: it is out of reach while
    // the issue is in flight.
    expect(screen.getByLabelText('租户')).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText('已向「ada@example.com」发出邀请'),
    ).toBeInTheDocument()

    // A member write lands in the other scope, so both carry a result.
    await screen.findAllByText('Operator')
    await user.click(screen.getByRole('button', { name: '豁免 SSO' }))
    expect(
      await screen.findByText('已为成员「Operator」设置 SSO 豁免'),
    ).toBeInTheDocument()

    // Another tenant makes both results describe a tenant left behind.
    await user.click(screen.getByLabelText('租户'))
    await user.click(await screen.findByRole('option', { name: 'Globex' }))
    expect(screen.queryByText('已向「ada@example.com」发出邀请')).toBeNull()
    expect(screen.queryByText('已为成员「Operator」设置 SSO 豁免')).toBeNull()
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
