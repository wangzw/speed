/**
 * PermissionsPage: the authorization-administration page.
 *
 * It prompts for a tenant until one is chosen, then renders a tenant's
 * roles, the permission catalog and the bindings, and drives the role
 * create / delete, the grant / revoke, the bind / unbind and the
 * account-binding lookup through the bundle.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PermissionsPage } from './PermissionsPage.js'
import type {
  ConsoleBinding,
  ConsoleGrant,
  ConsolePermission,
  ConsoleRole,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

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

  it('creates a role through the bundle', async () => {
    const user = userEvent.setup()
    const written: string[] = []
    const operations = withAuthz({
      writeRole: (input) => {
        written.push(input.role)
        return Promise.resolve()
      },
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await user.type(screen.getByLabelText('角色名'), 'auditor')
    await user.click(screen.getByRole('button', { name: '保存角色' }))
    await vi.waitFor(() => {
      expect(written).toEqual(['auditor'])
    })
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
    await user.click(screen.getByRole('button', { name: '管理授权' }))
    await screen.findByText('deal')
    await user.click(screen.getByLabelText('权限'))
    await user.click(await screen.findByRole('option', { name: 'crm.deal.write' }))
    await user.click(screen.getByRole('button', { name: '授予' }))
    await user.click(screen.getByRole('button', { name: '撤销' }))
    await vi.waitFor(() => {
      // Grant and revoke name the permission by its CODE, symmetrically:
      // the granted code is sent back on revoke through the catalog map.
      expect(granted).toEqual(['crm.deal.write'])
      expect(revoked).toEqual(['crm.deal.read'])
    })
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
    await user.click(screen.getByRole('button', { name: '管理授权' }))
    await screen.findByText('factor')
    await user.click(screen.getByRole('button', { name: '撤销' }))
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
    await user.click(screen.getByRole('button', { name: '解绑' }))
    await vi.waitFor(() => {
      expect(bound).toEqual(['viewer'])
      expect(unbound).toEqual(['editor'])
    })
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
    await user.click(screen.getByRole('button', { name: '删除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
    await vi.waitFor(() => {
      expect(removed).toEqual(['editor'])
    })
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
      listRoles: () => Promise.resolve([role()]),
      listBindings: () => Promise.resolve([binding({ role: 'viewer' })]),
    })
    renderWithProviders(<PermissionsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('editor')
    await expectNoAxeViolations()
  })
})
