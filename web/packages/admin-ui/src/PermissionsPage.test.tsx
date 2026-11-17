/**
 * PermissionsPage: the authorization-administration page.
 *
 * It prompts for a tenant until one is chosen, then renders a tenant's
 * roles, the permission catalog and the bindings, and drives the role
 * create / delete, the grant / revoke, the bind / unbind and the
 * account-binding lookup through the bundle. A builtin role's grants open
 * read-only: its list is visible and every write control is withheld.
 */

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

/** Fills a locale template's `{{name}}` placeholders. */
function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

/** A matcher for a label that carries a locale string inside other text. */
function contains(value: string): RegExp {
  return new RegExp(value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
}

/** The account reference a notice inserts for a resolved display name. */
function byName(name: string): string {
  return text(zhCN.common.accountReference.name, { name })
}

/** The account reference a notice inserts for an identifier alone. */
function byIdentifier(identifier: string): string {
  return text(zhCN.common.accountReference.identifier, { identifier })
}

/** The account reference a notice inserts when both resolve. */
function byNameAndIdentifier(name: string, identifier: string): string {
  return text(zhCN.common.accountReference.nameWithIdentifier, {
    name,
    identifier,
  })
}

const TENANT = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

/** The zh-CN label the confirm button shows once the two-step guard arms. */
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

/** A searchable account row: a display name beside an opaque id. */
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

/** The titled section a heading opens, which holds its group's region. */
function section(name: string): HTMLElement {
  const element = screen.getByRole('heading', { name }).closest('section')
  if (element === null) {
    throw new Error(`no section headed ${name}`)
  }
  return element as HTMLElement
}

/**
 * jsdom implements no layout, so `scrollIntoView` does not exist on
 * elements. A test that observes a region's scroll installs this spy.
 */
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

/**
 * Asserts the header refresh is inert while a write is in flight: disabled
 * without the empty-scope explanation, and a forced activation issues no
 * roles read.
 */
async function expectRefreshInert(
  listRoles: ReturnType<typeof vi.fn>,
  readsBefore: number,
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
  await waitForCondition(() => {
    expect(refresh).toBeDisabled()
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(listRoles).toHaveBeenCalledTimes(readsBefore)
}

/** A promise the test settles by hand, for observing an in-flight write. */
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
      // The catalog carries permission CODES (the form the grant routes
      // name a permission by); a grant row carries the resource:action pair
      // the engine matches on, and the page maps it back to its code for
      // the row label and the revoke.
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

    // The roles read is disabled while no tenant is chosen; the refresh
    // control follows it instead of issuing the read with an empty tenant.
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose a tenant
    // first" text.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      zhCN.permissions.needTenant.description,
    )
    // A programmatic click, because user-event refuses a disabled target:
    // the control must stay inert even when reached from a script.
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
      expect(screen.getByRole('button', { name: zhCN.common.refresh })).not.toBeDisabled()
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
    expect(submit).toBeDisabled()

    // Whitespace alone is not a name.
    await user.type(name, '   ')
    expect(submit).toBeDisabled()
    await user.clear(name)
    await user.type(name, 'auditor')
    await user.type(description, 'reads audit records')
    expect(submit).toBeEnabled()

    await user.click(submit)
    expect(
      await screen.findByText(
        text(zhCN.permissions.roles.saved, { role: 'auditor' }),
      ),
    ).toBeInTheDocument()
    // A landed create returns both fields to their initial values.
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
      // The link that carried the tenant arrives with a selection already
      // made: the roles read runs for it and the selector shows its name.
      expect(await screen.findByText('editor')).toBeInTheDocument()
      expect(screen.queryByText(zhCN.permissions.needTenant.title)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toHaveTextContent('Acme')
      expect(listRoles).toHaveBeenCalledWith('acme')
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('locks the scope to a controlled tenant and hides the selector', async () => {
    // The URL names a different tenant: a controlled page ignores it.
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
    // The binding's account column shows the readable identity the account
    // read answers, not the recorded id.
    expect(screen.getByText('Operator')).toBeInTheDocument()
  })

  it("keeps a read-only grants entry on a builtin role and drops its write controls", async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      // The two rows differ in the flag alone, so the case distinguishes the
      // gate from "no role has write controls": builtin true drops both write
      // controls and keeps the read-only entry, builtin false keeps them.
      listRoles: () =>
        Promise.resolve([
          role({ role: 'platform-admin', builtin: true }),
          role({ role: 'editor', builtin: false }),
        ]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('platform-admin')).toBeInTheDocument()
    // The reason is visible text, not a colour or a tooltip on a disabled
    // control: a disabled button cannot be focused and drops out of the Tab
    // order, so a tooltip or aria-describedby that depends on focus or hover is
    // unreachable to keyboard-only users. The control is still in the
    // accessibility tree; visible text sits in every reader's order.
    expect(screen.getByText(zhCN.permissions.roles.builtinLocked)).toBeInTheDocument()
    // The server refuses writes on a builtin role, not reads: the row keeps
    // an entry that opens the grants it holds.
    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.viewGrantsAria, { role: 'platform-admin' }) }),
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'platform-admin' }) }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'platform-admin' }) }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'editor' }) }),
    ).toBeEnabled()
    expect(screen.getByRole('button', { name: text(zhCN.permissions.roles.removeAria, { role: 'editor' }) })).toBeEnabled()
  })

  it('opens a builtin role grants read-only, with no grant or revoke control', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () =>
        Promise.resolve([role({ role: 'platform-admin', builtin: true })]),
      // The grant list must actually be loaded for the builtin role, or the
      // absence of the write controls would prove nothing.
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
    // The held grant is visible, spelled as the catalog spells it (the
    // catalog lists the same text, so the section scopes the assertion).
    const grantsSection = screen
      .getByRole('heading', { name: zhCN.permissions.grants.title })
      .closest('section') as HTMLElement
    expect(await within(grantsSection).findByText('crm.deal.read')).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.permissions.grants.builtinReadOnly),
    ).toBeInTheDocument()
    // ...and neither the grant form nor the revoke action is offered.
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
    // The grant row shows the catalog's permission code verbatim, so "what is
    // granted" and "what the catalog declares" are one vocabulary; a pair
    // spelling ("deal") would read as a different permission here.
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

    // The directory keeps the code as its primary text and renders the
    // server-rendered description as a second line in the same cell.
    const catalog = section(zhCN.permissions.catalog.title)
    const code = await within(catalog).findByText('crm.deal.read')
    const cell = code.closest('td')
    expect(cell).not.toBeNull()
    expect(within(cell as HTMLElement).getByText(described)).toBeInTheDocument()

    // The grant picker's options carry the same pair; the closed control
    // shows the chosen code alone, so the code stays primary there too.
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
    // The menu closes asynchronously, and until its listbox unmounts the
    // label addresses both it and the combobox: wait the listbox out and
    // name the combobox by role so the read cannot match two elements.
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

    // No second line: the code's own container holds the code and nothing
    // else, and neither the cell nor the option carries a caption element.
    const catalog = section(zhCN.permissions.catalog.title)
    const code = await within(catalog).findByText('crm.deal.read')
    const cell = code.closest('td')
    expect(cell).not.toBeNull()
    expect(code.parentElement?.children).toHaveLength(1)
    expect(
      (cell as HTMLElement).querySelector(`.${typographyClasses.caption}`),
    ).toBeNull()

    // The picker option is the bare code, and so is the closed control.
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
    // Same close transition as the described picker: the listbox still
    // carries the label while the menu closes, so wait it out and address
    // the combobox by role.
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
    // The readable description is the row's primary text; the permission
    // code the server reports stays beneath it.
    const cell = (await within(grants).findByText(described)).closest('td')
    expect(cell).not.toBeNull()
    expect(
      within(cell as HTMLElement).getByText('authz.tenant.role.manage'),
    ).toBeInTheDocument()
    // The badge is text, so the mark never rests on color alone.
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

    // A dangerous permission asks before it is written: the add click opens
    // the dialog and submits nothing.
    await user.click(screen.getByLabelText(zhCN.permissions.grants.permissionLabel))
    await user.click(
      await screen.findByRole('option', { name: contains('crm.deal.read') }),
    )
    await user.click(screen.getByRole('button', { name: zhCN.permissions.grants.add }))
    const dialog = await screen.findByRole('dialog')
    expect(granted).toEqual([])
    // The heavier two-step arm: the first confirming click only arms the
    // button, and the deliberate second click fires the write.
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

    // A permission the server does not mark dangerous submits at once.
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
    // The catalog is grouped by the declaring module's group, in the shared
    // order and with the shared headings.
    expect(
      await within(catalog).findByText(
        zhCN.permissions.catalog.group.authorization,
      ),
    ).toBeInTheDocument()
    expect(
      within(catalog).getByText(zhCN.permissions.catalog.group.membership),
    ).toBeInTheDocument()
    // An entry the caller cannot grant is still listed and marked.
    expect(
      await within(catalog).findByText('authn.member.read'),
    ).toBeInTheDocument()
    expect(
      within(catalog).getByText(zhCN.permissions.catalog.notGrantable),
    ).toBeInTheDocument()

    // The grant picker offers only what the caller may grant.
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
    // The grant revoke belongs to the selected role's grant list, which
    // opens with the manage action.
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
    // The form offers no builtin control: the mark freezes a role for good and
    // is the host's to set when it seeds the role, not the admin's.
    expect(screen.queryByRole('checkbox')).toBeNull()
    await user.type(screen.getByLabelText(zhCN.permissions.roles.create.roleLabel), 'auditor')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }))
    await waitForCondition(() => {
      expect(written).toEqual([
        { tenant: 'acme', role: 'auditor', description: '' },
      ])
    })
    // The landed create fills the role scope's success region, naming the
    // role it wrote.
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
      // Grant and revoke name the permission by its CODE, symmetrically:
      // the granted code is sent back on revoke through the catalog map.
      expect(granted).toEqual(['crm.deal.write'])
      expect(revoked).toEqual(['crm.deal.read'])
    })
    // The landed revoke fills the grants scope's success region, naming the
    // permission and the role it was revoked from.
    expect(
      await screen.findByText(text(zhCN.permissions.grants.revoked, { role: 'editor', permission: 'crm.deal.read' })),
    ).toBeInTheDocument()
  })

  it('revokes a grant whose resource is not the code segment', async () => {
    const user = userEvent.setup()
    const revoked: string[] = []
    const operations = withAuthz({
      // The catalog's code is authn.mfa.reset, whose translated pair is
      // factor/reset (pkg/authz's translation table is its own authority and
      // is not a suffix of the code); the grant row carries that pair. The
      // label and the revoke still name the code, resolved by the unique
      // action.
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
      // A pair this process no longer translates: the server reports no
      // permission code for it, so the row shows the recorded spelling and
      // the revoke route (which names a permission by its code) is withheld.
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
    expect(revoke).toBeDisabled()
    // The reason is visible text beneath the disabled control, not only a
    // hover affordance a keyboard user cannot reach.
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
    // A role alone is not a binding: the account must be chosen, and the
    // picker holds no id until then.
    await user.type(screen.getByRole('textbox', { name: zhCN.permissions.bindings.roleLabel }), 'viewer')
    expect(screen.getByRole('button', { name: zhCN.permissions.bindings.bind })).toBeDisabled()
    // A display name finds the account; the write carries its id ("acc-2"),
    // never the typed name.
    await chooseAccount(user, zhCN.permissions.bindings.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.bindings.bind }))
    await user.click(screen.getByRole('button', { name: text(zhCN.permissions.bindings.unbindAria, { account: 'Operator', role: 'editor' }) }))
    await waitForCondition(() => {
      expect(bound).toEqual(['acc-2:viewer'])
      expect(unbound).toEqual(['editor'])
    })
    // The landed unbind fills the bindings scope's success region, naming
    // the account and the role it took away.
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
    // The cell renders the neutral copy, labelled unresolved rather than
    // passed off as a resolved identity.
    const cell = await screen.findByTitle(zhCN.common.accountUnresolved)
    expect(cell).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(cell).not.toHaveTextContent('acc-1')
    // The unbind control names the account by the same neutral copy when no
    // readable identity was resolved.
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
    // The refused lookup renders the recorded identifier in full, which
    // otherwise sets the account column's width from its content. The
    // account is the row's identity, so it takes the shared primary cap,
    // which grows at md and counts the copy control in the budget.
    const accountMarker = await within(bindings).findByTitle(zhCN.common.accountUnresolved)
    const accountCell = accountMarker.closest('td')
    expect(containerMaxWidthOf(accountCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    // The role is a long-text column that is not the primary identity: a
    // flat cap that does not grow with the container.
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
    // No account chosen yet, so the lookup is not offered.
    expect(screen.getByRole('button', { name: zhCN.permissions.lookup.apply })).toBeDisabled()
    await chooseAccount(user, zhCN.permissions.lookup.accountLabel, 'Ada', 'Ada (ada)')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.lookup.apply }))
    // The read is addressed by the account id, not the typed name.
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
    // The retry's pending state withdraws the failure placeholder.
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
    // Deleting a role revokes its grants and bindings with it and cannot be
    // undone: the heavier two-step guard -- the first click only arms.
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
    // The dialog closes on the confirming click, and the landed delete
    // fills the role scope's success region after it closes.
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

    // The removal is confirmed while beta is the selected role.
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

    // While the deletion is in flight the operator moves to alpha. The
    // settlement must clear a selection only if it still names beta.
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

    // The dialog does not hold the write open: it closes on the confirming
    // click, so the refusal lands in the role scope's region where it is
    // readable rather than behind an aria-hidden modal. The same text marks
    // the failed row inline, so the region is addressed by its role.
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
    // A read's refusal never settles into a write scope's slot.
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

    // The role catalog refuses the create.
    await user.type(screen.getByLabelText(zhCN.permissions.roles.create.roleLabel), 'auditor')
    await user.click(screen.getByRole('button', { name: zhCN.permissions.roles.create.submit }))
    expect(await within(section(zhCN.permissions.roles.title)).findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.bad_request,
    )

    // The grants group refuses the grant.
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

    // Neither section carries the other's text, and no page-level banner
    // aggregates them: the two regions are the only alerts.
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

    // The failing row carries the marker; the row beside it does not.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.authz.forbidden,
    )
    expect(failed).toHaveAttribute('aria-invalid', 'true')
    expect(other).toHaveAttribute('aria-invalid', 'false')
    const hintId = failed.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    const hint = document.getElementById(hintId as string)
    expect(hint?.textContent).toBe(screen.getByRole('alert').textContent)

    // The region under the table was brought into view, animated.
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

    // The grant and the revoke share the grants scope: a new attempt starts
    // from a clean result, so the refused row's marker and its inline reason
    // do not outlive the result they described.
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
    // A read's refusal never settles into a write scope's slot.
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

    // The selected role drives the grants scope, and the tenant drives
    // every scope: both are out of reach while the grant is in flight.
    expect(
      screen.getByRole('button', { name: text(zhCN.permissions.roles.manageGrantsAria, { role: 'viewer' }) }),
    ).toBeDisabled()
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

    // Another role means another context: the old confirmation goes.
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

    // The chosen account drives the bindings scope: it is disabled while
    // the write it drives is in flight.
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

    // Choosing another account is another context: the confirmation goes.
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

  // Both binding writes name the account in the shared four shapes and never
  // by its opaque id.
  const NOTICE_IDENTIFIERS: AdminAccount['identifiers'] = [
    { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
  ]

  /** Drives one binding write with the given identity and returns its notice. */
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
    // The unbind control resolves the row's account through the shared unit;
    // wait for that read before the click captures the identity.
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
    // The bindings table also spells the role it binds, so the roles table
    // is the one the role row is read from.
    await within(section(zhCN.permissions.roles.title)).findByText('editor')
    await screen.findByText('crm.deal.read')

    // No role selected and no account applied: the grants read and the
    // account-binding read are disabled, so the refresh must not issue
    // them (they would run without their argument).
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

    // The refresh is a context change: the confirmation written under the
    // old table does not describe the re-read one.
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
    // A controlled page takes its tenant from the host, so the change
    // arrives as a prop; the host-owned switch stands in for the host
    // re-rendering the page under another tenant.
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

    // The tenant is an argument of every write: the confirmation written
    // under the tenant left behind does not describe the new one.
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

    // The role disappears elsewhere; the refresh's roles read answers
    // without it.
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

    // The next refresh clears the standing announcement.
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

    // A failed roles read keeps the selection; the stale marker reports
    // the failure and the disappearance announcement stays silent.
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
