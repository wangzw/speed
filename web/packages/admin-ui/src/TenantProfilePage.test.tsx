/**
 * TenantProfilePage: the organization's own profile page.
 *
 * It reads one tenant's organization profile, renders the identity card
 * read-only (the name is never an input), edits the four descriptive
 * fields, and reports a landed save through its feedback scope while
 * calling the host's `onTenantUpdated`. The read's 403, 404 and loading
 * branches are the shared async section's; a refused save keeps the draft
 * exactly as the operator typed it.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { attachSession } from '@speed/auth-core'
import { uiKitResources } from '@speed/ui-kit'
import { TenantProfilePage } from './TenantProfilePage.js'
import {
  OrganizationAdminOperationsProvider,
  type AdminTenantProfile,
} from './organization-operations.js'
import type { AdminMember } from './operations.js'
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
/** An owner account identifier the page must never render. */
const UNSHOWN_OWNER = '8f14e45f-ceea-467a-9d3d-0c1b7e2f4a51'

/** The ui-kit label the confirm button shows once the two-step guard arms. */
const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

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

/** Attach the session the page reads the acting account from. */
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

/** The page's heading, once its read settled. */
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
        displayName: 'Acme Incorporated',
        status: 'active',
      }),
    })
    await ready()

    expect(screen.getByText('acme-inc')).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.status.active)).toBeInTheDocument()
    // The name is stated, never editable: there is no control named after
    // it, and the read-only explanation sits beside the identity card.
    expect(
      screen.queryByLabelText(zhCN.tenantProfile.field.name),
    ).toBeNull()
    expect(
      screen.getByText(zhCN.tenantProfile.identity.nameHint),
    ).toBeInTheDocument()
    // The display name is the profile card's editable value, not a second
    // read-only statement on the identity card.
    expect(
      screen.getByLabelText(zhCN.tenantProfile.field.displayName),
    ).toHaveValue('Acme Incorporated')
  })

  it('falls back to the name when the profile names no display name', async () => {
    renderPage({
      profile: profileFixture({ name: 'acme-inc', displayName: '' }),
    })
    await ready()
    // The identity card's name is the only place it is stated on the
    // page; the form's display-name field is empty and offers the name as
    // its placeholder.
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
        displayName: 'Old name',
        description: 'Old description',
        contactEmail: 'old@acme.example',
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
      profile: profileFixture({ name: 'acme-inc', displayName: 'Acme' }),
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
    // The confirmation names what every surface will show: the name.
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
    // The refusal did not withdraw the edit, and the host was not asked to
    // reload a list that did not change.
    expect(displayName).toHaveValue('Unsaved name')
    expect(onTenantUpdated).not.toHaveBeenCalled()
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
    displayName: ORG_NAME,
    ownerAccount: OWNER,
    ownerDisplayName: OWNER_DISPLAY_NAME,
    ownerUsername: OWNER_USERNAME,
    ...overrides,
  })
}

/** The owner row's whole definition row: the label cell and its value. */
function ownerRow(container: HTMLElement): HTMLElement {
  const label = within(container).getByText(zhCN.tenantProfile.field.owner)
  const row = label.closest('div')
  if (row === null) {
    throw new Error('the owner row was not found')
  }
  return row
}

/** The identifier must be absent from the rendered page, attributes
 * included: it is the internal key, never a fallback for the label. */
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

/** Arm the dialog's two-step guard and fire it. */
async function armAndConfirm(
  user: ReturnType<typeof userEvent.setup>,
  dialog: HTMLElement,
  confirmLabel: string,
): Promise<void> {
  await user.click(within(dialog).getByRole('button', { name: confirmLabel }))
  const again = await within(dialog).findByRole('button', {
    name: CONFIRM_AGAIN_LABEL,
  })
  await waitFor(() => expect(again).toBeEnabled())
  await user.click(again)
}

describe('TenantProfilePage owner row', () => {
  it('shows the display name and the username, never the account identifier', async () => {
    const { container } = renderPage({
      profile: profileFixture({
        ownerAccount: UNSHOWN_OWNER,
        ownerDisplayName: OWNER_DISPLAY_NAME,
        ownerUsername: OWNER_USERNAME,
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
    // The username is shown because a display name alone does not identify
    // the owner: two members may carry the same one.
    const first = renderPage({
      profile: profileFixture({
        ownerAccount: UNSHOWN_OWNER,
        ownerDisplayName: 'Alex Kim',
        ownerUsername: 'alex-one',
      }),
    })
    await ready()
    expect(
      within(ownerRow(first.container)).getByText('Alex Kim (alex-one)'),
    ).toBeInTheDocument()
    first.unmount()

    const second = renderPage({
      profile: profileFixture({
        ownerAccount: UNSHOWN_OWNER,
        ownerDisplayName: 'Alex Kim',
        ownerUsername: 'alex-two',
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
        ownerAccount: UNSHOWN_OWNER,
        ownerDisplayName: OWNER_DISPLAY_NAME,
        ownerUsername: '',
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
        ownerAccount: UNSHOWN_OWNER,
        ownerDisplayName: '',
        ownerUsername: OWNER_USERNAME,
      }),
    })
    await ready()

    expect(
      within(ownerRow(container)).getByText(OWNER_USERNAME),
    ).toBeInTheDocument()
    expectNoOwnerAccount(container, UNSHOWN_OWNER)
  })

  it('shows the neutral label when the owner cannot be resolved, never the identifier', async () => {
    // A static credential-table subject has no account row, so both name
    // fields are empty; the page still must not fall back to the identifier.
    const { container } = renderPage({
      profile: profileFixture({
        ownerAccount: UNSHOWN_OWNER,
        ownerDisplayName: '',
        ownerUsername: '',
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
  it('lets the current owner transfer the organization and hands the confirmation to the departure action', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const transfers: unknown[] = []
    const onTenantUpdated = vi.fn()
    const onOrganizationLeft = vi.fn()
    renderPage({
      profile: ownerProfile(),
      onTenantUpdated,
      onOrganizationLeft,
      backend: {
        listMembers: () =>
          Promise.resolve({
            members: [ADA, BOB, SUSPENDED],
            next_cursor: '',
          }),
        transferOrganizationOwner: (input) => {
          transfers.push(input)
          return Promise.resolve()
        },
      },
    })
    await ready()

    // The owner is shown by display name and username; the transfer control
    // is offered because the acting account is the profile's ownerAccount.
    expect(screen.getByText(zhCN.tenantProfile.field.owner)).toBeInTheDocument()
    expect(
      screen.getByText(`${OWNER_DISPLAY_NAME} (${OWNER_USERNAME})`),
    ).toBeInTheDocument()

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
    // Only active members other than the owner are offered.
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
      expect(transfers).toEqual([{ tenant: TENANT, account: OTHER_ACCOUNT }]),
    )
    const message = zhCN.tenantProfile.ownership.transfer.saved
      .replace('{{name}}', ORG_NAME)
      .replace('{{account}}', 'Bob Stone (bob)')
    // The caller is no longer a member: the confirmation goes to the host's
    // departure action, which shows it where the viewer lands.
    await waitFor(() => expect(onOrganizationLeft).toHaveBeenCalledWith(message))
    expect(onTenantUpdated).not.toHaveBeenCalled()
    expect(screen.queryByText(message)).toBeNull()
    // The landed write is reflected locally; the page issues no re-read the
    // caller would be refused. The new owner's names were not read here, so
    // the row falls back to the neutral label rather than a stale name.
    expect(
      await screen.findByText(zhCN.tenantProfile.owner.unnamed),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).toBeNull()
  })

  it('keeps the confirmation in the card when the host supplies no departure action', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    const onTenantUpdated = vi.fn()
    renderPage({
      profile: ownerProfile(),
      onTenantUpdated,
      backend: {
        listMembers: () =>
          Promise.resolve({ members: [ADA, BOB], next_cursor: '' }),
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

    expect(
      await screen.findByText(
        zhCN.tenantProfile.ownership.transfer.saved
          .replace('{{name}}', ORG_NAME)
          .replace('{{account}}', 'Bob Stone (bob)'),
      ),
    ).toBeInTheDocument()
    expect(onTenantUpdated).toHaveBeenCalledTimes(1)
    // The controls are withdrawn: the caller is no longer a member.
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.transfer.button,
      }),
    ).toBeNull()
  })

  it('keeps the controls off the page for a tenant administrator who is not the owner', async () => {
    attachAccount(OTHER_ACCOUNT)
    renderPage({ profile: ownerProfile() })
    await ready()

    // The administrator still sees who the owner is, but no ownership
    // control: the controls branch on ownerAccount, not on the row's label.
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

  it('resolves a refused transfer to its readable text', async () => {
    const user = userEvent.setup()
    attachAccount(OWNER)
    renderPage({
      profile: ownerProfile(),
      backend: {
        listMembers: () =>
          Promise.resolve({ members: [ADA, BOB], next_cursor: '' }),
        transferOrganizationOwner: () =>
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
        transferOrganizationOwner: () =>
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
        transferOrganizationOwner: () =>
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
    expect(confirm).toBeDisabled()

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
    // The terminal state is reflected locally and the controls are gone.
    expect(await screen.findByText(zhCN.tenants.status.deleted)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', {
        name: zhCN.tenantProfile.ownership.delete.button,
      }),
    ).toBeNull()
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
    // The dialog reads the first page before any search is typed.
    await waitFor(() => expect(queried).toEqual([undefined]))

    await user.type(
      within(dialog).getByLabelText(
        zhCN.tenantProfile.ownership.transfer.searchLabel,
      ),
      'Bob',
    )
    // The search is a server-side prefix read, so it reaches members beyond
    // the dialog's first page instead of paging the whole roster.
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
