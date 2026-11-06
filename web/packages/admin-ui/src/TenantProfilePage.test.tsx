/**
 * TenantProfilePage: the organization's own profile page.
 *
 * It reads one tenant's organization profile, renders the identity card
 * read-only (the alias is never an input), edits the four descriptive
 * fields, and reports a landed save through its feedback scope while
 * calling the host's `onTenantUpdated`. The read's 403, 404 and loading
 * branches are the shared async section's; a refused save keeps the draft
 * exactly as the operator typed it.
 */

import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { TenantProfilePage } from './TenantProfilePage.js'
import {
  OrganizationAdminOperationsProvider,
  type AdminTenantProfile,
} from './organization-operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  profileFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'

function renderPage({
  profile = profileFixture(),
  getTenantProfile,
  updateTenantProfile,
  onTenantUpdated,
}: {
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
} = {}) {
  const operations = createAdminOperations(
    makeAdminBackend({
      getTenantProfile: () =>
        getTenantProfile === undefined
          ? Promise.resolve(profile)
          : getTenantProfile(TENANT),
      ...(updateTenantProfile === undefined ? {} : { updateTenantProfile }),
    }),
  )
  return renderWithProviders(
    <OrganizationAdminOperationsProvider operations={operations}>
      <TenantProfilePage
        tenant={TENANT}
        onTenantUpdated={onTenantUpdated}
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
  it('renders the identity read-only with no alias input', async () => {
    renderPage({
      profile: profileFixture({
        id: 'acme',
        alias: 'acme-inc',
        displayName: 'Acme Incorporated',
        status: 'active',
      }),
    })
    await ready()

    expect(screen.getByText('acme-inc')).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.status.active)).toBeInTheDocument()
    // The alias is stated, never editable: there is no control named after
    // it, and the read-only explanation sits beside the identity card.
    expect(
      screen.queryByLabelText(zhCN.tenantProfile.field.alias),
    ).toBeNull()
    expect(
      screen.getByText(zhCN.tenantProfile.identity.aliasHint),
    ).toBeInTheDocument()
    // The display name is the profile card's editable value, not a second
    // read-only statement on the identity card.
    expect(
      screen.getByLabelText(zhCN.tenantProfile.field.displayName),
    ).toHaveValue('Acme Incorporated')
  })

  it('falls back to the alias when the profile names no display name', async () => {
    renderPage({
      profile: profileFixture({ alias: 'acme-inc', displayName: '' }),
    })
    await ready()
    // The identity card's alias is the only place the name is stated on the
    // page; the form's display-name field is empty and offers the alias as
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
        alias: 'acme-inc',
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

  it('allows the display name to be cleared and names the alias in the confirmation', async () => {
    const user = userEvent.setup()
    const updates: unknown[] = []
    renderPage({
      profile: profileFixture({ alias: 'acme-inc', displayName: 'Acme' }),
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
    // The confirmation names what every surface will show: the alias.
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
