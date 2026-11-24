
import { useState } from 'react'
import type { ReactElement } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { typographyClasses } from '@mui/material/Typography'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { PermissionsPage } from './PermissionsPage.js'
import type {
  AdminAccount,
  AdminBinding,
  AdminGrant,
  AdminPermission,
  AdminRole,
  AdminWriteRoleInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { containerMaxWidthOf } from '../test-utils/container-visibility.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
  type AdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

function contains(value: string): RegExp {
  return new RegExp(value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
}

function byName(name: string): string {
  return text(zhCN.common.accountReference.name, { name })
}

function byIdentifier(identifier: string): string {
  return text(zhCN.common.accountReference.identifier, { identifier })
}

function byNameAndIdentifier(name: string, identifier: string): string {
  return text(zhCN.common.accountReference.nameWithIdentifier, {
    name,
    identifier,
  })
}

const TENANT = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

function role(overrides: Partial<AdminRole> = {}): AdminRole {
  return {
    tenant: 'acme',
    role: 'editor',
    builtin: false,
    can_manage: true,
    description: '',
    permission_count: 0,
    member_count: null,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function grant(overrides: Partial<AdminGrant> = {}): AdminGrant {
  return {
    tenant: 'acme',
    role: 'editor',
    permission: 'crm.deal.read',
    resource: 'deal',
    action: 'read',
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function permission(
  value: string,
  description = '',
  overrides: Partial<AdminPermission> = {},
): AdminPermission {
  return {
    permission: value,
    description,
    group: '',
    dangerous: false,
    grantable: true,
    ...overrides,
  }
}

function account(overrides: Partial<AdminAccount> = {}): AdminAccount {
  return {
    id: 'acc-1',
    kind: 'user',
    status: 'active',
    display_name: 'Operator',
    mfa_required: false,
    protected: false,
    password_set: true,
    identifiers: [],
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function binding(overrides: Partial<AdminBinding> = {}): AdminBinding {
  return {
    tenant: 'acme',
    account: 'acc-1',
    role: 'editor',
    granted_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText(zhCN.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

function stubScrollIntoView(): ReturnType<typeof vi.fn> {
  const spy = vi.fn()
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    writable: true,
    value: spy,
  })
  return spy
}

afterEach(() => {
  delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
})

async function expectRefreshInert(
  listRoles: ReturnType<typeof vi.fn>,
  readsBefore: number,
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
  await waitForCondition(() => {
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(listRoles).toHaveBeenCalledTimes(readsBefore)
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((settle, fail) => {
    resolve = settle
    reject = fail
  })
  return { promise, resolve, reject }
}

function withAuthz(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listPermissions: () =>
        Promise.resolve([
          permission('crm.deal.read'),
          permission('crm.deal.write'),
        ]),
      listAccounts: () =>
        Promise.resolve({
          accounts: [
            account(),
            account({
              id: 'acc-2',
              display_name: 'Ada',
              identifiers: [
                { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
              ],
            }),
          ],
          next_cursor: '',
        }),
      ...overrides,
    }),
  )
}

describe('PermissionsPage', () => {
  it('prompts for a tenant before listing', () => {
    const operations = withAuthz()
    renderWithProviders(<PermissionsPage />, { operations })
    expect(screen.getByText(zhCN.permissions.needTenant.title)).toBeInTheDocument()
  })

  it('does not read an empty tenant scope on refresh', async () => {
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const operations = withAuthz({ listRoles })
    renderWithProviders(<PermissionsPage />, { operations })

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      zhCN.permissions.needTenant.description,
    )
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(listRoles).not.toHaveBeenCalled()
  })

  it('disables the refresh while the role scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const operations = withAuthz({
      listRoles,
      writeRole: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    const reads = listRoles.mock.calls.length

    await user.type(screen.getByLabelText(zhCN.permissions.roles.create.roleLabel), 'auditor')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }))
    await expectRefreshInert(listRoles, reads)

    await act(async () => {
      pending.resolve()
    })
    await waitForCondition(() => {
      expect(
        screen.getByRole('button', { name: zhCN.common.refresh }),
      ).not.toHaveAttribute('aria-disabled', 'true')
    })
  })

  it('disables the role create submit without a trimmed name and clears both drafts after a save', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')

    const name = screen.getByLabelText(
      zhCN.permissions.roles.create.roleLabel,
    )
    const description = screen.getByLabelText(
      zhCN.permissions.roles.create.descriptionLabel,
    )
    const submit = screen.getByRole('button', {
      name: zhCN.permissions.roles.create.submit,
    })
    expect(submit).toHaveAttribute('aria-disabled', 'true')

    await user.type(name, '   ')
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    await user.clear(name)
    await user.type(name, 'auditor')
    await user.type(description, 'reads audit records')
    expect(submit).not.toHaveAttribute('aria-disabled', 'true')

    await user.click(submit)
    expect(
      await screen.findByText(
        text(zhCN.permissions.roles.saved, { role: 'auditor' }),
      ),
    ).toBeInTheDocument()
    expect(name).toHaveValue('')
    expect(description).toHaveValue('')
  })

  it('disables the refresh while the grants scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const operations = withAuthz({
      listRoles,
      grantPermission: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    const reads = listRoles.mock.calls.length

    await user.click(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }),
    )
    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))
    await expectRefreshInert(listRoles, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while the bindings scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const operations = withAuthz({
      listRoles,
      bindRole: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    const reads = listRoles.mock.calls.length

    await user.type(screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }), 'viewer')
    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.bindings.bind }))
    await expectRefreshInert(listRoles, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while the lookup scope is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const operations = withAuthz({
      listRoles,
      listAccountBindings: () => Promise.resolve([binding()]),
      unbindRole: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    const reads = listRoles.mock.calls.length

    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.lookup.apply }))
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.bindings.unbindAria, { account: 'Operator', role: 'editor' }),
      }),
    )
    await expectRefreshInert(listRoles, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/admin/permissions?tenant=acme')
    try {
      const listRoles = vi.fn(() => Promise.resolve([role()]))
      const operations = withAuthz({ listRoles })
      renderWithProviders(<PermissionsPage />, { operations })
      expect(await screen.findByText('editor')).toBeInTheDocument()
      expect(screen.queryByText(zhCN.permissions.needTenant.title)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent('Acme')
      expect(listRoles).toHaveBeenCalledWith('acme')
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('locks the scope to a controlled tenant and hides the selector', async () => {
    window.history.replaceState({}, '', '/admin/permissions?tenant=globex')
    try {
      const listRoles = vi.fn(() => Promise.resolve([role()]))
      const operations = withAuthz({ listRoles })
      renderWithProviders(<PermissionsPage tenant="acme" />, { operations })

      await waitForCondition(() => {
        expect(listRoles).toHaveBeenCalledWith('acme')
      })
      expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('seeds from defaultTenant ahead of the URL and reports a re-selection', async () => {
    window.history.replaceState({}, '', '/admin/permissions?tenant=globex')
    try {
      const user = userEvent.setup()
      const listRoles = vi.fn(() => Promise.resolve([role()]))
      const onTenantChange = vi.fn()
      const operations = withAuthz({
        listRoles,
        listTenants: () =>
          Promise.resolve([
            TENANT,
            tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
          ]),
      })
      renderWithProviders(
        <PermissionsPage defaultTenant="acme" onTenantChange={onTenantChange} />,
        { operations },
      )

      await waitForCondition(() => {
        expect(listRoles).toHaveBeenCalledWith('acme')
      })
      await waitForCondition(() => {
        expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent(
          'Acme',
        )
      })

      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await waitForCondition(() => {
        expect(listRoles).toHaveBeenCalledWith('globex')
      })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('lists roles, catalog and bindings for the tenant', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listBindings: () => Promise.resolve([binding({ role: 'viewer' })]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('editor')).toBeInTheDocument()
    expect(screen.getByText('crm.deal.read')).toBeInTheDocument()
    expect(screen.getByText('Operator')).toBeInTheDocument()
  })

  it("keeps a read-only grants entry on a builtin role and drops its write controls", async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([
          role({ role: 'platform-admin', builtin: true }),
          role({ role: 'editor', builtin: false }),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('platform-admin')).toBeInTheDocument()
    expect(screen.getByText(zhCN.permissions.roles.builtinLocked)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.viewGrantsAria, { role: 'platform-admin' }) }),
    ).not.toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.queryByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'platform-admin' }) }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'platform-admin' }) }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }),
    ).not.toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'editor' }) }),
    ).not.toHaveAttribute('aria-disabled', 'true')
  })

  it('opens a builtin role grants read-only, with no grant or revoke control', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role({ role: 'platform-admin', builtin: true })]),
      listGrants: (tenant, role) =>
        Promise.resolve(
          role === 'platform-admin'
            ? [grant({ role: 'platform-admin' })]
            : [],
        ),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.viewGrantsAria, { role: 'platform-admin' }),
      }),
    )
    const grantsSection = screen
      .getByRole('heading', { name: zhCN.permissions.grants.title })
      .closest('section') as HTMLElement
    expect(await within(grantsSection).findByText('crm.deal.read')).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.permissions.grants.builtinReadOnly),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText(zhCN.permissions.grants.permissionLabel)).toBeNull()
    expect(screen.queryByRole('button', { name: zhCN.permissions.grants.add })).toBeNull()
    expect(
      screen.queryByRole('button', { name: text(zhCN.permissions.grants.revokeAria, { permission: 'crm.deal.read' }) }),
    ).toBeNull()
  })

  it('spells a grant with the same permission code the catalog lists', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () => Promise.resolve([grant()]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }))
    const grantsSection = screen.getByRole('heading', { name: zhCN.permissions.grants.title }).closest('section')
    const catalogSection = screen.getByRole('heading', { name: zhCN.permissions.catalog.title }).closest('section')
    expect(grantsSection).not.toBeNull()
    expect(catalogSection).not.toBeNull()
    const grantText = (
      await within(grantsSection as HTMLElement).findByText('crm.deal.read')
    ).textContent
    const catalogText = (
      await within(catalogSection as HTMLElement).findByText('crm.deal.read')
    ).textContent
    expect(grantText).toEqual(catalogText)
  })

  it('shows the permission description under the code in the catalog and the grant picker', async () => {
    const user = userEvent.setup()
    const described = 'Read the tenant deal pipeline'
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listPermissions: () =>
        Promise.resolve([
          permission('crm.deal.read', described),
          permission('crm.deal.write'),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)

    const catalog = section(zhCN.permissions.catalog.title)
    const code = await within(catalog).findByText('crm.deal.read')
    const cell = code.closest('td')
    expect(cell).not.toBeNull()
    expect(within(cell as HTMLElement).getByText(described)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    await user.click(
      screen.getByLabelText(zhCN.permissions.grants.permissionLabel),
    )
    const option = await screen.findByRole('option', {
      name: contains('crm.deal.read'),
    })
    expect(within(option).getByText(described)).toBeInTheDocument()
    await user.click(option)
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    const picker = screen.getByRole('combobox', {
      name: zhCN.permissions.grants.permissionLabel,
    })
    expect(picker).toHaveTextContent('crm.deal.read')
    expect(picker).not.toHaveTextContent(described)
  })

  it('renders a permission with an empty description as its code alone', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listPermissions: () => Promise.resolve([permission('crm.deal.read')]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)

    const catalog = section(zhCN.permissions.catalog.title)
    const code = await within(catalog).findByText('crm.deal.read')
    const cell = code.closest('td')
    expect(cell).not.toBeNull()
    expect(code.parentElement?.children).toHaveLength(1)
    expect(
      (cell as HTMLElement).querySelector(`.${typographyClasses.caption}`),
    ).toBeNull()

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    await user.click(
      screen.getByLabelText(zhCN.permissions.grants.permissionLabel),
    )
    const option = await screen.findByRole('option', {
      name: 'crm.deal.read',
    })
    expect(option.querySelector(`.${typographyClasses.caption}`)).toBeNull()
    expect(option.textContent).toBe('crm.deal.read')
    await user.click(option)
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    expect(
      screen.getByRole('combobox', {
        name: zhCN.permissions.grants.permissionLabel,
      }),
    ).toHaveTextContent(/^crm\.deal\.read$/)
  })

  it("shows a role's free-text description from the server", async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([
          role({
            description: 'Reads audit records',
          }),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    const roles = section(zhCN.permissions.roles.title)
    expect(
      await within(roles).findByText('Reads audit records'),
    ).toBeInTheDocument()
  })

  it('renders a grant by the catalog description, the server code and a high-risk badge', async () => {
    const user = userEvent.setup()
    const described = "Manage the organization's roles"
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listPermissions: () =>
        Promise.resolve([
          permission('authz.tenant.role.manage', described, {
            dangerous: true,
          }),
        ]),
      listGrants: () =>
        Promise.resolve([
          grant({
            permission: 'authz.tenant.role.manage',
            resource: 'tenant_role',
            action: 'manage',
          }),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    const grants = section(zhCN.permissions.grants.title)
    const cell = (await within(grants).findByText(described)).closest('td')
    expect(cell).not.toBeNull()
    expect(
      within(cell as HTMLElement).getByText('authz.tenant.role.manage'),
    ).toBeInTheDocument()
    expect(
      within(cell as HTMLElement).getByText(
        zhCN.permissions.catalog.dangerous,
      ),
    ).toBeInTheDocument()
  })

  it('requires the two-step confirmation before granting a dangerous permission', async () => {
    const user = userEvent.setup()
    const granted: string[] = []
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listPermissions: () =>
        Promise.resolve([
          permission('crm.deal.read', '', { dangerous: true }),
          permission('crm.deal.write'),
        ]),
      grantPermission: (input) => {
        granted.push(input.permission)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )

    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(
      await screen.findByRole('option', { name: contains('crm.deal.read') }),
    )
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))
    const dialog = await screen.findByRole('dialog')
    expect(granted).toEqual([])
    await user.click(
      within(dialog).getByRole('button', {
        name: zhCN.permissions.grants.dangerousConfirm,
      }),
    )
    expect(granted).toEqual([])
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitForCondition(() => {
      expect(granted).toEqual(['crm.deal.read'])
    })
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))
    await waitForCondition(() => {
      expect(granted).toEqual(['crm.deal.read', 'crm.deal.write'])
    })
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('groups the catalog by the server group and marks entries the caller cannot grant', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listPermissions: () =>
        Promise.resolve([
          permission('authz.role.read', 'Read roles', {
            group: 'authorization',
          }),
          permission('authn.member.read', 'Read members', {
            group: 'membership',
            grantable: false,
          }),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)

    const catalog = section(zhCN.permissions.catalog.title)
    expect(
      await within(catalog).findByText(
        zhCN.permissions.catalog.group.authorization,
      ),
    ).toBeInTheDocument()
    expect(
      within(catalog).getByText(zhCN.permissions.catalog.group.membership),
    ).toBeInTheDocument()
    expect(
      await within(catalog).findByText('authn.member.read'),
    ).toBeInTheDocument()
    expect(
      within(catalog).getByText(zhCN.permissions.catalog.notGrantable),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    expect(
      await screen.findByRole('option', { name: contains('authz.role.read') }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('option', { name: contains('authn.member.read') }),
    ).toBeNull()
  })

  it('writes the role description as free text', async () => {
    const user = userEvent.setup()
    const written: AdminWriteRoleInput[] = []
    const operations = withAuthz({
      writeRole: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      'auditor',
    )
    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.descriptionLabel),
      'Reads audit records',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }),
    )
    await waitForCondition(() => {
      expect(written).toEqual([
        {
          tenant: 'acme',
          role: 'auditor',
          description: 'Reads audit records',
        },
      ])
    })
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () => Promise.resolve([grant()]),
      listBindings: () => Promise.resolve([binding({ role: 'viewer' })]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    const manage = await screen.findByRole('button', {
      name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
    })
    const removeRole = screen.getByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'editor' }) })
    const unbind = screen.getByRole('button', {
      name: text(zhCN.permissions.bindings.unbindAria, { account: 'Operator', role: 'viewer' }),
    })
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    await user.click(manage)
    const revoke = await screen.findByRole('button', {
      name: text(zhCN.permissions.grants.revokeAria, { permission: 'crm.deal.read' }),
    })
    for (const control of [manage, removeRole, unbind, refresh, revoke]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('creates a role through the bundle, without a builtin member', async () => {
    const user = userEvent.setup()
    const written: AdminWriteRoleInput[] = []
    const operations = withAuthz({
      writeRole: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(screen.queryByRole('checkbox')).toBeNull()
    await user.type(screen.getByLabelText(zhCN.permissions.roles.create.roleLabel), 'auditor')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }))
    await waitForCondition(() => {
      expect(written).toEqual([
        { tenant: 'acme', role: 'auditor', description: '' },
      ])
    })
    expect(await screen.findByText(text(zhCN.permissions.roles.saved, { role: 'auditor' }))).toBeInTheDocument()
  })

  it('grants and revokes a permission on the selected role', async () => {
    const user = userEvent.setup()
    const granted: string[] = []
    const revoked: string[] = []
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () => Promise.resolve([grant()]),
      grantPermission: (input) => {
        granted.push(input.permission)
        return Promise.resolve()
      },
      revokePermission: (input) => {
        revoked.push(input.permission)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }))
    const grantsSection = screen
      .getByRole('heading', { name: zhCN.permissions.grants.title })
      .closest('section') as HTMLElement
    await within(grantsSection).findByText('crm.deal.read')
    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.grants.revokeAria, { permission: 'crm.deal.read' }) }))
    await waitForCondition(() => {
      expect(granted).toEqual(['crm.deal.write'])
      expect(revoked).toEqual(['crm.deal.read'])
    })
    expect(
      await screen.findByText(text(zhCN.permissions.grants.revoked, { role: 'editor', permission: 'crm.deal.read' })),
    ).toBeInTheDocument()
  })

  it('revokes a grant whose resource is not the code segment', async () => {
    const user = userEvent.setup()
    const revoked: string[] = []
    const operations = withAuthz({
      listPermissions: () => Promise.resolve([permission('authn.mfa.reset')]),
      listRoles: () => Promise.resolve([role()]),
      listGrants: () =>
        Promise.resolve([
          grant({
            permission: 'authn.mfa.reset',
            resource: 'factor',
            action: 'reset',
          }),
        ]),
      revokePermission: (input) => {
        revoked.push(input.permission)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }))
    const grantsSection = screen
      .getByRole('heading', { name: zhCN.permissions.grants.title })
      .closest('section') as HTMLElement
    await within(grantsSection).findByText('authn.mfa.reset')
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.grants.revokeAria, { permission: 'authn.mfa.reset' }) }))
    await waitForCondition(() => {
      expect(revoked).toEqual(['authn.mfa.reset'])
    })
  })

  it('withholds revoke for a grant whose server permission is empty', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () =>
        Promise.resolve([
          grant({ permission: '', resource: 'retired', action: 'op' }),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    const grantsSection = screen
      .getByRole('heading', { name: zhCN.permissions.grants.title })
      .closest('section') as HTMLElement
    await within(grantsSection).findByText('retired/op')
    const revoke = within(grantsSection).getByRole('button', {
      name: text(zhCN.permissions.grants.revokeAria, {
        permission: 'retired/op',
      }),
    })
    expect(revoke).toHaveAttribute('aria-disabled', 'true')
    const reason = within(grantsSection).getByText(
      zhCN.permissions.grants.revokeUnavailable,
    )
    expect(reason).toBeInTheDocument()
    expect(revoke).toHaveAttribute('aria-describedby', reason.id)
  })

  it('binds the role to the chosen account, not to the typed name', async () => {
    const user = userEvent.setup()
    const bound: string[] = []
    const unbound: string[] = []
    const operations = withAuthz({
      listBindings: () => Promise.resolve([binding()]),
      bindRole: (input) => {
        bound.push(`${input.account}:${input.role}`)
        return Promise.resolve()
      },
      unbindRole: (input) => {
        unbound.push(input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Operator')
    await user.type(screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }), 'viewer')
    expect(screen.getByRole('button', { name: zhCN.permissions.bindings.bind })).toHaveAttribute('aria-disabled', 'true')
    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.bindings.bind }))
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.bindings.unbindAria, { account: 'Operator', role: 'editor' }) }))
    await waitForCondition(() => {
      expect(bound).toEqual(['acc-2:viewer'])
      expect(unbound).toEqual(['editor'])
    })
    expect(
      await screen.findByText(text(zhCN.permissions.bindings.unbound, { account: byName('Operator'), role: 'editor' })),
    ).toBeInTheDocument()
  })

  it('keeps a binding account id marked unresolved when the account read is refused', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listBindings: () => Promise.resolve([binding({ role: 'viewer' })]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(cell).not.toHaveTextContent('acc-1')
    expect(
      screen.getByRole('button', {
        name: text(zhCN.permissions.bindings.unbindAria, {
          account: zhCN.common.accountUnavailable,
          role: 'viewer',
        }),
      }),
    ).toBeInTheDocument()
  })

  it('caps the binding cells that the refused identity lookup widens', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listBindings: () => Promise.resolve([binding({ role: 'viewer' })]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)

    const bindings = section(zhCN.permissions.bindings.title)
    const accountMarker = await within(bindings).findByTitle(zhCN.common.accountUnresolved)
    const accountCell = accountMarker.closest('td')
    expect(containerMaxWidthOf(accountCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    const roleCell = within(bindings).getByText('viewer').closest('td')
    expect(roleCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(containerMaxWidthOf(roleCell!.firstElementChild!)).toEqual(new Map())
  })

  it('looks up the bindings of the chosen account by its id', async () => {
    const user = userEvent.setup()
    const looked: string[] = []
    const operations = withAuthz({
      listAccountBindings: (query) => {
        looked.push(query.account ?? '')
        return Promise.resolve([binding({ role: 'viewer' })])
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(screen.getByRole('button', { name: zhCN.permissions.lookup.apply })).toHaveAttribute('aria-disabled', 'true')
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.lookup.apply }))
    await waitForCondition(() => {
      expect(looked).toEqual(['acc-2'])
    })
    expect(await screen.findByText('viewer')).toBeInTheDocument()
  })

  it('gives the lookup read its loading and empty placeholders', async () => {
    const user = userEvent.setup()
    const pending = deferred<readonly AdminBinding[]>()
    const operations = withAuthz({
      listAccountBindings: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.lookup.apply }))

    const lookup = section(zhCN.permissions.lookup.title)
    expect(
      within(lookup).getByRole('status', {
        name: zhCN.permissions.lookup.loading,
      }),
    ).toBeInTheDocument()

    await act(async () => {
      pending.resolve([])
      await pending.promise
    })
    expect(await within(lookup).findByText(zhCN.permissions.lookup.empty.title)).toBeInTheDocument()
  })

  it('gives the lookup read a retryable failure and re-reads on retry', async () => {
    const user = userEvent.setup()
    let reads = 0
    const operations = withAuthz({
      listAccountBindings: () => {
        reads += 1
        return reads === 1
          ? Promise.reject(new Error('boom'))
          : Promise.resolve([])
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.lookup.apply }))

    const lookup = section(zhCN.permissions.lookup.title)
    expect(
      await within(lookup).findByText(zhCN.permissions.lookup.error.title),
    ).toBeInTheDocument()
    expect(
      within(lookup).getByText(zhCN.permissions.lookup.error.description),
    ).toBeInTheDocument()
    await user.click(within(lookup).getByRole('button', { name: zhCN.permissions.lookup.retry }))
    expect(await within(lookup).findByText(zhCN.permissions.lookup.empty.title)).toBeInTheDocument()
    expect(reads).toBe(2)
    expect(
      within(lookup).queryByText(zhCN.permissions.lookup.error.title),
    ).toBeNull()
  })

  it('confirms before deleting a role', async () => {
    const user = userEvent.setup()
    const removed: string[] = []
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      removeRole: (input) => {
        removed.push(input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'editor' }) }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.permissions.roles.removeConfirm }))
    expect(removed).toEqual([])
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitForCondition(() => {
      expect(removed).toEqual(['editor'])
    })
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByText(text(zhCN.permissions.roles.removed, { role: 'editor' }))).toBeInTheDocument()
  })

  it('judges the deleted role against the selection current when the removal settles', async () => {
    const user = userEvent.setup()
    const removal = deferred<void>()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role({ role: 'alpha' }), role({ role: 'beta' })]),
      removeRole: () => removal.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'beta' }) })

    await user.click(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'beta' }) }),
    )
    expect(await screen.findByText(text(zhCN.permissions.grants.forRole, { role: 'beta' }))).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'beta' }) }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.permissions.roles.removeConfirm }))
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'alpha' }) }),
    )
    expect(await screen.findByText(text(zhCN.permissions.grants.forRole, { role: 'alpha' }))).toBeInTheDocument()

    await act(async () => {
      removal.resolve()
    })
    await waitForCondition(() =>
      expect(screen.queryByText(text(zhCN.permissions.roles.removed, { role: 'beta' }))).toBeInTheDocument(),
    )
    expect(screen.getByText(text(zhCN.permissions.grants.forRole, { role: 'alpha' }))).toBeInTheDocument()
  })

  it('closes the delete dialog on confirm and reports a refusal in the page region', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      removeRole: () => Promise.reject({ code: 'authz.forbidden' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'editor' }) }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: zhCN.permissions.roles.removeConfirm }))
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )

    // The dialog closes on the confirming click, so the refusal lands in the
    // visible region rather than behind an aria-hidden modal.
    await waitForCondition(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.forbidden,
    )
  })

  it('renders the retryable error placeholder when the roles read fails', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.permissions.roles.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.permissions.roles.error.description)).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the roles read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.permissions.roles.retry })).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it("reports each control group's failure in its own region", async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () => Promise.resolve([grant()]),
      writeRole: () => Promise.reject({ code: 'authz.bad_request' }),
      grantPermission: () => Promise.reject({ code: 'authz.conflict' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)

    await user.type(screen.getByLabelText(zhCN.permissions.roles.create.roleLabel), 'auditor')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }))
    expect(await within(section(zhCN.permissions.roles.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.bad_request,
    )

    await user.click(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }),
    )
    await within(section(zhCN.permissions.grants.title)).findByText('crm.deal.read')
    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))
    expect(await within(section(zhCN.permissions.grants.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.conflict,
    )

    expect(within(section(zhCN.permissions.roles.title)).getByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.bad_request,
    )
    expect(
      within(section(zhCN.permissions.roles.title)).queryByText(zhCN.errors.authz.conflict),
    ).toBeNull()
    expect(
      within(section(zhCN.permissions.grants.title)).queryByText(
        zhCN.errors.authz.bad_request,
      ),
    ).toBeNull()
    expect(screen.getAllByRole('alert')).toHaveLength(2)
  })

  it('marks the refused grant row inline and brings its region into view', async () => {
    mockMatchMedia(false)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () =>
        Promise.resolve([
          grant(),
          grant({
            permission: 'crm.deal.write',
            resource: 'deal',
            action: 'write',
          }),
        ]),
      revokePermission: () => Promise.reject({ code: 'authz.forbidden' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }),
    )
    const grants = section(zhCN.permissions.grants.title)
    const failed = await within(grants).findByRole('button', {
      name: text(zhCN.permissions.grants.revokeAria, { permission: 'crm.deal.read' }),
    })
    const other = within(grants).getByRole('button', {
      name: text(zhCN.permissions.grants.revokeAria, { permission: 'crm.deal.write' }),
    })
    await user.click(failed)

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.forbidden,
    )
    expect(failed).toHaveAttribute('aria-invalid', 'true')
    expect(other).toHaveAttribute('aria-invalid', 'false')
    const hintId = failed.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    const hint = document.getElementById(hintId as string)
    expect(hint?.textContent).toBe(screen.getByRole('alert').textContent)

    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    })
    await expectNoAxeViolations()
  })

  it('drops the refused grant row marker when the next grant starts', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants: () => Promise.resolve([grant()]),
      revokePermission: () => Promise.reject({ code: 'authz.forbidden' }),
      grantPermission: () => Promise.resolve(),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    const grants = section(zhCN.permissions.grants.title)
    const failed = await within(grants).findByRole('button', {
      name: text(zhCN.permissions.grants.revokeAria, {
        permission: 'crm.deal.read',
      }),
    })
    await user.click(failed)
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.forbidden,
    )
    expect(failed).toHaveAttribute('aria-invalid', 'true')

    await user.click(
      screen.getByLabelText(zhCN.permissions.grants.permissionLabel),
    )
    await user.click(
      await screen.findByRole('option', { name: 'crm.deal.write' }),
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.grants.add }),
    )
    expect(
      await screen.findByText(
        text(zhCN.permissions.grants.granted, {
          role: 'editor',
          permission: 'crm.deal.write',
        }),
      ),
    ).toBeInTheDocument()
    expect(failed).toHaveAttribute('aria-invalid', 'false')
  })

  it('keeps a refused lookup read in the permission placeholder, not a write error', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listAccountBindings: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.lookup.apply }))

    const lookup = section(zhCN.permissions.lookup.title)
    expect(await within(lookup).findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(within(lookup).queryByRole('button', { name: zhCN.permissions.lookup.retry })).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('disables the controls a pending grant drives, and clears the grant result when the role changes', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role(), role({ role: 'viewer' })]),
      grantPermission: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }),
    )
    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))

    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'viewer' }) }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByLabelText(zhCN.common.tenant)).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText(text(zhCN.permissions.grants.granted, { role: 'editor', permission: 'crm.deal.write' })),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'viewer' }) }),
    )
    expect(
      screen.queryByText(text(zhCN.permissions.grants.granted, { role: 'editor', permission: 'crm.deal.write' })),
    ).toBeNull()
  })

  it('disables the account picker while the bind is in flight and clears the bind result when the account changes', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withAuthz({
      bindRole: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }), 'viewer')
    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.bindings.bind }))

    expect(screen.getByLabelText(zhCN.permissions.bindings.accountLabel)).toBeDisabled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText(
        text(zhCN.permissions.bindings.bound, {
          role: 'viewer',
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      ),
    ).toBeInTheDocument()

    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Operator', 'Operator')
    expect(
      screen.queryByText(
        text(zhCN.permissions.bindings.bound, {
          role: 'viewer',
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      ),
    ).toBeNull()
  })

  it('keeps the bind control focusable and inert while its precondition is unmet and its write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const bound: string[] = []
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      bindRole: (input) => {
        bound.push(`${input.account}:${input.role}`)
        return pending.promise
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')

    const bind = screen.getByRole('button', {
      name: zhCN.permissions.bindings.bind,
    })
    // The control is unavailable with a reason, not natively disabled, so it can
    // hold focus and says why it cannot run.
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveAccessibleDescription(
      zhCN.permissions.bindings.accountRequired,
    )
    bind.focus()
    expect(bind).toHaveFocus()
    fireEvent.click(bind)
    await act(async () => {
      await Promise.resolve()
    })
    expect(bound).toEqual([])

    await user.type(
      screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }),
      'viewer',
    )
    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(bind)
    expect(bind).toHaveAttribute('aria-busy', 'true')
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).toHaveFocus()
    // The role field is read-only, not natively disabled, while the write runs.
    expect(
      screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }),
    ).toHaveAttribute('readonly')
    fireEvent.click(bind)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await act(async () => {
      await Promise.resolve()
    })
    expect(bound).toEqual(['acc-2:viewer'])

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(bind).toHaveFocus()
  })

  const NOTICE_IDENTIFIERS: AdminAccount['identifiers'] = [
    { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
  ]

  async function driveBindingNotice(
    user: ReturnType<typeof userEvent.setup>,
    action: 'bound' | 'unbound',
    displayName: string,
    identifiers: AdminAccount['identifiers'],
    expected: string,
  ): Promise<HTMLElement> {
    if (action === 'bound') {
      const operations = withAuthz({
        listAccounts: () =>
          Promise.resolve({
            accounts: [
              account({ id: 'acc-2', display_name: displayName, identifiers }),
            ],
            next_cursor: '',
          }),
        bindRole: () => Promise.resolve(),
      })
      renderWithProviders(<PermissionsPage />, { operations })
      await pickTenant(user)
      await user.type(
        screen.getByRole('textbox', {
          name: zhCN.permissions.bindings.roleLabel,
        }),
        'viewer',
      )
      const username =
        identifiers.find((identifier) => identifier.kind === 'username')
          ?.value ?? ''
      const optionName =
        displayName === '' && username === ''
          ? zhCN.common.accountUnavailable
          : displayName === ''
            ? username
            : username === ''
              ? displayName
              : `${displayName} (${username})`
      await chooseAccount(
        user,
        zhCN.permissions.bindings.accountLabel,
        'Ada',
        optionName,
      )
      await user.click(
        screen.getByRole('button', { name: zhCN.permissions.bindings.bind }),
      )
      return screen.findByText(expected)
    }

    const getAccount = vi.fn(() =>
      Promise.resolve(
        account({
          id: 'acc-1',
          display_name: displayName,
          identifiers,
        }),
      ),
    )
    const operations = withAuthz({
      listBindings: () => Promise.resolve([binding()]),
      getAccount,
      unbindRole: () => Promise.resolve(),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await waitForCondition(() => expect(getAccount).toHaveBeenCalled())
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    const username =
      identifiers.find((identifier) => identifier.kind === 'username')
        ?.value ?? ''
    const composed =
      displayName === '' && username === ''
        ? zhCN.common.accountUnavailable
        : displayName === ''
          ? username
          : username === ''
            ? displayName
            : `${displayName} (${username})`
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.bindings.unbindAria, {
          account: composed,
          role: 'editor',
        }),
      }),
    )
    return screen.findByText(expected)
  }

  describe.each([
    {
      action: 'bound' as const,
      named: zhCN.permissions.bindings.bound,
      unnamed: zhCN.permissions.bindings.boundUnnamed,
      role: 'viewer',
    },
    {
      action: 'unbound' as const,
      named: zhCN.permissions.bindings.unbound,
      unnamed: zhCN.permissions.bindings.unboundUnnamed,
      role: 'editor',
    },
  ])('bindings notice: $action', ({ action, named, unnamed, role }) => {
    it('names the account by display name and first identifier, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveBindingNotice(
        user,
        action,
        'Ada',
        NOTICE_IDENTIFIERS,
        text(named, {
          role,
          account: byNameAndIdentifier('Ada', 'ada'),
        }),
      )
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })

    it('names the account by its first identifier when the display name is empty, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveBindingNotice(
        user,
        action,
        '',
        NOTICE_IDENTIFIERS,
        text(named, { role, account: byIdentifier('ada') }),
      )
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })

    it('uses the neutral sentence when neither resolves, never the id', async () => {
      const user = userEvent.setup()
      const notice = await driveBindingNotice(
        user,
        action,
        '',
        [],
        text(unnamed, { role }),
      )
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-2')
    })
  })

  it('re-reads every enabled body read once on refresh and skips the disabled ones', async () => {
    const user = userEvent.setup()
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const listPermissions = vi.fn(() =>
      Promise.resolve([permission('crm.deal.read')]),
    )
    const listBindings = vi.fn(() => Promise.resolve([binding()]))
    const listGrants = vi.fn(() => Promise.resolve([grant()]))
    const listAccountBindings = vi.fn(() => Promise.resolve([binding()]))
    const operations = withAuthz({
      listRoles,
      listPermissions,
      listBindings,
      listGrants,
      listAccountBindings,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await within(section(zhCN.permissions.roles.title)).findByText('editor')
    await screen.findByText('crm.deal.read')

    expect(listGrants).not.toHaveBeenCalled()
    expect(listAccountBindings).not.toHaveBeenCalled()
    const rolesBefore = listRoles.mock.calls.length
    const catalogBefore = listPermissions.mock.calls.length
    const bindingsBefore = listBindings.mock.calls.length

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    await waitFor(() =>
      expect(listRoles.mock.calls.length).toBe(rolesBefore + 1),
    )
    await waitFor(() =>
      expect(listPermissions.mock.calls.length).toBe(catalogBefore + 1),
    )
    await waitFor(() =>
      expect(listBindings.mock.calls.length).toBe(bindingsBefore + 1),
    )
    expect(listRoles.mock.calls.length).toBe(rolesBefore + 1)
    expect(listPermissions.mock.calls.length).toBe(catalogBefore + 1)
    expect(listBindings.mock.calls.length).toBe(bindingsBefore + 1)
    expect(listGrants).not.toHaveBeenCalled()
    expect(listAccountBindings).not.toHaveBeenCalled()
  })

  it('re-reads the selected role grants and the applied account bindings on refresh', async () => {
    const user = userEvent.setup()
    const listGrants = vi.fn(() => Promise.resolve([grant()]))
    const listAccountBindings = vi.fn(() => Promise.resolve([binding()]))
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
      listGrants,
      listAccountBindings,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: 'editor' }),
    )
    await chooseAccount(
      user,
      zhCN.permissions.lookup.accountLabel,
      'Ada',
      'Ada (ada)',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.permissions.lookup.apply }),
    )
    await waitFor(() =>
      expect(listAccountBindings).toHaveBeenCalledTimes(1),
    )
    const grantsBefore = listGrants.mock.calls.length
    const lookupBefore = listAccountBindings.mock.calls.length

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    await waitFor(() =>
      expect(listGrants.mock.calls.length).toBe(grantsBefore + 1),
    )
    await waitFor(() =>
      expect(listAccountBindings.mock.calls.length).toBe(lookupBefore + 1),
    )
  })

  it('resets the page scopes and row markers before re-reading on refresh', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')

    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      'auditor',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.permissions.roles.create.submit,
      }),
    )
    const saved = text(zhCN.permissions.roles.saved, { role: 'auditor' })
    expect(await screen.findByText(saved)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() => {
      expect(screen.queryByText(saved)).toBeNull()
    })
  })

  it('resets the page scopes when a controlled tenant changes', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role()]),
    })
    function Harness(): ReactElement {
      const [tenant, setTenant] = useState('acme')
      return (
        <>
          <button type="button" onClick={() => setTenant('globex')}>
            switch-tenant
          </button>
          <PermissionsPage tenant={tenant} />
        </>
      )
    }
    renderWithProviders(<Harness />, { operations })
    await screen.findByText('editor')

    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      'auditor',
    )
    await user.click(
      screen.getByRole('button', {
        name: zhCN.permissions.roles.create.submit,
      }),
    )
    const saved = text(zhCN.permissions.roles.saved, { role: 'auditor' })
    expect(await screen.findByText(saved)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'switch-tenant' }))
    await waitFor(() => {
      expect(screen.queryByText(saved)).toBeNull()
    })
  })

  it('clears a selected role the refresh no longer lists and announces it', async () => {
    const user = userEvent.setup()
    let listed: AdminRole[] = [role()]
    const listRoles = vi.fn(() => Promise.resolve(listed))
    const operations = withAuthz({ listRoles })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    expect(
      await screen.findByText(
        text(zhCN.permissions.grants.forRole, { role: 'editor' }),
      ),
    ).toBeInTheDocument()

    listed = []
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    const announcement = await screen.findByText(
      zhCN.permissions.roles.selectionCleared,
    )
    expect(announcement).toHaveAttribute('role', 'status')
    expect(
      screen.getByText(zhCN.permissions.grants.pickRole),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(zhCN.permissions.roles.empty.title),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() => {
      expect(
        screen.queryByText(zhCN.permissions.roles.selectionCleared),
      ).toBeNull()
    })
  })

  it('keeps the selected role when the refresh roles read fails', async () => {
    const user = userEvent.setup()
    let fail = false
    const listRoles = vi.fn(() =>
      fail
        ? Promise.reject({ status: 500, code: 'client.http.500' })
        : Promise.resolve([role()]),
    )
    const operations = withAuthz({ listRoles })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }),
      }),
    )
    await screen.findByText(
      text(zhCN.permissions.grants.forRole, { role: 'editor' }),
    )

    fail = true
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })
    expect(
      screen.getByText(text(zhCN.permissions.grants.forRole, { role: 'editor' })),
    ).toBeInTheDocument()
    expect(
      screen.queryByText(zhCN.permissions.roles.selectionCleared),
    ).toBeNull()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role(), role({ role: 'platform-admin', builtin: true })]),
      listBindings: () => Promise.resolve([binding({ role: 'viewer' })]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await screen.findByText('platform-admin')
    await expectNoAxeViolations()
  })
})

async function confirmRoleRemoval(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.click(
    within(dialog).getByRole('button', {
      name: zhCN.permissions.roles.removeConfirm,
    }),
  )
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
}

function roleRemoveButton(name: string): HTMLElement {
  return screen.getByRole('button', {
    name: text(zhCN.permissions.roles.removeAria, { role: name }),
  })
}

function grantRevokeButton(code: string): HTMLElement {
  return screen.getByRole('button', {
    name: text(zhCN.permissions.grants.revokeAria, { permission: code }),
  })
}

function expectSiblingUnavailable(control: HTMLElement): void {
  expect(control).toHaveAttribute('aria-disabled', 'true')
  expect(control).not.toHaveAttribute('aria-busy')
  expect(control).not.toHaveAttribute('aria-describedby')
}

describe('PermissionsPage write attribution', () => {
  it('marks only the create submit busy while a role is created, the remove rows unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const removeRole = vi.fn(() => Promise.resolve())
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role({ role: 'alpha' }), role({ role: 'beta' })]),
      writeRole: () => pending.promise,
      removeRole,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('alpha')

    await user.type(
      screen.getByLabelText(zhCN.permissions.roles.create.roleLabel),
      'reviewer',
    )
    const create = screen.getByRole('button', {
      name: zhCN.permissions.roles.create.submit,
    })
    await user.click(create)

    expect(create).toHaveAttribute('aria-busy', 'true')
    for (const button of [roleRemoveButton('alpha'), roleRemoveButton('beta')]) {
      expectSiblingUnavailable(button)
    }
    const alpha = roleRemoveButton('alpha')
    fireEvent.click(alpha)
    alpha.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await act(async () => {
      await Promise.resolve()
    })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(removeRole).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the deleting row busy, the create submit and the other rows unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const removeRole = vi.fn(() => pending.promise)
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role({ role: 'alpha' }), role({ role: 'beta' })]),
      writeRole: () => Promise.resolve(),
      removeRole,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('alpha')

    await user.click(roleRemoveButton('alpha'))
    await confirmRoleRemoval(user)

    const alpha = roleRemoveButton('alpha')
    const beta = roleRemoveButton('beta')
    expect(alpha).toHaveAttribute('aria-busy', 'true')
    expectSiblingUnavailable(beta)
    const create = screen.getByRole('button', {
      name: zhCN.permissions.roles.create.submit,
    })
    expect(create).toHaveAttribute('aria-disabled', 'true')
    expect(create).not.toHaveAttribute('aria-busy')

    fireEvent.click(beta)
    await act(async () => {
      await Promise.resolve()
    })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(removeRole).toHaveBeenCalledTimes(1)

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the activated grant control busy and its grant sibling unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const revokePermission = vi.fn(() => Promise.resolve())
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
      listGrants: () =>
        Promise.resolve([
          grant({ role: 'alpha', permission: 'crm.deal.read', resource: 'deal', action: 'read' }),
          grant({ role: 'alpha', permission: 'crm.deal.write', resource: 'deal', action: 'write' }),
        ]),
      grantPermission: () => pending.promise,
      revokePermission,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'alpha' }),
      }),
    )
    const grantsSection = section(zhCN.permissions.grants.title)
    await within(grantsSection).findByText('crm.deal.read')

    await user.click(
      screen.getByLabelText(zhCN.permissions.grants.permissionLabel),
    )
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    const add = screen.getByRole('button', { name: zhCN.permissions.grants.add })
    await user.click(add)

    expect(add).toHaveAttribute('aria-busy', 'true')
    const readRevoke = grantRevokeButton('crm.deal.read')
    expectSiblingUnavailable(readRevoke)
    fireEvent.click(readRevoke)
    readRevoke.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await act(async () => {
      await Promise.resolve()
    })
    expect(revokePermission).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the revoking row busy and the grant control unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const grantPermission = vi.fn(() => Promise.resolve())
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
      listGrants: () =>
        Promise.resolve([
          grant({ role: 'alpha', permission: 'crm.deal.read', resource: 'deal', action: 'read' }),
          grant({ role: 'alpha', permission: 'crm.deal.write', resource: 'deal', action: 'write' }),
        ]),
      revokePermission: () => pending.promise,
      grantPermission,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'alpha' }),
      }),
    )
    await within(section(zhCN.permissions.grants.title)).findByText(
      'crm.deal.read',
    )

    await user.click(grantRevokeButton('crm.deal.read'))

    expect(grantRevokeButton('crm.deal.read')).toHaveAttribute('aria-busy', 'true')
    expectSiblingUnavailable(grantRevokeButton('crm.deal.write'))
    const add = screen.getByRole('button', { name: zhCN.permissions.grants.add })
    expect(add).toHaveAttribute('aria-disabled', 'true')
    expect(add).not.toHaveAttribute('aria-busy')
    fireEvent.click(add)
    await act(async () => {
      await Promise.resolve()
    })
    expect(grantPermission).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the activated binding control busy and the other binding controls unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const unbindRole = vi.fn(() => Promise.resolve())
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
      listBindings: () =>
        Promise.resolve([
          binding({ account: 'acc-1', role: 'editor' }),
          binding({ account: 'acc-1', role: 'viewer' }),
        ]),
      bindRole: () => pending.promise,
      unbindRole,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('viewer')

    await user.type(
      screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }),
      'viewer',
    )
    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Ada', 'Ada (ada)')
    const bind = screen.getByRole('button', { name: zhCN.permissions.bindings.bind })
    await user.click(bind)

    expect(bind).toHaveAttribute('aria-busy', 'true')
    const bindingsSection = section(zhCN.permissions.bindings.title)
    const unbinds = within(bindingsSection).getAllByRole('button', {
      name: new RegExp(zhCN.permissions.bindings.unbind),
    })
    expect(unbinds).toHaveLength(2)
    for (const control of unbinds) {
      expectSiblingUnavailable(control)
    }
    fireEvent.click(unbinds[0]!)
    unbinds[0]!.focus()
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await act(async () => {
      await Promise.resolve()
    })
    expect(unbindRole).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the unbinding row busy and the bind submit unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const bindRole = vi.fn(() => Promise.resolve())
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
      listBindings: () =>
        Promise.resolve([
          binding({ account: 'acc-1', role: 'editor' }),
          binding({ account: 'acc-1', role: 'viewer' }),
        ]),
      unbindRole: () => pending.promise,
      bindRole,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    const bindingsSection = section(zhCN.permissions.bindings.title)
    const first = await within(bindingsSection).findByRole('button', {
      name: text(zhCN.permissions.bindings.unbindAria, {
        account: 'Operator',
        role: 'editor',
      }),
    })
    await user.click(first)

    expect(first).toHaveAttribute('aria-busy', 'true')
    const bind = within(bindingsSection).getByRole('button', {
      name: zhCN.permissions.bindings.bind,
    })
    expect(bind).toHaveAttribute('aria-disabled', 'true')
    expect(bind).not.toHaveAttribute('aria-busy')
    fireEvent.click(bind)
    await act(async () => {
      await Promise.resolve()
    })
    expect(bindRole).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('marks only the activated lookup unbind row busy and the apply control unavailable', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const operations = withAuthz({
      listAccountBindings: () =>
        Promise.resolve([
          binding({ account: 'acc-1', role: 'editor' }),
          binding({ account: 'acc-1', role: 'viewer' }),
        ]),
      unbindRole: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    const lookup = section(zhCN.permissions.lookup.title)
    await user.click(within(lookup).getByRole('button', { name: zhCN.permissions.lookup.apply }))
    const first = await within(lookup).findByRole('button', {
      name: text(zhCN.permissions.bindings.unbindAria, {
        account: 'Operator',
        role: 'editor',
      }),
    })
    await user.click(first)

    expect(first).toHaveAttribute('aria-busy', 'true')
    const second = within(lookup).getByRole('button', {
      name: text(zhCN.permissions.bindings.unbindAria, {
        account: 'Operator',
        role: 'viewer',
      }),
    })
    expectSiblingUnavailable(second)
    const apply = within(lookup).getByRole('button', {
      name: zhCN.permissions.lookup.apply,
    })
    expect(apply).toHaveAttribute('aria-disabled', 'true')
    expect(apply).not.toHaveAttribute('aria-busy')

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
  })

  it('names the reason on each unavailable form control', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'alpha' }),
      }),
    )

    expect(
      screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }),
    ).toHaveAccessibleDescription(zhCN.permissions.roles.create.roleRequired)
    expect(
      screen.getByRole('button', { name: zhCN.permissions.grants.add }),
    ).toHaveAccessibleDescription(zhCN.permissions.grants.permissionRequired)
    expect(
      screen.getByRole('button', { name: zhCN.permissions.bindings.bind }),
    ).toHaveAccessibleDescription(zhCN.permissions.bindings.accountRequired)
    expect(
      screen.getByRole('button', { name: zhCN.permissions.lookup.apply }),
    ).toHaveAccessibleDescription(zhCN.permissions.lookup.accountRequired)
  })
})

describe('PermissionsPage unload focus', () => {
  it("moves focus to the next role's remove control after a delete lands", async () => {
    const user = userEvent.setup()
    let rows = [role({ role: 'alpha' }), role({ role: 'beta' })]
    const operations = withAuthz({
      listRoles: () => Promise.resolve([...rows]),
      removeRole: (input) => {
        rows = rows.filter((row) => row.role !== input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('beta')

    await user.click(roleRemoveButton('alpha'))
    await confirmRoleRemoval(user)

    await waitFor(() =>
      expect(screen.queryByText('alpha')).toBeNull(),
    )
    await waitForCondition(() => expect(roleRemoveButton('beta')).toHaveFocus())
  })

  it('moves focus to the roles heading when the last role is removed', async () => {
    const user = userEvent.setup()
    let rows = [role({ role: 'alpha' })]
    const operations = withAuthz({
      listRoles: () => Promise.resolve([...rows]),
      removeRole: (input) => {
        rows = rows.filter((row) => row.role !== input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('alpha')

    await user.click(roleRemoveButton('alpha'))
    await confirmRoleRemoval(user)

    await waitFor(() => expect(screen.queryByText('alpha')).toBeNull())
    await waitForCondition(() =>
      expect(
        screen.getByRole('heading', { name: zhCN.permissions.roles.title }),
      ).toHaveFocus(),
    )
  })

  it("moves focus to the next grant's revoke control after a revoke lands", async () => {
    const user = userEvent.setup()
    let rows = [
      grant({ role: 'alpha', permission: 'crm.deal.read', resource: 'deal', action: 'read' }),
      grant({ role: 'alpha', permission: 'crm.deal.write', resource: 'deal', action: 'write' }),
    ]
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
      listGrants: () => Promise.resolve([...rows]),
      revokePermission: (input) => {
        rows = rows.filter((row) => row.permission !== input.permission)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'alpha' }),
      }),
    )
    const grantsSection = section(zhCN.permissions.grants.title)
    await within(grantsSection).findByText('crm.deal.read')

    await user.click(grantRevokeButton('crm.deal.read'))
    await waitFor(() =>
      expect(within(grantsSection).queryByText('crm.deal.read')).toBeNull(),
    )
    await waitForCondition(() =>
      expect(grantRevokeButton('crm.deal.write')).toHaveFocus(),
    )
  })

  it("moves focus to the next binding's unbind control after an unbind lands", async () => {
    const user = userEvent.setup()
    let rows = [
      binding({ account: 'acc-1', role: 'editor' }),
      binding({ account: 'acc-1', role: 'viewer' }),
    ]
    const operations = withAuthz({
      listBindings: () => Promise.resolve([...rows]),
      unbindRole: (input) => {
        rows = rows.filter((row) => row.role !== input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    const bindingsSection = section(zhCN.permissions.bindings.title)
    const first = await within(bindingsSection).findByRole('button', {
      name: text(zhCN.permissions.bindings.unbindAria, {
        account: 'Operator',
        role: 'editor',
      }),
    })
    await user.click(first)

    await waitFor(() =>
      expect(
        within(bindingsSection).queryByRole('button', {
          name: text(zhCN.permissions.bindings.unbindAria, {
            account: 'Operator',
            role: 'editor',
          }),
        }),
      ).toBeNull(),
    )
    await waitForCondition(() =>
      expect(
        within(bindingsSection).getByRole('button', {
          name: text(zhCN.permissions.bindings.unbindAria, {
            account: 'Operator',
            role: 'viewer',
          }),
        }),
      ).toHaveFocus(),
    )
  })

  it("moves focus to the next lookup unbind control after an unbind lands", async () => {
    const user = userEvent.setup()
    let rows = [
      binding({ account: 'acc-2', role: 'editor' }),
      binding({ account: 'acc-2', role: 'viewer' }),
    ]
    const operations = withAuthz({
      getAccount: (query) =>
        Promise.resolve(
          query.id === 'acc-2'
            ? account({
                id: 'acc-2',
                display_name: 'Ada',
                identifiers: [
                  {
                    kind: 'username',
                    value: 'ada',
                    proven: false,
                    sign_in: true,
                    contact: false,
                    default_contact: false,
                  },
                ],
              })
            : account({ id: query.id ?? 'acc-1' }),
        ),
      listAccountBindings: () => Promise.resolve([...rows]),
      unbindRole: (input) => {
        rows = rows.filter((row) => row.role !== input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    const lookup = section(zhCN.permissions.lookup.title)
    await user.click(
      within(lookup).getByRole('button', { name: zhCN.permissions.lookup.apply }),
    )
    const first = await within(lookup).findByRole('button', {
      name: text(zhCN.permissions.bindings.unbindAria, {
        account: 'Ada (ada)',
        role: 'editor',
      }),
    })
    await user.click(first)

    await waitFor(() =>
      expect(
        within(lookup).queryByRole('button', {
          name: text(zhCN.permissions.bindings.unbindAria, {
            account: 'Ada (ada)',
            role: 'editor',
          }),
        }),
      ).toBeNull(),
    )
    await waitForCondition(() =>
      expect(
        within(lookup).getByRole('button', {
          name: text(zhCN.permissions.bindings.unbindAria, {
            account: 'Ada (ada)',
            role: 'viewer',
          }),
        }),
      ).toHaveFocus(),
    )
  })

  it('does not take focus back when the reader moved it before a removal lands', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    let rows = [
      grant({ role: 'alpha', permission: 'crm.deal.read', resource: 'deal', action: 'read' }),
      grant({ role: 'alpha', permission: 'crm.deal.write', resource: 'deal', action: 'write' }),
    ]
    const operations = withAuthz({
      listRoles: () => Promise.resolve([role({ role: 'alpha' })]),
      listGrants: () => Promise.resolve([...rows]),
      revokePermission: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'alpha' }),
      }),
    )
    const grantsSection = section(zhCN.permissions.grants.title)
    await within(grantsSection).findByText('crm.deal.read')

    await user.click(grantRevokeButton('crm.deal.read'))
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    refresh.focus()
    expect(refresh).toHaveFocus()

    rows = rows.filter((row) => row.permission !== 'crm.deal.read')
    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    await waitFor(() =>
      expect(within(grantsSection).queryByText('crm.deal.read')).toBeNull(),
    )
    expect(refresh).toHaveFocus()
  })
})
