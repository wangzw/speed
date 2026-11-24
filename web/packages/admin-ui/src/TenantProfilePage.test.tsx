import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { attachSession } from '@speed/auth-core'
import { formatInstant } from '@speed/i18n'
import { uiKitResources } from '@speed/ui-kit'
import { TenantProfilePage } from './TenantProfilePage.js'
import {
  OrganizationAdminOperationsProvider,
  type AdminTenantProfile,
} from './organization-operations.js'
import type { AdminMember } from './operations.js'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  profileFixture,
  type AdminBackend,
} from '../test-utils/operations.js'
import {
  ANONYMOUS_SNAPSHOT,
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'
const OWNER = 'acc-1'
const OTHER_ACCOUNT = 'acc-2'
const OWNER_DISPLAY_NAME = 'Ada Lovelace'
const OWNER_USERNAME = 'ada'
// An owner account identifier the page must never render.
const UNSHOWN_OWNER = '8f14e45f-ceea-467a-9d3d-0c1b7e2f4a51'

const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
  readonly reject: (reason: unknown) => void
} {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

const ADA: AdminMember = {
  account: OWNER,
  tenant: TENANT,
  active: true,
  display_name: 'Ada Lovelace',
  username: 'ada',
  source: 'admin',
  sso_exempt: false,
  roles: ['tenant-admin'],
  joined_at: '2026-10-15T12:00:00Z',
}

const BOB: AdminMember = {
  account: OTHER_ACCOUNT,
  tenant: TENANT,
  active: true,
  display_name: 'Bob Stone',
  username: 'bob',
  source: 'invite',
  sso_exempt: false,
  roles: [],
  joined_at: '2026-10-20T08:30:00Z',
}

const SUSPENDED: AdminMember = {
  ...BOB,
  account: 'acc-3',
  display_name: 'Carol Suspended',
  username: 'carol',
  active: false,
}

function attachAccount(accountId: string): void {
  attachSession(
    createStepUpSession({
      ...AUTHENTICATED_SNAPSHOT,
      principal: {
        ...AUTHENTICATED_SNAPSHOT.principal!,
        account_id: accountId,
      },
    }),
  )
}

afterEach(() => {
  attachSession(createStepUpSession(ANONYMOUS_SNAPSHOT))
})

function renderPage({
  tenant = TENANT,
  profile = profileFixture(),
  getTenantProfile,
  updateTenantProfile,
  onTenantUpdated,
  onOrganizationLeft,
  backend = {},
}: {
  readonly tenant?: string
  readonly profile?: AdminTenantProfile
  readonly getTenantProfile?: (
    tenant: string,
  ) => Promise<AdminTenantProfile>
  readonly updateTenantProfile?: (
    input: Parameters<
      ReturnType<typeof makeAdminBackend>['updateTenantProfile']
    >[0],
  ) => Promise<void>
  readonly onTenantUpdated?: () => void | Promise<void>
  readonly onOrganizationLeft?: (message: string) => void | Promise<void>
  readonly backend?: Partial<AdminBackend>
} = {}) {
  const operations = createAdminOperations(
    makeAdminBackend({
      getTenantProfile: () =>
        getTenantProfile === undefined
          ? Promise.resolve(profile)
          : getTenantProfile(tenant),
      ...(updateTenantProfile === undefined ? {} : { updateTenantProfile }),
      ...backend,
    }),
  )
  return renderWithProviders(
    <OrganizationAdminOperationsProvider operations={operations}>
      <TenantProfilePage
        tenant={tenant}
        onTenantUpdated={onTenantUpdated}
        onOrganizationLeft={onOrganizationLeft}
      />
    </OrganizationAdminOperationsProvider>,
    { operations },
  )
}

async function ready(): Promise<void> {
  await screen.findByRole('heading', {
    level: 1,
    name: zhCN.organization.nav.profile,
  })
  await screen.findByText(zhCN.tenantProfile.identity.title)
}

describe('TenantProfilePage', () => {
  it('renders the identity read-only with no name input', async () => {
    renderPage({
      profile: profileFixture({
        name: 'acme-inc',
        display_name: 'Acme Incorporated',
        status: 'active',
      }),
    })
    await ready()

    expect(screen.getByText('acme-inc')).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.status.active)).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.tenantProfile.field.statusAria
          .replace('{{label}}', zhCN.tenantProfile.field.status)
          .replace('{{value}}', '')
          .trimEnd(),
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.tenantProfile.field.name),
    ).toBeNull()
    expect(
      screen.getByText(zhCN.tenantProfile.identity.nameHint),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.tenantProfile.field.displayName),
    ).toHaveValue('Acme Incorporated')
  })

  it('falls back to the name when the profile names no display name', async () => {
    renderPage({
      profile: profileFixture({ name: 'acme-inc', display_name: '' }),
    })
    await ready()
    const displayName = screen.getByLabelText(
      zhCN.tenantProfile.field.displayName,
    )
    expect(displayName).toHaveValue('')
    expect(displayName).toHaveAttribute('placeholder', 'acme-inc')
  })

  it('saves the four fields and reloads the host tenant list', async () => {
    const user = userEvent.setup()
    const updates: unknown[] = []
    const onTenantUpdated = vi.fn()
    renderPage({
      profile: profileFixture({
        name: 'acme-inc',
        display_name: 'Old name',
        description: 'Old description',
        contact_email: 'old@acme.example',
        website: 'https://old.acme.example',
      }),
      updateTenantProfile: (input) => {
        updates.push(input)
        return Promise.resolve()
      },
      onTenantUpdated,
    })
    await ready()

    const displayName = screen.getByLabelText(
      zhCN.tenantProfile.field.displayName,
    )
    await user.clear(displayName)
    await user.type(displayName, 'New name')
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.form.save }),
    )

    await waitFor(() => expect(updates).toHaveLength(1))
    expect(updates[0]).toEqual({
      tenant: TENANT,
      fields: {
        displayName: 'New name',
        description: 'Old description',
        contactEmail: 'old@acme.example',
        website: 'https://old.acme.example',
      },
    })
    expect(onTenantUpdated).toHaveBeenCalledTimes(1)
    expect(
      await screen.findByText(
        zhCN.tenantProfile.form.savedNamed.replace('{{name}}', 'New name'),
      ),
    ).toBeInTheDocument()
  })

  it('allows the display name to be cleared and shows the name in the confirmation', async () => {
    const user = userEvent.setup()
    const updates: unknown[] = []
    renderPage({
      profile: profileFixture({ name: 'acme-inc', display_name: 'Acme' }),
      updateTenantProfile: (input) => {
        updates.push(input)
        return Promise.resolve()
      },
    })
    await ready()

    await user.clear(
      screen.getByLabelText(zhCN.tenantProfile.field.displayName),
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.form.save }),
    )

    await waitFor(() => expect(updates).toHaveLength(1))
    expect((updates[0] as { fields: { displayName: string } }).fields)
      .toMatchObject({ displayName: '' })
    expect(
      await screen.findByText(
        zhCN.tenantProfile.form.savedNamed.replace('{{name}}', 'acme-inc'),
      ),
    ).toBeInTheDocument()
  })

  it('keeps the draft when the save is refused', async () => {
    const user = userEvent.setup()
    const onTenantUpdated = vi.fn()
    renderPage({
      updateTenantProfile: () => Promise.reject({ code: 'tenant.conflict' }),
      onTenantUpdated,
    })
    await ready()

    const displayName = screen.getByLabelText(
      zhCN.tenantProfile.field.displayName,
    )
    await user.type(displayName, 'Unsaved name')
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.form.save }),
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.tenant.conflict,
    )
    expect(displayName).toHaveValue('Unsaved name')
    expect(onTenantUpdated).not.toHaveBeenCalled()
  })

  it('keeps the save control focused and inert while the profile save is in flight', async () => {
    const user = userEvent.setup()
    const held = deferred<void>()
    const updates: unknown[] = []
    renderPage({
      updateTenantProfile: (input) => {
        updates.push(input)
        return held.promise
      },
    })
    await ready()

    const save = screen.getByRole('button', {
      name: zhCN.tenantProfile.form.save,
    })
    await user.click(save)
    await waitFor(() => expect(updates).toHaveLength(1))

    expect(save).not.toHaveAttribute('disabled')
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).toHaveAttribute('aria-busy', 'true')
    expect(save).toHaveFocus()

    await user.click(save)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(updates).toHaveLength(1)
    expect(save).toHaveFocus()

    await act(async () => {
      held.resolve()
    })
  })

  it('refuses an invalid contact email before sending anything', async () => {
    const user = userEvent.setup()
    const updates = vi.fn()
    renderPage({ updateTenantProfile: updates })
    await ready()

    await user.type(
      screen.getByLabelText(zhCN.tenantProfile.field.contactEmail),
      'not-an-email',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.form.save }),
    )

    expect(
      await screen.findByText(
        zhCN.tenantProfile.form.invalid.contactEmail,
      ),
    ).toBeInTheDocument()
    expect(updates).not.toHaveBeenCalled()
  })

  it('renders the loading announcement while the read is pending', async () => {
    let resolveProfile!: (value: AdminTenantProfile) => void
    renderPage({
      getTenantProfile: () =>
        new Promise<AdminTenantProfile>((resolve) => {
          resolveProfile = resolve
        }),
    })
    expect(
      screen.getByRole('status', { name: zhCN.tenantProfile.loading }),
    ).toBeInTheDocument()

    resolveProfile(profileFixture())
    await ready()
  })

  it('renders the permission placeholder with no retry on a 403', async () => {
    renderPage({
      getTenantProfile: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.tenantProfile.retry }),
    ).toBeNull()
  })

  it('renders the not-found placeholder with a retry on a 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    renderPage({
      getTenantProfile: () => {
        calls += 1
        return calls === 1
          ? Promise.reject({ status: 404, code: 'client.http.404' })
          : Promise.resolve(profileFixture())
      },
    })
    expect(
      await screen.findByText(zhCN.detail.notFound.title),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.retry }),
    )
    await ready()
    expect(calls).toBe(2)
  })

  it('has no axe violations', async () => {
    renderPage()
    await ready()
    await expectNoAxeViolations()
  })
})

const ORG_NAME = 'Acme Incorporated'
const ORG_URL_NAME = 'acme-inc'

function ownerProfile(
  overrides: Partial<AdminTenantProfile> = {},
): AdminTenantProfile {
  return profileFixture({
    name: ORG_URL_NAME,
    display_name: ORG_NAME,
    owner_account: OWNER,
    owner_display_name: OWNER_DISPLAY_NAME,
    owner_username: OWNER_USERNAME,
    ...overrides,
  })
}

function ownerRow(container: HTMLElement): HTMLElement {
  const label = within(container).getByText(zhCN.tenantProfile.field.owner)
  const row = label.closest('div')
  if (row === null) {
    throw new Error('the owner row was not found')
  }
  return row
}

function expectNoOwnerAccount(container: HTMLElement, account: string): void {
  expect(container.innerHTML).not.toContain(account)
  expect(container.textContent).not.toContain(account)
}

function transferTitle(): string {
  return zhCN.tenantProfile.ownership.transfer.title.replace('{{name}}', ORG_NAME)
}

function deleteTitle(): string {
  return zhCN.tenantProfile.ownership.delete.title.replace('{{name}}', ORG_NAME)
}

async function armAndConfirm(
  user: ReturnType<typeof userEvent.setup>,
  dialog: HTMLElement,
  confirmLabel: string,
): Promise<void> {
  await user.click(within(dialog).getByRole('button', { name: confirmLabel }))
  const again = await within(dialog).findByRole('button', {
    name: CONFIRM_AGAIN_LABEL,
  })
  await waitFor(() => expect(again).not.toHaveAttribute('aria-disabled', 'true'))
  await user.click(again)
}

describe('TenantProfilePage owner row', () => {
  it('shows the display name and the username, never the account identifier', async () => {
    const { container } = renderPage({
      profile: profileFixture({
        owner_account: UNSHOWN_OWNER,
        owner_display_name: OWNER_DISPLAY_NAME,
        owner_username: OWNER_USERNAME,
      }),
    })
    await ready()

    expect(
      within(ownerRow(container)).getByText(
        `${OWNER_DISPLAY_NAME} (${OWNER_USERNAME})`,
      ),
    ).toBeInTheDocument()
    expectNoOwnerAccount(container, UNSHOWN_OWNER)
  })

  it('tells two owners sharing one display name apart by their usernames', async () => {
    const first = renderPage({
      profile: profileFixture({
        owner_account: UNSHOWN_OWNER,
        owner_display_name: 'Alex Kim',
        owner_username: 'alex-one',
      }),
    })
    await ready()
    expect(
      within(ownerRow(first.container)).getByText('Alex Kim (alex-one)'),
    ).toBeInTheDocument()
    first.unmount()

    const second = renderPage({
      profile: profileFixture({
        owner_account: UNSHOWN_OWNER,
        owner_display_name: 'Alex Kim',
        owner_username: 'alex-two',
      }),
    })
    await ready()
    expect(
      within(ownerRow(second.container)).getByText('Alex Kim (alex-two)'),
    ).toBeInTheDocument()
  })

  it('shows only the display name when the owner has no username', async () => {
    const { container } = renderPage({
      profile: profileFixture({
        owner_account: UNSHOWN_OWNER,
        owner_display_name: OWNER_DISPLAY_NAME,
        owner_username: '',
      }),
    })
    await ready()

    expect(
      within(ownerRow(container)).getByText(OWNER_DISPLAY_NAME),
    ).toBeInTheDocument()
    expectNoOwnerAccount(container, UNSHOWN_OWNER)
  })

  it('shows only the username when the owner has no display name', async () => {
    const { container } = renderPage({
      profile: profileFixture({
        owner_account: UNSHOWN_OWNER,
        owner_display_name: '',
        owner_username: OWNER_USERNAME,
      }),
    })
    await ready()

    expect(
      within(ownerRow(container)).getByText(OWNER_USERNAME),
    ).toBeInTheDocument()
    expectNoOwnerAccount(container, UNSHOWN_OWNER)
  })

  it('shows the neutral label when the owner cannot be resolved, never the identifier', async () => {
    const { container } = renderPage({
      profile: profileFixture({
        owner_account: UNSHOWN_OWNER,
        owner_display_name: '',
        owner_username: '',
      }),
    })
    await ready()

    expect(
      within(ownerRow(container)).getByText(zhCN.tenantProfile.owner.unnamed),
    ).toBeInTheDocument()
    expectNoOwnerAccount(container, UNSHOWN_OWNER)
  })
})

describe('TenantProfilePage ownership', () => {
  const PENDING = {
    id: 'transfer-1',
    target_display_name: 'Bob Stone',
    target_username: 'bob',
    requested_at: '2026-10-20T08:30:00Z',
    expires_at: '2026-10-27T08:30:00Z',
    status: 'pending' as const,
  }

  it('keeps the transfer control focusable and explains the pending request', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        getPendingOrganizationOwner: () => Promise.resolve(PENDING),
      },
    })
    await ready()

    const transfer = await screen.findByRole('button', {
      name: zhCN.tenantProfile.ownership.transfer.button,
    })
    expect(transfer).toHaveAttribute('aria-disabled', 'true')
    expect(transfer).not.toHaveAttribute('disabled')
    expect(transfer).toHaveAccessibleDescription(
      zhCN.tenantProfile.ownership.pending.description,
    )

    transfer.focus()
    expect(transfer).toHaveFocus()
    await user.click(transfer)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(transfer).toHaveFocus()
  })

  it('keeps focus on the delete control while the delete is in flight', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const removed = deferred<void>()
    renderPage({
      profile: ownerProfile(),
      onOrganizationLeft: vi.fn(),
      backend: { deleteOrganization: () => removed.promise },
    })
    await ready()

    const remove = screen.getByRole('button', {
      name: zhCN.tenantProfile.ownership.delete.button,
    })
    await user.click(remove)
    const dialog = await screen.findByRole('dialog', { name: deleteTitle() })
    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.delete.nameLabel,
      ),
      ORG_URL_NAME,
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.delete.confirmLabel,
    )

    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: deleteTitle() })).toBeNull(),
    )
    expect(
      document.activeElement,
      `focus after confirming the delete; document.activeElement is ${
        document.activeElement?.tagName ?? 'null'
      }`,
    ).toBe(remove)
    expect(remove).toHaveAttribute('aria-busy', 'true')

    await act(async () => {
      removed.resolve()
    })
  })

  it('marks the transfer dialog behind its open target menu inert', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      getTenantProfile: () => Promise.resolve(ownerProfile()),
      backend: {
        getPendingOrganizationOwner: () => Promise.resolve(null),
        listMembers: () =>
          Promise.resolve({
            members: [ADA, BOB, SUSPENDED],
            next_cursor: '',
          }),
      },
    })
    await ready()
    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: transferTitle(),
    })
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    await screen.findByRole('option', { name: 'Bob Stone (bob)' })
    expect(dialog.closest('[inert]')).not.toBeNull()
  })

  it('asks an active member to take ownership and shows the pending request without changing the owner', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const requests: unknown[] = []
    const onOrganizationLeft = vi.fn()
    const removeMember = vi.fn(() => Promise.resolve())
    const unbindTenantRole = vi.fn(() => Promise.resolve())
    let pending: typeof PENDING | null = null
    renderPage({
      getTenantProfile: () => Promise.resolve(ownerProfile()),
      onOrganizationLeft,
      backend: {
        getPendingOrganizationOwner: () => Promise.resolve(pending),
        requestOrganizationOwner: (input) => {
          requests.push(input)
          pending = PENDING
          return Promise.resolve(PENDING)
        },
        listMembers: () =>
          Promise.resolve({
            members: [ADA, BOB, SUSPENDED],
            next_cursor: '',
          }),
        removeMember,
        unbindTenantRole,
      },
    })
    await ready()

    expect(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).not.toHaveAttribute('aria-disabled', 'true')

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    expect(
      screen.queryByRole('option', {
        name: 'Carol Suspended (carol)',
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('option', {
        name: 'Ada Lovelace (ada)',
      }),
    ).toBeNull()
    await user.click(
      await screen.findByRole('option', { name: 'Bob Stone (bob)' }),
    )

    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.transfer.confirmLabel,
    )

    await waitFor(() =>
      expect(requests).toEqual([{ tenant: TENANT, account: OTHER_ACCOUNT }]),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', { name: transferTitle() }),
      ).toBeNull(),
    )
    expect(
      await screen.findByText(
        zhCN.tenantProfile.ownership.transfer.requested.replace(
          '{{account}}',
          'Bob Stone (bob)',
        ),
      ),
    ).toBeInTheDocument()
    expect(onOrganizationLeft).not.toHaveBeenCalled()
    expect(removeMember).not.toHaveBeenCalled()
    expect(unbindTenantRole).not.toHaveBeenCalled()
    expect(
      screen.getByText(`${OWNER_DISPLAY_NAME} (${OWNER_USERNAME})`),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.tenantProfile.ownership.pending.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.tenantProfile.ownership.pending.target.replace(
          '{{target}}',
          'Bob Stone (bob)',
        ),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        zhCN.tenantProfile.ownership.pending.expires.replace(
          '{{expires}}',
          formatInstant(PENDING.expires_at, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
        ),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('lets the owner cancel the pending request and keeps the ownership unchanged', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const cancelled: unknown[] = []
    let pending: typeof PENDING | null = PENDING
    renderPage({
      getTenantProfile: () => Promise.resolve(ownerProfile()),
      backend: {
        getPendingOrganizationOwner: () => Promise.resolve(pending),
        cancelOrganizationOwner: (input) => {
          cancelled.push(input)
          pending = null
          return Promise.resolve()
        },
      },
    })
    await ready()

    expect(
      await screen.findByText(zhCN.tenantProfile.ownership.pending.title),
    ).toBeInTheDocument()
    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.pending.cancel,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.tenantProfile.ownership.pending.cancelTitle,
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantProfile.ownership.pending.cancelConfirm,
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', {
          name: zhCN.tenantProfile.ownership.pending.cancelTitle,
        }),
      ).toBeNull(),
    )

    await waitFor(() =>
      expect(cancelled).toEqual([{ tenant: TENANT, request: 'transfer-1' }]),
    )
    expect(
      await screen.findByText(zhCN.tenantProfile.ownership.pending.cancelled),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.tenantProfile.ownership.pending.title),
      ).toBeNull(),
    )
    expect(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).not.toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByText(`${OWNER_DISPLAY_NAME} (${OWNER_USERNAME})`),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('heading', {
        name: zhCN.tenantProfile.ownership.title,
      }),
    ).toHaveFocus()
  })

  it('leaves focus where the operator moved it when the cancel lands', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const settled = deferred<void>()
    let pending: typeof PENDING | null = PENDING
    renderPage({
      profile: ownerProfile(),
      backend: {
        getPendingOrganizationOwner: () => Promise.resolve(pending),
        cancelOrganizationOwner: () => {
          pending = null
          return settled.promise
        },
      },
    })
    await ready()

    await user.click(
      await screen.findByRole('button', {
        name: zhCN.tenantProfile.ownership.pending.cancel,
      }),
    )
    const dialog = await screen.findByRole('dialog', {
      name: zhCN.tenantProfile.ownership.pending.cancelTitle,
    })
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.tenantProfile.ownership.pending.cancelConfirm,
      }),
    )
    await waitFor(() =>
      expect(
        screen.queryByRole('dialog', {
          name: zhCN.tenantProfile.ownership.pending.cancelTitle,
        }),
      ).toBeNull(),
    )
    const transfer = screen.getByRole('button', {
      name: zhCN.tenantProfile.ownership.transfer.button,
    })
    transfer.focus()
    await act(async () => {
      settled.resolve()
    })
    await waitFor(() =>
      expect(
        screen.queryByText(zhCN.tenantProfile.ownership.pending.title),
      ).toBeNull(),
    )
    expect(transfer).toHaveFocus()
  })

  it('keeps the controls off the page for a tenant administrator who is not the owner', async () => {
    attachAccount(OTHER_ACCOUNT)
    renderPage({ profile: ownerProfile() })
    await ready()

    expect(screen.getByText(zhCN.tenantProfile.field.owner)).toBeInTheDocument()
    expect(
      screen.getByText(`${OWNER_DISPLAY_NAME} (${OWNER_USERNAME})`),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    ).toBeNull()
  })

  it('renders neither ownership control for a personal organization', async () => {
    attachAccount(OWNER)
    renderPage({
      tenant: 'my-space',
      profile: ownerProfile({ name: 'my-space', kind: 'personal' }),
    })
    await ready()

    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    ).toBeNull()
  })

  it('keeps the transfer submit unavailable with a reason until a target is chosen', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const requests: unknown[] = []
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: () => Promise.resolve({ members: [BOB], next_cursor: '' }),
        requestOrganizationOwner: (input) => {
          requests.push(input)
          return Promise.resolve(PENDING)
        },
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    const submit = within(dialog).getByRole('button', {
      name: zhCN.tenantProfile.ownership.transfer.confirmLabel,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.tenantProfile.ownership.transfer.targetRequired,
    )

    await user.click(submit)
    submit.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(
      within(dialog).queryByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    ).toBeNull()
    expect(requests).toEqual([])
    expect(document.activeElement).toBe(submit)
  })

  it('resolves a refused transfer to its readable text', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: () =>
          Promise.resolve({ members: [ADA, BOB], next_cursor: '' }),
        requestOrganizationOwner: () =>
          Promise.reject({ status: 403, code: 'client.http.403' }),
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    await user.click(
      await screen.findByRole('option', { name: 'Bob Stone (bob)' }),
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.transfer.confirmLabel,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantProfile.ownership.error.forbidden,
    )
  })

  it('names the conflict for a 409 refusal', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: () =>
          Promise.resolve({ members: [ADA, BOB], next_cursor: '' }),
        requestOrganizationOwner: () =>
          Promise.reject({ status: 409, code: 'tenant.conflict' }),
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    await user.click(
      await screen.findByRole('option', { name: 'Bob Stone (bob)' }),
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.transfer.confirmLabel,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantProfile.ownership.error.conflict,
    )
  })

  it('names the missing organization for a 404 refusal', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        deleteOrganization: () =>
          Promise.reject({ status: 404, code: 'client.http.404' }),
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: deleteTitle() })
    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.delete.nameLabel,
      ),
      ORG_URL_NAME,
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.delete.confirmLabel,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantProfile.ownership.error.notFound,
    )
  })

  it('falls back to its own unknown text for any other refusal', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: () =>
          Promise.resolve({ members: [ADA, BOB], next_cursor: '' }),
        requestOrganizationOwner: () =>
          Promise.reject({ status: 503, code: 'client.http.503' }),
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    await user.click(
      await screen.findByRole('option', { name: 'Bob Stone (bob)' }),
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.transfer.confirmLabel,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantProfile.ownership.error.unknown,
    )
  })

  it('deletes the organization after the name is typed and hands the confirmation to the departure action', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const deletes: unknown[] = []
    const onOrganizationLeft = vi.fn()
    renderPage({
      profile: ownerProfile(),
      onOrganizationLeft,
      backend: {
        deleteOrganization: (input) => {
          deletes.push(input)
          return Promise.resolve()
        },
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: deleteTitle() })
    const confirm = within(dialog).getByRole('button', {
      name: zhCN.tenantProfile.ownership.delete.confirmLabel,
    })
    expect(confirm).toHaveAttribute('aria-disabled', 'true')
    expect(confirm).toHaveAccessibleDescription(
      zhCN.tenantProfile.ownership.delete.nameMismatch,
    )

    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.delete.nameLabel,
      ),
      ORG_URL_NAME,
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.delete.confirmLabel,
    )

    await waitFor(() => expect(deletes).toEqual([{ tenant: TENANT }]))
    const message = zhCN.tenantProfile.ownership.delete.saved.replace(
      '{{name}}',
      ORG_NAME,
    )
    await waitFor(() => expect(onOrganizationLeft).toHaveBeenCalledWith(message))
    expect(screen.queryByText(message)).toBeNull()
    expect(await screen.findByText(zhCN.tenants.status.deleted)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    ).toBeNull()
  })

  it('keeps the confirmation in the card and lands focus on its heading when the host does not depart', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: { deleteOrganization: () => Promise.resolve() },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: deleteTitle() })
    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.delete.nameLabel,
      ),
      ORG_URL_NAME,
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.delete.confirmLabel,
    )

    const message = zhCN.tenantProfile.ownership.delete.saved.replace(
      '{{name}}',
      ORG_NAME,
    )
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    ).toBeNull()
    expect(
      screen.getByRole('heading', {
        name: zhCN.tenantProfile.ownership.title,
      }),
    ).toHaveFocus()
  })

  it('names the conflict when the delete meets a 409', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        deleteOrganization: () =>
          Promise.reject({ status: 409, code: 'tenant.conflict' }),
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: deleteTitle() })
    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.delete.nameLabel,
      ),
      ORG_URL_NAME,
    )
    await armAndConfirm(
      user,
      dialog,
      zhCN.tenantProfile.ownership.delete.confirmLabel,
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.tenantProfile.ownership.error.conflict,
    )
  })

  it('narrows the transfer candidates through the member search', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const queried: Array<string | undefined> = []
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: (_tenant, params) => {
          queried.push(params?.query)
          return Promise.resolve({
            members: params?.query === undefined ? [BOB, SUSPENDED] : [BOB],
            next_cursor: '',
          })
        },
      },
    })
    await ready()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    await waitFor(() => expect(queried).toEqual([undefined]))

    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.transfer.searchLabel,
      ),
      'Bob',
    )
    await waitFor(() => expect(queried.at(-1)).toBe('Bob'))
    expect(
      screen.queryByRole('option', { name: 'Carol Suspended (carol)' }),
    ).toBeNull()
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    expect(
      await screen.findByRole('option', { name: 'Bob Stone (bob)' }),
    ).toBeInTheDocument()
  })

  it('has no axe violations with the ownership controls and dialogs', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: () =>
          Promise.resolve({ members: [ADA, BOB], next_cursor: '' }),
      },
    })
    await ready()
    await expectNoAxeViolations()

    await user.click(
      screen.getByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    )
    const dialog = await screen.findByRole('dialog', { name: transferTitle() })
    await user.click(
      within(dialog).getByRole('combobox', {
        name: zhCN.tenantProfile.ownership.transfer.targetLabel,
      }),
    )
    await user.click(
      await screen.findByRole('option', { name: 'Bob Stone (bob)' }),
    )
    await expectNoAxeViolations()
  })
})
