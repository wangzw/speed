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
  ConsoleBinding,
  ConsoleGrant,
  ConsolePermission,
  ConsoleRole,
  ConsoleWriteRoleInput,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
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
      // name a permission by), not the resource:action pair the grant list
      // shows -- the page maps a grant's pair back to its code for the
      // revoke.
      listPermissions: () =>
        Promise.resolve([
          permission('crm.deal.read'),
          permission('crm.deal.write'),
        ]),
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
    expect(screen.getByText('acc-1')).toBeInTheDocument()
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
    // The held grant is visible...
    expect(await screen.findByText('deal')).toBeInTheDocument()
    expect(
      screen.getByText('该角色为内建角色，授权为只读：不能授予，也不能撤销。'),
    ).toBeInTheDocument()
    // ...and neither the grant form nor the revoke action is offered.
    expect(screen.queryByLabelText('权限')).toBeNull()
    expect(screen.queryByRole('button', { name: '授予' })).toBeNull()
    expect(
      screen.queryByRole('button', { name: '撤销授权 deal read' }),
    ).toBeNull()
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
      name: '解绑账户 acc-1 的角色 viewer',
    })
    const refresh = screen.getByRole('button', { name: '刷新' })
    // The grant revoke belongs to the selected role's grant list, which
    // opens with the manage action.
    await user.click(manage)
    const revoke = await screen.findByRole('button', {
      name: '撤销授权 deal read',
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
    await screen.findByText('deal')
    await user.click(screen.getByLabelText('权限'))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: '授予' }))
    await user.click(screen.getByRole('button', { name: '撤销授权 deal read' }))
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
      // is not a suffix of the code); the grant list shows that pair. The
      // revoke still names the code, resolved by the unique action.
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
    await screen.findByText('factor')
    await user.click(screen.getByRole('button', { name: '撤销授权 factor reset' }))
    await vi.waitFor(() => {
      expect(revoked).toEqual(['authn.mfa.reset'])
    })
  })

  it('binds and unbinds a role', async () => {
    const user = userEvent.setup()
    const bound: string[] = []
    const unbound: string[] = []
    const operations = withAuthz({
      listBindings: () => Promise.resolve([binding()]),
      bindRole: (input) => {
        bound.push(input.role)
        return Promise.resolve()
      },
      unbindRole: (input) => {
        unbound.push(input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('acc-1')
    await user.type(screen.getByLabelText('账户'), 'acc-2')
    await user.type(screen.getByLabelText('角色'), 'viewer')
    await user.click(screen.getByRole('button', { name: '绑定' }))
    await user.click(screen.getByRole('button', { name: '解绑账户 acc-1 的角色 editor' }))
    await vi.waitFor(() => {
      expect(bound).toEqual(['viewer'])
      expect(unbound).toEqual(['editor'])
    })
    // The landed unbind fills the page's standing success region.
    expect(await screen.findByText('角色已解绑')).toBeInTheDocument()
  })

  it('looks up an account bindings', async () => {
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
    await user.type(screen.getByLabelText('账户标识'), 'acc-9')
    await user.click(screen.getByRole('button', { name: '查询' }))
    await vi.waitFor(() => {
      expect(looked).toEqual(['acc-9'])
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
