/**
 * PermissionsPage: the authorization-administration page.
 *
 * It prompts for a tenant until one is chosen, then renders a tenant's
 * roles, the permission catalog and the bindings, and drives the role
 * create / delete, the grant / revoke, the bind / unbind and the
 * account-binding lookup through the bundle. A builtin role's grants open
 * read-only: its list is visible and every write control is withheld.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
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
    // The landed create fills the page's standing success region.
    expect(await screen.findByText('角色已保存')).toBeInTheDocument()
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
    // The landed revoke fills the page's standing success region.
    expect(await screen.findByText('已撤销权限')).toBeInTheDocument()
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
    // The landed unbind fills the page's standing success region.
    expect(await screen.findByText('角色已解绑')).toBeInTheDocument()
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
    // The landed delete fills the page's standing success region.
    expect(await screen.findByText('角色已删除')).toBeInTheDocument()
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
