/**
 * PermissionsPage: the authorization-administration page.
 *
 * It prompts for a tenant until one is chosen, then renders a tenant's
 * roles, the permission catalog and the bindings, and drives the role
 * create / delete, the grant / revoke, the bind / unbind and the
 * account-binding lookup through the bundle. A builtin role's grants open
 * read-only: its list is visible and every write control is withheld.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { PermissionsPage } from './PermissionsPage.js'
import type {
  ConsoleAccount,
  ConsoleBinding,
  ConsoleGrant,
  ConsolePermission,
  ConsoleRole,
  ConsoleWriteRoleInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { containerMaxWidthOf } from '../test-utils/container-visibility.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

/** The zh-CN label the confirm button shows once the two-step guard arms. */
const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

function role(overrides: Partial<ConsoleRole> = {}): ConsoleRole {
  return {
    tenant: 'acme',
    role: 'editor',
    builtin: false,
    description_key: 'role.editor',
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function grant(overrides: Partial<ConsoleGrant> = {}): ConsoleGrant {
  return {
    tenant: 'acme',
    role: 'editor',
    resource: 'deal',
    action: 'read',
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function permission(value: string): ConsolePermission {
  return { permission: value }
}

/** A searchable account row: a display name beside an opaque id. */
function account(overrides: Partial<ConsoleAccount> = {}): ConsoleAccount {
  return {
    id: 'acc-1',
    kind: 'user',
    status: 'active',
    display_name: 'Operator',
    mfa_required: false,
    protected: false,
    password_set: true,
    created_at: '2026-10-15T12:00:00Z',
    ...overrides,
  }
}

function binding(overrides: Partial<ConsoleBinding> = {}): ConsoleBinding {
  return {
    tenant: 'acme',
    account: 'acc-1',
    role: 'editor',
    granted_at: '2026-10-15T12:00:00Z',
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
  const refresh = screen.getByRole('button', { name: '刷新' })
  await vi.waitFor(() => {
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
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
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
            account({ id: 'acc-2', display_name: 'Ada' }),
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
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
  })

  it('does not read an empty tenant scope on refresh', async () => {
    const listRoles = vi.fn(() => Promise.resolve([role()]))
    const operations = withAuthz({ listRoles })
    renderWithProviders(<PermissionsPage />, { operations })

    // The roles read is disabled while no tenant is chosen; the refresh
    // control follows it instead of issuing the read with an empty tenant.
    const refresh = screen.getByRole('button', { name: '刷新' })
    expect(refresh).toBeDisabled()
    // The disabled control is described by the page's own "choose a tenant
    // first" text.
    const describedBy = refresh.getAttribute('aria-describedby')
    expect(describedBy).not.toBeNull()
    expect(document.getElementById(describedBy as string)).toHaveTextContent(
      '权限面的角色、授权与绑定都按租户存储，请先选择一个租户。',
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

    await user.type(screen.getByLabelText('角色名'), 'auditor')
    await user.click(screen.getByRole('button', { name: '保存角色' }))
    await expectRefreshInert(listRoles, reads)

    await act(async () => {
      pending.resolve()
    })
    await vi.waitFor(() => {
      expect(screen.getByRole('button', { name: '刷新' })).not.toBeDisabled()
    })
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
      screen.getByRole('button', { name: '管理授权（角色 editor）' }),
    )
    await user.click(screen.getByLabelText('权限'))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: '授予' }))
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

    await user.type(screen.getByLabelText('角色'), 'viewer')
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '绑定' }))
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

    await chooseAccount(user, '账户标识', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '查询' }))
    await user.click(
      await screen.findByRole('button', {
        name: '解绑账户 Operator 的角色 editor',
      }),
    )
    await expectRefreshInert(listRoles, reads)

    await act(async () => {
      pending.resolve()
    })
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/console/permissions?tenant=acme')
    try {
      const listRoles = vi.fn(() => Promise.resolve([role()]))
      const operations = withAuthz({ listRoles })
      renderWithProviders(<PermissionsPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: the roles read runs for it and the selector shows its alias.
      expect(await screen.findByText('editor')).toBeInTheDocument()
      expect(screen.queryByText('请选择租户')).toBeNull()
      expect(screen.getByLabelText('租户')).toHaveTextContent('Acme')
      expect(listRoles).toHaveBeenCalledWith('acme')
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
    expect(screen.getByText('内建角色不可修改或删除')).toBeInTheDocument()
    // The server refuses writes on a builtin role, not reads: the row keeps
    // an entry that opens the grants it holds.
    expect(
      screen.getByRole('button', { name: '查看授权（角色 platform-admin）' }),
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: '管理授权（角色 platform-admin）' }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', { name: '删除角色 platform-admin' }),
    ).toBeNull()
    expect(
      screen.getByRole('button', { name: '管理授权（角色 editor）' }),
    ).toBeEnabled()
    expect(screen.getByRole('button', { name: '删除角色 editor' })).toBeEnabled()
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
        name: '查看授权（角色 platform-admin）',
      }),
    )
    // The held grant is visible, spelled as the catalog spells it (the
    // catalog lists the same text, so the section scopes the assertion).
    const grantsSection = screen
      .getByRole('heading', { name: '授权' })
      .closest('section') as HTMLElement
    expect(await within(grantsSection).findByText('crm.deal.read')).toBeInTheDocument()
    expect(
      screen.getByText('该角色为内建角色，授权为只读：不能授予，也不能撤销。'),
    ).toBeInTheDocument()
    // ...and neither the grant form nor the revoke action is offered.
    expect(screen.queryByLabelText('权限')).toBeNull()
    expect(screen.queryByRole('button', { name: '授予' })).toBeNull()
    expect(
      screen.queryByRole('button', { name: '撤销授权 crm.deal.read' }),
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
    await user.click(screen.getByRole('button', { name: '管理授权（角色 editor）' }))
    const grantsSection = screen.getByRole('heading', { name: '授权' }).closest('section')
    const catalogSection = screen.getByRole('heading', { name: '权限目录' }).closest('section')
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
      name: '管理授权（角色 editor）',
    })
    const removeRole = screen.getByRole('button', { name: '删除角色 editor' })
    const unbind = screen.getByRole('button', {
      name: '解绑账户 Operator 的角色 viewer',
    })
    const refresh = screen.getByRole('button', { name: '刷新' })
    // The grant revoke belongs to the selected role's grant list, which
    // opens with the manage action.
    await user.click(manage)
    const revoke = await screen.findByRole('button', {
      name: '撤销授权 crm.deal.read',
    })
    for (const control of [manage, removeRole, unbind, refresh, revoke]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('creates a role through the bundle, without a builtin member', async () => {
    const user = userEvent.setup()
    const written: ConsoleWriteRoleInput[] = []
    const operations = withAuthz({
      writeRole: (input) => {
        written.push(input)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    // The form offers no builtin control: the mark freezes a role for good and
    // is the host's to set when it seeds the role, not the console's.
    expect(screen.queryByRole('checkbox', { name: '内建角色' })).toBeNull()
    await user.type(screen.getByLabelText('角色名'), 'auditor')
    await user.click(screen.getByRole('button', { name: '保存角色' }))
    await vi.waitFor(() => {
      expect(written).toEqual([
        { tenant: 'acme', role: 'auditor', descriptionKey: '' },
      ])
    })
    // The landed create fills the role scope's success region, naming the
    // role it wrote.
    expect(await screen.findByText('角色「auditor」已保存')).toBeInTheDocument()
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
    await user.click(screen.getByRole('button', { name: '管理授权（角色 editor）' }))
    const grantsSection = screen
      .getByRole('heading', { name: '授权' })
      .closest('section') as HTMLElement
    await within(grantsSection).findByText('crm.deal.read')
    await user.click(screen.getByLabelText('权限'))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: '授予' }))
    await user.click(screen.getByRole('button', { name: '撤销授权 crm.deal.read' }))
    await vi.waitFor(() => {
      // Grant and revoke name the permission by its CODE, symmetrically:
      // the granted code is sent back on revoke through the catalog map.
      expect(granted).toEqual(['crm.deal.write'])
      expect(revoked).toEqual(['crm.deal.read'])
    })
    // The landed revoke fills the grants scope's success region, naming the
    // permission and the role it was revoked from.
    expect(
      await screen.findByText('已从角色「editor」撤销权限 crm.deal.read'),
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
        Promise.resolve([grant({ resource: 'factor', action: 'reset' })]),
      revokePermission: (input) => {
        revoked.push(input.permission)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await user.click(screen.getByRole('button', { name: '管理授权（角色 editor）' }))
    const grantsSection = screen
      .getByRole('heading', { name: '授权' })
      .closest('section') as HTMLElement
    await within(grantsSection).findByText('authn.mfa.reset')
    await user.click(screen.getByRole('button', { name: '撤销授权 authn.mfa.reset' }))
    await vi.waitFor(() => {
      expect(revoked).toEqual(['authn.mfa.reset'])
    })
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
    await user.type(screen.getByLabelText('角色'), 'viewer')
    expect(screen.getByRole('button', { name: '绑定' })).toBeDisabled()
    // A display name finds the account; the write carries its id ("acc-2"),
    // never the typed name.
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '绑定' }))
    await user.click(screen.getByRole('button', { name: '解绑账户 Operator 的角色 editor' }))
    await vi.waitFor(() => {
      expect(bound).toEqual(['acc-2:viewer'])
      expect(unbound).toEqual(['editor'])
    })
    // The landed unbind fills the bindings scope's success region, naming
    // the account and the role it took away.
    expect(
      await screen.findByText('已解除账户「acc-1」的角色「editor」绑定'),
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
    // The cell keeps the recorded id, labelled unresolved rather than
    // passed off as a resolved identity.
    const cell = await screen.findByLabelText(/身份未解析/)
    expect(cell).toHaveTextContent('acc-1')
    // The unbind control names the account by the recorded id when no
    // readable name was resolved.
    expect(
      screen.getByRole('button', { name: '解绑账户 acc-1 的角色 viewer' }),
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

    const bindings = section('绑定')
    // The refused lookup renders the recorded identifier in full, which
    // otherwise sets the account column's width from its content. The
    // account is the row's identity, so it takes the shared primary cap,
    // which grows at md and counts the copy control in the budget.
    const accountMarker = await within(bindings).findByLabelText(/身份未解析/)
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
    expect(screen.getByRole('button', { name: '查询' })).toBeDisabled()
    await chooseAccount(user, '账户标识', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '查询' }))
    // The read is addressed by the account id, not the typed name.
    await vi.waitFor(() => {
      expect(looked).toEqual(['acc-2'])
    })
    expect(await screen.findByText('viewer')).toBeInTheDocument()
  })

  it('gives the lookup read its loading and empty placeholders', async () => {
    const user = userEvent.setup()
    const pending = deferred<readonly ConsoleBinding[]>()
    const operations = withAuthz({
      listAccountBindings: () => pending.promise,
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, '账户标识', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '查询' }))

    const lookup = section('查询账户的绑定')
    expect(
      within(lookup).getByRole('status', {
        name: '正在加载该账户的绑定',
      }),
    ).toBeInTheDocument()

    await act(async () => {
      pending.resolve([])
      await pending.promise
    })
    expect(await within(lookup).findByText('暂无绑定')).toBeInTheDocument()
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
    await chooseAccount(user, '账户标识', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '查询' }))

    const lookup = section('查询账户的绑定')
    expect(
      await within(lookup).findByText('该账户的绑定加载失败'),
    ).toBeInTheDocument()
    await user.click(within(lookup).getByRole('button', { name: '重试' }))
    expect(await within(lookup).findByText('暂无绑定')).toBeInTheDocument()
    expect(reads).toBe(2)
    // The retry's pending state withdraws the failure placeholder.
    expect(
      within(lookup).queryByText('该账户的绑定加载失败'),
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
    await user.click(screen.getByRole('button', { name: '删除角色 editor' }))
    // Deleting a role revokes its grants and bindings with it and cannot be
    // undone: the heavier two-step guard -- the first click only arms.
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
    expect(removed).toEqual([])
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await vi.waitFor(() => {
      expect(removed).toEqual(['editor'])
    })
    // The dialog closes on the confirming click, and the landed delete
    // fills the role scope's success region after it closes.
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByText('角色「editor」已删除')).toBeInTheDocument()
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
    await screen.findByRole('button', { name: '删除角色 beta' })

    // The removal is confirmed while beta is the selected role.
    await user.click(
      screen.getByRole('button', { name: '管理授权（角色 beta）' }),
    )
    expect(await screen.findByText('角色「beta」的授权')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '删除角色 beta' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
    await act(async () => {
      await new Promise((resolve) =>
        setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
      )
    })
    await user.click(
      within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
    )
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    // While the deletion is in flight the operator moves to alpha. The
    // settlement must clear a selection only if it still names beta.
    await user.click(
      screen.getByRole('button', { name: '管理授权（角色 alpha）' }),
    )
    expect(await screen.findByText('角色「alpha」的授权')).toBeInTheDocument()

    await act(async () => {
      removal.resolve()
    })
    await vi.waitFor(() =>
      expect(screen.queryByText('角色「beta」已删除')).toBeInTheDocument(),
    )
    expect(screen.getByText('角色「alpha」的授权')).toBeInTheDocument()
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
    await user.click(screen.getByRole('button', { name: '删除角色 editor' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
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
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
    )
  })

  it('renders the retryable error placeholder when the roles read fails', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('角色加载失败')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the roles read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listRoles: () => Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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
    await user.type(screen.getByLabelText('角色名'), 'auditor')
    await user.click(screen.getByRole('button', { name: '保存角色' }))
    expect(await within(section('角色')).findByRole('alert')).toHaveTextContent(
      '服务端无法读取该请求，请检查表单后重试。',
    )

    // The grants group refuses the grant.
    await user.click(
      screen.getByRole('button', { name: '管理授权（角色 editor）' }),
    )
    await within(section('授权')).findByText('crm.deal.read')
    await user.click(screen.getByLabelText('权限'))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: '授予' }))
    expect(await within(section('授权')).findByRole('alert')).toHaveTextContent(
      '该改动与现有角色或绑定冲突。',
    )

    // Neither section carries the other's text, and no page-level banner
    // aggregates them: the two regions are the only alerts.
    expect(within(section('角色')).getByRole('alert')).toHaveTextContent(
      '服务端无法读取该请求，请检查表单后重试。',
    )
    expect(
      within(section('角色')).queryByText('该改动与现有角色或绑定冲突。'),
    ).toBeNull()
    expect(
      within(section('授权')).queryByText(
        '服务端无法读取该请求，请检查表单后重试。',
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
          grant({ resource: 'deal', action: 'write' }),
        ]),
      revokePermission: () => Promise.reject({ code: 'authz.forbidden' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.click(
      await screen.findByRole('button', { name: '管理授权（角色 editor）' }),
    )
    const grants = section('授权')
    const failed = await within(grants).findByRole('button', {
      name: '撤销授权 crm.deal.read',
    })
    const other = within(grants).getByRole('button', {
      name: '撤销授权 crm.deal.write',
    })
    await user.click(failed)

    // The failing row carries the marker; the row beside it does not.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '你没有执行此操作的权限。',
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

  it('keeps a refused lookup read in the permission placeholder, not a write error', async () => {
    const user = userEvent.setup()
    const operations = withAuthz({
      listAccountBindings: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await chooseAccount(user, '账户标识', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '查询' }))

    const lookup = section('查询账户的绑定')
    expect(await within(lookup).findByText('权限不足')).toBeInTheDocument()
    expect(within(lookup).queryByRole('button', { name: '重试' })).toBeNull()
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
      await screen.findByRole('button', { name: '管理授权（角色 editor）' }),
    )
    await user.click(screen.getByLabelText('权限'))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: '授予' }))

    // The selected role drives the grants scope, and the tenant drives
    // every scope: both are out of reach while the grant is in flight.
    expect(
      screen.getByRole('button', { name: '管理授权（角色 viewer）' }),
    ).toBeDisabled()
    expect(screen.getByLabelText('租户')).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText('已向角色「editor」授予权限 crm.deal.write'),
    ).toBeInTheDocument()

    // Another role means another context: the old confirmation goes.
    await user.click(
      screen.getByRole('button', { name: '管理授权（角色 viewer）' }),
    )
    expect(
      screen.queryByText('已向角色「editor」授予权限 crm.deal.write'),
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
    await user.type(screen.getByLabelText('角色'), 'viewer')
    await chooseAccount(user, '账户', 'Ada', 'Ada (acc-2)')
    await user.click(screen.getByRole('button', { name: '绑定' }))

    // The chosen account drives the bindings scope: it is disabled while
    // the write it drives is in flight.
    expect(screen.getByLabelText('账户')).toBeDisabled()

    await act(async () => {
      pending.resolve()
      await pending.promise
    })
    expect(
      await screen.findByText('已将角色「viewer」绑定到账户「Ada」'),
    ).toBeInTheDocument()

    // Choosing another account is another context: the confirmation goes.
    await chooseAccount(user, '账户', 'Operator', 'Operator (acc-1)')
    expect(
      screen.queryByText('已将角色「viewer」绑定到账户「Ada」'),
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
