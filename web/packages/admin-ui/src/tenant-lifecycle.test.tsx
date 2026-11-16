/**
 * The organization lifecycle controls on the platform roster and the
 * tenant detail: transfer owner and delete organization.
 *
 * The entries are visible only to a caller whose platform permission set
 * holds `tenant.manage`, never on a personal organization, and never on a
 * tombstone. The transfer dialog has two target paths: the organization's
 * member selector when the caller's tenant permission set holds
 * `authn.membership.manage`, and the platform account selector otherwise.
 * The account path never pretends the chosen account is a member: the
 * endpoint still refuses a non-member with 409, and the dialog states that
 * refusal inline. Deletion asks for the organization's name and the
 * shared two-step danger confirmation, and renders the tombstone refusal as
 * the delete's own copy.
 */

import { afterEach, describe, expect, it } from 'vitest'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { attachSession } from '@speed/auth-core'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { TenantRosterPage } from './TenantRosterPage.js'
import { TenantDetailPage } from './TenantDetailPage.js'
import type { AdminMember, AdminTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
  transferFixture,
} from '../test-utils/operations.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The zh-CN label the shared confirm shows once its two-step guard arms. */
const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

/** Fills a locale template's `{{name}}` placeholders. */
function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

/** Attaches the session the pages read their permission affordances from. */
function attachPermissions({
  system,
  tenant = null,
}: {
  readonly system: readonly string[] | null
  readonly tenant?: readonly string[] | null
}): void {
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      permissionSets: { system, tenant },
    }),
  )
}

/** One active member and one suspended member of `acme`. */
const MEMBERS: readonly AdminMember[] = [
  {
    tenant: 'acme',
    account: 'acc-2',
    display_name: 'Ada Lovelace',
    username: 'ada',
    active: true,
    joined_at: '2026-01-01T00:00:00Z',
    roles: [],
    source: 'admin',
    sso_exempt: false,
  },
  {
    tenant: 'acme',
    account: 'acc-3',
    display_name: 'Grace Hopper',
    username: 'grace',
    active: false,
    joined_at: '2026-01-01T00:00:00Z',
    roles: [],
    source: 'admin',
    sso_exempt: false,
  },
]

/** The two-step danger confirmation: arm, wait out the lockout, confirm. */
async function confirmTwice(
  user: ReturnType<typeof userEvent.setup>,
  confirmLabel: string,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.click(within(dialog).getByRole('button', { name: confirmLabel }))
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
}

/**
 * The delete's confirmation: type the organization's name, then run the
 * shared two-step guard. The confirming button is inert until the name
 * matches exactly.
 */
async function confirmDelete(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.type(
    within(dialog).getByLabelText(
      zhCN.tenantLifecycle.deleteConfirm.nameLabel,
    ),
    name,
  )
  await confirmTwice(user, zhCN.tenantLifecycle.deleteConfirm.confirmLabel)
}

afterEach(() => {
  attachSession(createStepUpSession(AUTHENTICATED_SNAPSHOT))
})

describe('platform tenant lifecycle entries', () => {
  const acme = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

  it('shows the transfer and delete entries only with tenant.manage', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve([acme]) }),
    )
    attachPermissions({ system: [] })
    const first = renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    ).toBeNull()
    first.unmount()

    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    expect(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    ).toBeInTheDocument()
  })

  it('shows no entry on a personal organization or a tombstone', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'personal-alice', display_name: 'Alice', kind: 'personal', status: 'active' }),
      tenantFixture({ name: 'gone', display_name: 'Gone Co', status: 'deleted' }),
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Alice')

    for (const id of ['personal-alice', 'gone']) {
      expect(
        screen.queryByRole('button', {
          name: text(zhCN.tenantLifecycle.transferAria, { id }),
        }),
      ).toBeNull()
      expect(
        screen.queryByRole('button', {
          name: text(zhCN.tenantLifecycle.deleteAria, { id }),
        }),
      ).toBeNull()
    }
    // The business organization beside them still carries both.
    expect(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    ).toBeInTheDocument()
  })

  it('transfers through the member selector when membership.manage is held', async () => {
    const user = userEvent.setup()
    const writes: Array<{ name: string; account: string }> = []
    let reads = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => {
          reads += 1
          return Promise.resolve([acme])
        },
        listMembers: () =>
          Promise.resolve({ members: [...MEMBERS], next_cursor: '' }),
        requestTenantOwner: (input) => {
          writes.push(input)
          return Promise.resolve(transferFixture())
        },
      }),
    )
    attachPermissions({
      system: ['tenant.manage'],
      tenant: ['authn.membership.manage'],
    })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByRole('heading', {
        name: text(zhCN.tenantLifecycle.transfer.title, { name: 'Acme' }),
      }),
    ).toBeInTheDocument()
    // The dialog names itself by its title and describes the target paths
    // by its lead paragraph.
    expect(dialog).toHaveAccessibleName(
      text(zhCN.tenantLifecycle.transfer.title, { name: 'Acme' }),
    )
    expect(dialog).toHaveAccessibleDescription(
      zhCN.tenantLifecycle.transfer.description,
    )

    // Only the active member is offered; the suspended membership is not.
    const memberInput = within(dialog).getByRole('combobox', {
      name: zhCN.tenantLifecycle.transfer.memberLabel,
    })
    await user.click(memberInput)
    expect(
      await screen.findByRole('option', { name: 'Ada Lovelace (ada)' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: /Grace Hopper/ }),
    ).toBeNull()
    await chooseAccount(
      user,
      zhCN.tenantLifecycle.transfer.memberLabel,
      'Ada',
      'Ada Lovelace (ada)',
    )

    await expectNoAxeViolations()

    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.submit,
      }),
    )
    await confirmTwice(user, zhCN.tenantLifecycle.transfer.confirmLabel)

    await waitFor(() => {
      expect(writes).toEqual([{ name: 'acme', account: 'acc-2' }])
    })
    expect(
      await screen.findByText(
        text(zhCN.tenantLifecycle.transfer.success, {
          name: 'acme',
          target: 'Ada Lovelace (ada)',
        }),
      ),
    ).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    // A request changes no owner column, so the roster is not re-read.
    expect(reads).toBe(1)
  })

  it('shows the pending request with its target and lets platform staff cancel it', async () => {
    const user = userEvent.setup()
    let pending: ReturnType<typeof transferFixture> | null = transferFixture({
      target_display_name: 'Ada Lovelace',
      target_username: 'ada',
    })
    const cancelled: Array<{ name: string; request: string }> = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve([acme]),
        getPendingTenantOwner: () => Promise.resolve(pending),
        cancelTenantOwner: (input) => {
          cancelled.push(input)
          pending = null
          return Promise.resolve()
        },
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    expect(
      await screen.findByText(zhCN.tenantLifecycle.transfer.pendingTitle),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        text(zhCN.tenantLifecycle.transfer.pendingTarget, {
          target: 'Ada Lovelace (ada)',
        }),
      ),
    ).toBeInTheDocument()
    // The request control is held while one is pending.
    expect(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    ).toBeDisabled()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.pendingCancel,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.tenantLifecycle.transfer.pendingCancelTitle,
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.pendingCancel,
      }),
    )
    await waitFor(() =>
      expect(cancelled).toEqual([{ name: 'acme', request: 'transfer-1' }]),
    )
    expect(
      await screen.findByText(zhCN.tenantLifecycle.transfer.pendingCancelled),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.tenantLifecycle.transfer.pendingTitle),
      ).toBeNull(),
    )
  })

  it('uses the account selector without the member read, and explains the 409 inline', async () => {
    const user = userEvent.setup()
    const writes: Array<{ name: string; account: string }> = []
    let memberReads = 0
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve([acme]),
        listMembers: () => {
          memberReads += 1
          return Promise.resolve({ members: [...MEMBERS], next_cursor: '' })
        },
        listAccounts: () =>
          Promise.resolve({
            accounts: [accountFixture({ id: 'acc-9', display_name: 'Bob' })],
            next_cursor: '',
          }),
        requestTenantOwner: (input) => {
          writes.push(input)
          return Promise.reject({ status: 409, code: 'tenant.conflict' })
        },
      }),
    )
    // tenant.manage without the member read: the account path.
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    expect(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantLifecycle.transfer.accountLabel,
      }),
    ).toBeInTheDocument()
    expect(
      within(dialog).getByText(zhCN.tenantLifecycle.transfer.accountHint),
    ).toBeInTheDocument()

    await chooseAccount(
      user,
      zhCN.tenantLifecycle.transfer.accountLabel,
      'Bob',
      'Bob',
    )
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.submit,
      }),
    )
    await confirmTwice(user, zhCN.tenantLifecycle.transfer.confirmLabel)

    // The request carried the account; the server's non-member 409 stands
    // inline in the still-open dialog.
    await waitFor(() => {
      expect(writes).toEqual([{ name: 'acme', account: 'acc-9' }])
    })
    const failure = await screen.findByRole('alert')
    expect(failure).toHaveTextContent(
      zhCN.tenantLifecycle.transfer.failedConflict,
    )
    expect(
      screen.getByRole('heading', {
        name: text(zhCN.tenantLifecycle.transfer.title, { name: 'Acme' }),
      }),
    ).toBeInTheDocument()
    // The account path never issued the member read.
    expect(memberReads).toBe(0)
  })

  it('states a transfer 404 as the organization no longer existing', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve([acme]),
        listAccounts: () =>
          Promise.resolve({
            accounts: [accountFixture({ id: 'acc-9', display_name: 'Bob' })],
            next_cursor: '',
          }),
        requestTenantOwner: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await chooseAccount(
      user,
      zhCN.tenantLifecycle.transfer.accountLabel,
      'Bob',
      'Bob',
    )
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.submit,
      }),
    )
    await confirmTwice(user, zhCN.tenantLifecycle.transfer.confirmLabel)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantLifecycle.transfer.failedNotFound,
    )
  })

  it('resolves a bare transfer 403 to the permission refusal', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve([acme]),
        listAccounts: () =>
          Promise.resolve({
            accounts: [accountFixture({ id: 'acc-9', display_name: 'Bob' })],
            next_cursor: '',
          }),
        requestTenantOwner: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await chooseAccount(
      user,
      zhCN.tenantLifecycle.transfer.accountLabel,
      'Bob',
      'Bob',
    )
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.submit,
      }),
    )
    await confirmTwice(user, zhCN.tenantLifecycle.transfer.confirmLabel)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.client.httpForbidden,
    )
  })
})

describe('platform tenant deletion', () => {
  it('deletes through the two-step confirmation and refreshes the roster', async () => {
    const user = userEvent.setup()
    let tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
    ]
    let reads = 0
    const deletes: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => {
          reads += 1
          return Promise.resolve(tenants)
        },
        deleteTenant: (input) => {
          deletes.push(input.name)
          tenants = []
          return Promise.resolve()
        },
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    // The impact copy is part of the confirmation.
    expect(
      within(dialog).getByText(zhCN.tenantLifecycle.deleteConfirm.message),
    ).toBeInTheDocument()
    await expectNoAxeViolations()

    await confirmDelete(user, 'acme')

    await waitFor(() => expect(deletes).toEqual(['acme']))
    expect(
      await screen.findByText(
        text(zhCN.tenantLifecycle.deleteConfirm.success, { name: 'acme' }),
      ),
    ).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Acme')).toBeNull())
    await waitFor(() => expect(reads).toBeGreaterThan(1))
  })

  it('keeps the delete confirmation disabled until the exact name is typed', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ]),
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    const field = within(dialog).getByLabelText(
      zhCN.tenantLifecycle.deleteConfirm.nameLabel,
    )
    const confirm = within(dialog).getByRole('button', {
      name: zhCN.tenantLifecycle.deleteConfirm.confirmLabel,
    })

    // The empty field and a near miss both keep the guard closed; only the
    // exact name opens it.
    expect(field).toHaveValue('')
    expect(confirm).toBeDisabled()
    await user.type(field, 'acm')
    expect(confirm).toBeDisabled()
    await user.clear(field)
    await user.type(field, 'acme')
    expect(confirm).toBeEnabled()
  })

  it('renders the tombstone refusal as the delete copy, not the registration conflict', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ]),
        deleteTenant: () =>
          Promise.reject({ status: 409, code: 'tenant.conflict' }),
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    await confirmDelete(user, 'acme')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.tenantLifecycle.deleteConfirm.failedConflict,
    )
    // The registration-oriented conflict leaf would misdescribe the
    // tombstone, so the delete's own copy stands instead.
    expect(alert).not.toHaveTextContent(zhCN.errors.tenant.conflict)
    expect(screen.getByText('Acme')).toBeInTheDocument()
  })

  it('states a delete 404 as the organization no longer existing', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ]),
        deleteTenant: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    await confirmDelete(user, 'acme')

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantLifecycle.deleteConfirm.failedNotFound,
    )
    expect(screen.getByText('Acme')).toBeInTheDocument()
  })

  it('renders a protected-tenant delete refusal as the protected copy', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ]),
        deleteTenant: () =>
          Promise.reject({ status: 403, code: 'tenant.protected' }),
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    await confirmDelete(user, 'acme')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(zhCN.errors.tenant.protected)
    expect(screen.getByText('Acme')).toBeInTheDocument()
  })
})

describe('tenant detail lifecycle', () => {
  it('renders the lifecycle section only with tenant.manage and keeps personal organizations out', async () => {
    const backend = makeAdminBackend({
      getTenant: (query) =>
        Promise.resolve(
          tenantFixture({
            name: query.name ?? 'acme',
            display_name: query.name === 'personal-alice' ? 'Alice' : 'Acme',
            kind: query.name === 'personal-alice' ? 'personal' : 'business',
          }),
        ),
    })
    const operations = createAdminOperations(backend)

    attachPermissions({ system: ['tenant.manage'] })
    const first = renderWithProviders(
      <TenantDetailPage name="acme" />,
      { operations },
    )
    await screen.findByText(zhCN.tenantLifecycle.sectionTitle)
    expect(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    ).toBeInTheDocument()
    first.unmount()

    attachPermissions({ system: ['tenant.manage'] })
    const personal = renderWithProviders(
      <TenantDetailPage name="personal-alice" />,
      { operations },
    )
    await screen.findAllByText('Alice')
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, {
          name: 'personal-alice',
        }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, {
          name: 'personal-alice',
        }),
      }),
    ).toBeNull()
    // The group renders no entry on a personal organization, so the page
    // renders no card for it either -- not just no buttons inside one.
    expect(
      screen.queryByText(zhCN.tenantLifecycle.sectionTitle),
    ).toBeNull()
    expect(
      screen.queryByText(zhCN.tenantLifecycle.sectionDescription),
    ).toBeNull()
    personal.unmount()

    attachPermissions({ system: [] })
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')
    expect(
      screen.queryByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    ).toBeNull()
    // Without tenant.manage the group has nothing to show, and the card is
    // absent rather than empty.
    expect(
      screen.queryByText(zhCN.tenantLifecycle.sectionTitle),
    ).toBeNull()
    expect(
      screen.queryByText(zhCN.tenantLifecycle.sectionDescription),
    ).toBeNull()
  })

  it('shows the pending request on the detail page without re-reading the tenant', async () => {
    const user = userEvent.setup()
    let reads = 0
    const writes: Array<{ name: string; account: string }> = []
    let pending: ReturnType<typeof transferFixture> | null = null
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => {
          reads += 1
          return Promise.resolve(
            tenantFixture({ name: 'acme', display_name: 'Acme' }),
          )
        },
        getPendingTenantOwner: () => Promise.resolve(pending),
        listMembers: () =>
          Promise.resolve({ members: [...MEMBERS], next_cursor: '' }),
        requestTenantOwner: (input) => {
          writes.push(input)
          pending = transferFixture({
            target_display_name: 'Ada Lovelace',
            target_username: 'ada',
          })
          return Promise.resolve(pending)
        },
      }),
    )
    attachPermissions({
      system: ['tenant.manage'],
      tenant: ['authn.membership.manage'],
    })
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.transferAria, { name: 'acme' }),
      }),
    )
    const dialog = await screen.findByRole('dialog')
    await chooseAccount(
      user,
      zhCN.tenantLifecycle.transfer.memberLabel,
      'Ada',
      'Ada Lovelace (ada)',
    )
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantLifecycle.transfer.submit,
      }),
    )
    await confirmTwice(user, zhCN.tenantLifecycle.transfer.confirmLabel)

    await waitFor(() => {
      expect(writes).toEqual([{ name: 'acme', account: 'acc-2' }])
    })
    // The request is recorded; the tenant's owner column is not, so the
    // detail read stays settled and only the pending read converges.
    expect(
      await screen.findByText(zhCN.tenantLifecycle.transfer.pendingTitle),
    ).toBeInTheDocument()
    expect(reads).toBe(1)
  })

  it('keeps the delete confirmation after the detail settles into a tombstone', async () => {
    const user = userEvent.setup()
    let status: AdminTenant['status'] = 'active'
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(tenantFixture({ name: 'acme', display_name: 'Acme', status })),
        deleteTenant: () => {
          status = 'deleted'
          return Promise.resolve()
        },
      }),
    )
    attachPermissions({ system: ['tenant.manage'] })
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    await confirmDelete(user, 'acme')

    const confirmation = text(zhCN.tenantLifecycle.deleteConfirm.success, {
      name: 'acme',
    })
    expect(await screen.findByText(confirmation)).toBeInTheDocument()

    // The delete invalidates the detail read, which settles as the
    // tombstone; the page withdraws the lifecycle card with it. The
    // confirmation lives at page level, so it survives that removal instead
    // of flashing and vanishing with the card.
    expect(
      await screen.findByText(zhCN.tenants.status.deleted),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.tenantLifecycle.sectionTitle),
    ).toBeNull()
    expect(screen.getByText(confirmation)).toBeInTheDocument()
  })
})
