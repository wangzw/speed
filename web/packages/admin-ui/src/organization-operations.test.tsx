import { useState } from 'react'
import type { ReactNode } from 'react'
import { describe, expect, it } from 'vitest'
import { act, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { evictTenantQueries } from '@speed/auth-core'
import { OrganizationAdminProvider } from './OrganizationAdminProvider.js'
import {
  OrganizationPermissionsPage,
  OrganizationSettingsPage,
} from './organization-pages.js'
import {
  useOrganizationAdminOperations,
  organizationAdminOperations,
} from './organization-operations.js'
import { organizationAdminRoutes } from './organization-routes.js'
import { renderWithProviders } from '../test-utils/render.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import type { ObservedCall } from '../test-utils/request-rig.js'
import { profileFixture } from '../test-utils/operations.js'
import type {
  AdminInvitation,
  AdminInvitationPage,
  AdminMember,
  AdminMemberPage,
  AdminRole,
} from './operations.js'

const TENANT = 'acme'

const PREVIOUS_OWNER: AdminMember = {
  tenant: TENANT,
  account: 'acc-1',
  display_name: 'Ada Lovelace',
  username: 'ada',
  active: true,
  joined_at: '2026-01-01T00:00:00Z',
  roles: ['tenant-admin'],
  source: 'admin',
  sso_exempt: false,
}

const NEW_OWNER: AdminMember = {
  ...PREVIOUS_OWNER,
  account: 'acc-2',
  display_name: 'Bob Stone',
  username: 'bob',
}

function readResponder(call: ObservedCall): unknown {
  if (call.path === `/api/v1/tenants/${TENANT}/settings`) {
    return { settings: [], writable: true, read_only_reason: '' }
  }
  if (call.path === `/api/v1/tenants/${TENANT}/roles`) {
    return { roles: [] }
  }
  if (call.path === `/api/v1/tenants/${TENANT}/permissions`) {
    return { permissions: [] }
  }
  if (call.path === `/api/v1/tenants/${TENANT}/roles/tenant-admin/grants`) {
    return { grants: [] }
  }
  if (call.path === `/api/v1/tenants/${TENANT}/roles/tenant-admin/bindings`) {
    return { bindings: [], next_cursor: '' }
  }
  if (call.path === `/api/v1/tenants/${TENANT}/bindings`) {
    return { bindings: [] }
  }
  throw new Error(`unexpected request: ${call.method} ${call.path}`)
}

describe('the tenant operations address only tenant-addressed endpoints', () => {
  it('reads each surface at the tenant paths and no platform path', async () => {
    const calls = bindAdminRequest(readResponder)
    function ReadProbe() {
      const operations = useOrganizationAdminOperations()
      const settings = operations.useTenantSettingsList(TENANT)
      const roles = operations.useTenantRoles(TENANT)
      const permissions = operations.useTenantPermissions(TENANT)
      const grants = operations.useTenantGrants(TENANT, 'tenant-admin')
      const roleBindings = operations.useTenantRoleBindings(
        TENANT,
        'tenant-admin',
        { limit: 50 },
      )
      const bindings = operations.useTenantBindings(TENANT)
      const settled = [
        settings,
        roles,
        permissions,
        grants,
        roleBindings,
        bindings,
      ].every((read) => read.isSuccess)
      return <p>{settled ? 'settled' : 'loading'}</p>
    }
    renderWithProviders(<ReadProbe />)
    expect(await screen.findByText('settled')).toBeInTheDocument()
    expect(calls.map((call) => call.path).sort()).toEqual(
      [
        `/api/v1/tenants/${TENANT}/bindings`,
        `/api/v1/tenants/${TENANT}/permissions`,
        `/api/v1/tenants/${TENANT}/roles`,
        `/api/v1/tenants/${TENANT}/roles/tenant-admin/bindings`,
        `/api/v1/tenants/${TENANT}/roles/tenant-admin/grants`,
        `/api/v1/tenants/${TENANT}/settings`,
      ].sort(),
    )
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })

  it('sends the issue role and resends on the invitation route', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(
      (call) => {
        if (
          call.method === 'POST' &&
          call.path === `/api/v1/tenants/${TENANT}/invitations`
        ) {
          return {}
        }
        if (
          call.method === 'POST' &&
          call.path === `/api/v1/tenants/${TENANT}/invitations/inv-1/resend`
        ) {
          return {}
        }
        throw new Error(`unexpected request: ${call.method} ${call.path}`)
      },
      { recordBody: true },
    )
    function InvitationWrites() {
      const issue = organizationAdminOperations.useIssueInvitation()
      const resend = organizationAdminOperations.useResendInvitation()
      return (
        <>
          <button
            type="button"
            onClick={() =>
              issue.mutate({
                tenant: TENANT,
                email: 'a@b.co',
                expiresAt: undefined,
                role: 'tenant-operator',
              })
            }
          >
            issueRole
          </button>
          <button
            type="button"
            onClick={() => resend.mutate({ tenant: TENANT, id: 'inv-1' })}
          >
            resend
          </button>
        </>
      )
    }
    renderWithProviders(<InvitationWrites />)

    await user.click(screen.getByRole('button', { name: 'issueRole' }))
    await user.click(screen.getByRole('button', { name: 'resend' }))
    await waitFor(() => {
      expect(calls).toContainEqual({
        method: 'POST',
        path: `/api/v1/tenants/${TENANT}/invitations`,
        query: undefined,
        body: expect.objectContaining({ role: 'tenant-operator' }),
      })
      expect(calls).toContainEqual({
        method: 'POST',
        path: `/api/v1/tenants/${TENANT}/invitations/inv-1/resend`,
        query: undefined,
        body: undefined,
      })
    })
  })

  it('writes and removes a tenant setting at the tenant paths', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (
        (call.method === 'PUT' || call.method === 'DELETE') &&
        call.path === `/api/v1/tenants/${TENANT}/settings/app/mode`
      ) {
        return undefined
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    function WriteProbe() {
      const operations = useOrganizationAdminOperations()
      const write = operations.useTenantSettingsWrite()
      const remove = operations.useTenantSettingsRemove()
      return (
        <div>
          <button
            type="button"
            onClick={() =>
              write.mutate({
                tenant: TENANT,
                namespace: 'app',
                key: 'mode',
                value: 'on',
              })
            }
          >
            write
          </button>
          <button
            type="button"
            onClick={() =>
              remove.mutate({
                tenant: TENANT,
                namespace: 'app',
                key: 'mode',
              })
            }
          >
            remove
          </button>
        </div>
      )
    }
    renderWithProviders(<WriteProbe />)
    await user.click(screen.getByRole('button', { name: 'write' }))
    await waitFor(() =>
      expect(calls).toContainEqual({
        method: 'PUT',
        path: `/api/v1/tenants/${TENANT}/settings/app/mode`,
      }),
    )
    await user.click(screen.getByRole('button', { name: 'remove' }))
    await waitFor(() =>
      expect(calls).toContainEqual({
        method: 'DELETE',
        path: `/api/v1/tenants/${TENANT}/settings/app/mode`,
      }),
    )
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })

  it('writes a role description without the legacy description key', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(
      (call) => {
        if (
          call.method === 'PUT' &&
          call.path === `/api/v1/tenants/${TENANT}/roles/ops`
        ) {
          return undefined
        }
        throw new Error(`unexpected request: ${call.method} ${call.path}`)
      },
      { recordBody: true },
    )
    function WriteProbe() {
      const operations = useOrganizationAdminOperations()
      const write = operations.useTenantWriteRole()
      return (
        <button
          type="button"
          onClick={() =>
            write.mutate({
              tenant: TENANT,
              role: 'ops',
              description: 'Runs the operations.',
            })
          }
        >
          write
        </button>
      )
    }
    renderWithProviders(<WriteProbe />)
    await user.click(screen.getByRole('button', { name: 'write' }))
    await waitFor(() => expect(calls).toHaveLength(1))
    expect(calls[0]).toEqual({
      method: 'PUT',
      path: `/api/v1/tenants/${TENANT}/roles/ops`,
      query: undefined,
      body: { description: 'Runs the operations.' },
    })
    expect(calls[0]?.body).not.toHaveProperty('description_key')
  })
})

describe('the tenant operations key their reads by tenant', () => {
  function rolesResponder(call: ObservedCall): unknown {
    if (call.path.endsWith('/roles')) {
      return { roles: [] }
    }
    throw new Error(`unexpected request: ${call.method} ${call.path}`)
  }

  function KeyProbe() {
    const [tenant, setTenant] = useState('acme')
    const operations = useOrganizationAdminOperations()
    const roles = operations.useTenantRoles(tenant)
    return (
      <div>
        <p>{roles.isSuccess ? `roles:${tenant}` : `pending:${tenant}`}</p>
        <button type="button" onClick={() => setTenant('globex')}>
          switch
        </button>
      </div>
    )
  }

  it('builds a different key for a switched tenant and evicts only the departed one', async () => {
    const user = userEvent.setup()
    bindAdminRequest(rolesResponder)
    const { queryClient } = renderWithProviders(<KeyProbe />)
    expect(await screen.findByText('roles:acme')).toBeInTheDocument()
    await waitFor(() =>
      expect(
        queryClient.getQueryData(['acme', '/api/v1/tenants/acme/roles']),
      ).toEqual({ roles: [] }),
    )

    await user.click(screen.getByRole('button', { name: 'switch' }))
    expect(await screen.findByText('roles:globex')).toBeInTheDocument()
    await waitFor(() =>
      expect(
        queryClient.getQueryData(['globex', '/api/v1/tenants/globex/roles']),
      ).toEqual({ roles: [] }),
    )
    expect(
      queryClient.getQueryData(['acme', '/api/v1/tenants/acme/roles']),
    ).toEqual({ roles: [] })

    evictTenantQueries(queryClient, 'acme')
    expect(
      queryClient.getQueryData(['acme', '/api/v1/tenants/acme/roles']),
    ).toBeUndefined()
    expect(
      queryClient.getQueryData(['globex', '/api/v1/tenants/globex/roles']),
    ).toEqual({ roles: [] })
  })
})

describe('the inherited invitation, member and SSO reads key by tenant', () => {
  function inheritedReadsResponder(call: ObservedCall): unknown {
    if (call.path.endsWith('/sso/providers/idp-1')) {
      return { provider: { id: 'idp-1' } }
    }
    if (call.path.endsWith('/sso/providers')) {
      return { providers: [] }
    }
    if (call.path.endsWith('/invitations')) {
      return { invitations: [], next_cursor: '' }
    }
    if (call.path.endsWith('/members')) {
      return { members: [] }
    }
    throw new Error(`unexpected request: ${call.method} ${call.path}`)
  }

  function KeyProbe() {
    const [tenant, setTenant] = useState('acme')
    const operations = useOrganizationAdminOperations()
    const invitations = operations.useInvitations(tenant)
    const members = operations.useMembers(tenant)
    const providers = operations.useSsoProviders(tenant)
    const provider = operations.useSsoProvider({ tenant, id: 'idp-1' })
    const settled =
      invitations.isSuccess &&
      members.isSuccess &&
      providers.isSuccess &&
      provider.isSuccess
    return (
      <div>
        <p>{settled ? `settled:${tenant}` : `pending:${tenant}`}</p>
        <button type="button" onClick={() => setTenant('globex')}>
          switch
        </button>
      </div>
    )
  }

  it('prefixes each read with the tenant and evicts only the departed one', async () => {
    const user = userEvent.setup()
    bindAdminRequest(inheritedReadsResponder)
    const { queryClient } = renderWithProviders(<KeyProbe />)
    expect(await screen.findByText('settled:acme')).toBeInTheDocument()

    const acmeKeys = [
      ['acme', '/api/v1/tenants/acme/invitations'],
      ['acme', '/api/v1/tenants/acme/members'],
      ['acme', '/api/v1/tenants/acme/sso/providers'],
      ['acme', '/api/v1/tenants/acme/sso/providers/idp-1'],
    ] as const
    await waitFor(() => {
      for (const key of acmeKeys) {
        expect(queryClient.getQueryData(key)).toBeDefined()
      }
    })
    for (const bare of [
      '/api/v1/tenants/acme/invitations',
      '/api/v1/tenants/acme/members',
      '/api/v1/tenants/acme/sso/providers',
      '/api/v1/tenants/acme/sso/providers/idp-1',
    ]) {
      expect(queryClient.getQueryData([bare]), bare).toBeUndefined()
    }

    await user.click(screen.getByRole('button', { name: 'switch' }))
    expect(await screen.findByText('settled:globex')).toBeInTheDocument()

    const globexKeys = [
      ['globex', '/api/v1/tenants/globex/invitations'],
      ['globex', '/api/v1/tenants/globex/members'],
      ['globex', '/api/v1/tenants/globex/sso/providers'],
      ['globex', '/api/v1/tenants/globex/sso/providers/idp-1'],
    ] as const
    await waitFor(() => {
      for (const key of globexKeys) {
        expect(queryClient.getQueryData(key)).toBeDefined()
      }
    })

    evictTenantQueries(queryClient, 'acme')
    for (const key of acmeKeys) {
      expect(queryClient.getQueryData(key), String(key)).toBeUndefined()
    }
    for (const key of globexKeys) {
      expect(queryClient.getQueryData(key), String(key)).toBeDefined()
    }
  })
})

describe('the organization declarations for roles, settings and profile', () => {
  it('carries the tenant-domain permission and feature codes', () => {
    expect(organizationAdminRoutes.map((route) => route.id)).toEqual([
      'organization-profile',
      'organization-invitations',
      'organization-sso',
      'organization-roles',
      'organization-settings',
    ])
    expect(organizationAdminRoutes.map((route) => route.path)).toEqual([
      '/profile',
      '/invitations',
      '/sso',
      '/roles',
      '/settings',
    ])
    const roles = organizationAdminRoutes.find(
      (route) => route.id === 'organization-roles',
    )
    expect(roles?.permission).toBe('authz.tenant.role.read')
    expect(roles?.requires).toEqual(['authz.tenantadmin', 'authn.admin'])
    const settings = organizationAdminRoutes.find(
      (route) => route.id === 'organization-settings',
    )
    expect(settings?.permission).toBe('settings.tenant.read')
    expect(settings?.requires).toEqual(['settings.tenantadmin'])
    const profile = organizationAdminRoutes.find(
      (route) => route.id === 'organization-profile',
    )
    expect(profile?.permission).toBe('tenant.profile.read')
    expect(profile?.requires).toEqual(['tenant.profile'])
    for (const route of organizationAdminRoutes) {
      expect(route.requires).not.toContain('tenant.admin')
    }
  })
})

describe('the organization settings wrapper', () => {
  const SETTING = {
    key: 'tenant.admission.require-mfa',
    title: 'Require multi-factor authentication',
    description: 'Members must sign in with a second factor.',
    type: 'bool',
    control: 'switch',
    danger: 'critical',
    effect: 'immediate',
    tenant_access: 'read-write',
    effective_value: 'false',
    platform_value: 'false',
    tenant_value: 'false',
    locked_by_platform: false,
    has_row: false,
    source: 'default',
  } as const

  it('pins the page to the current tenant and reads no platform settings', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === `/api/v1/tenants/${TENANT}/settings`) {
        return {
          settings: [SETTING],
          writable: true,
          read_only_reason: '',
        }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={organizationAdminOperations}
        hasFeature={() => true}
      >
        <OrganizationSettingsPage />
      </OrganizationAdminProvider>,
    )
    expect(
      await screen.findByText('Require multi-factor authentication'),
    ).toBeInTheDocument()
    expect(calls.length).toBeGreaterThan(0)
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })
})

describe('the organization roles wrapper', () => {
  const ROLE: AdminRole = {
    tenant: TENANT,
    role: 'tenant-operator',
    builtin: false,
    can_manage: true,
    description: '',
    member_count: null,
    permission_count: 0,
    created_at: '2026-01-01T00:00:00Z',
  }
  const MEMBER = {
    account: 'acc-1',
    tenant: TENANT,
    active: true,
    display_name: 'Ada Lovelace',
    source: 'admin',
    sso_exempt: false,
    roles: [],
    joined_at: '2026-10-15T12:00:00Z',
  }

  it('pins the page to the current tenant and reads no platform asset', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === `/api/v1/tenants/${TENANT}/roles`) {
        return { roles: [ROLE] }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/permissions`) {
        return { permissions: [] }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/bindings`) {
        return { bindings: [] }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/members`) {
        return { members: [MEMBER], next_cursor: '' }
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={organizationAdminOperations}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
        protectedRoles={['tenant-admin']}
      >
        <OrganizationPermissionsPage />
      </OrganizationAdminProvider>,
    )
    expect(await screen.findByText('tenant-operator')).toBeInTheDocument()
    expect(calls.length).toBeGreaterThan(0)
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })
})

describe('the organization operations bundle', () => {
  it('exposes the tenant members and no platform member', () => {
    const members = Object.keys(organizationAdminOperations)
    for (const member of [
      'useTenantSettingsList',
      'useTenantSettingsWrite',
      'useTenantSettingsRemove',
      'useTenantRoles',
      'useTenantPermissions',
      'useTenantGrants',
      'useTenantRoleBindings',
      'useTenantBindings',
      'useTenantOwnerPending',
      'useTenantOwnerRequest',
      'useTenantOwnerCancel',
      'useTenantOwnerDelete',
    ]) {
      expect(members).toContain(member)
    }
    for (const platform of [
      'useTenants',
      'useTenant',
      'useSettings',
      'useSetSetting',
      'useRemoveSetting',
      'useRoles',
      'usePermissions',
      'useGrants',
      'useBindings',
      'useAccounts',
      'useAuditRecords',
    ]) {
      expect(members).not.toContain(platform)
    }
  })
})

describe('the organization ownership writes', () => {
  function ownershipResponder(
    initialPending = false,
  ): (call: ObservedCall) => unknown {
    const transfer = {
      id: 'transfer-1',
      target_display_name: 'Bob Stone',
      target_username: 'bob',
      requested_at: '2026-10-20T08:30:00Z',
      expires_at: '2026-10-27T08:30:00Z',
      status: 'pending',
    }
    let pending: typeof transfer | null = initialPending ? transfer : null
    return (call) => {
      if (call.path === `/api/v1/tenants/${TENANT}/profile`) {
        return { profile: profileFixture() }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/members`) {
        return { members: [PREVIOUS_OWNER, NEW_OWNER], next_cursor: '' }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/owner/requests/pending`) {
        return { request: pending }
      }
      if (
        call.method === 'POST' &&
        call.path === `/api/v1/tenants/${TENANT}/owner/requests`
      ) {
        pending = transfer
        return { request: transfer }
      }
      if (
        call.method === 'DELETE' &&
        call.path === `/api/v1/tenants/${TENANT}/owner/requests/transfer-1`
      ) {
        pending = null
        return undefined
      }
      if (
        call.method === 'DELETE' &&
        call.path === `/api/v1/tenants/${TENANT}`
      ) {
        return undefined
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    }
  }

  function OwnershipProbe({
    action,
  }: {
    readonly action: 'request' | 'cancel' | 'delete'
  }) {
    const operations = useOrganizationAdminOperations()
    const profile = operations.useTenantProfile(TENANT)
    const members = operations.useMembers(TENANT, { limit: 100 })
    const pending = operations.useTenantOwnerPending(TENANT)
    const request = operations.useTenantOwnerRequest()
    const cancel = operations.useTenantOwnerCancel()
    const remove = operations.useTenantOwnerDelete()
    return (
      <div>
        <p>
          {profile.isSuccess && members.isSuccess && pending.isSuccess
            ? 'settled'
            : 'loading'}
        </p>
        <p data-testid="member-rows">
          {(members.data?.members ?? [])
            .map(
              (member) =>
                `${member.account}:${member.active ? 'active' : 'suspended'}:${member.roles.join('|')}`,
            )
            .join(',')}
        </p>
        <p data-testid="pending-request">{pending.data?.id ?? 'none'}</p>
        <button
          type="button"
          onClick={() => {
            if (action === 'request') {
              request.mutate({ tenant: TENANT, account: 'acc-2' })
            } else if (action === 'cancel') {
              cancel.mutate({ tenant: TENANT, request: 'transfer-1' })
            } else {
              remove.mutate({ tenant: TENANT })
            }
          }}
        >
          write
        </button>
      </div>
    )
  }

  it('records a transfer request at the tenant path without changing the owner or re-reading the profile', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(ownershipResponder(), { recordBody: true })
    renderWithProviders(<OwnershipProbe action="request" />)
    expect(await screen.findByText('settled')).toBeInTheDocument()
    expect(screen.getByTestId('member-rows')).toHaveTextContent(
      'acc-1:active:tenant-admin',
    )
    expect(screen.getByTestId('pending-request')).toHaveTextContent('none')

    const profileReads = () =>
      calls.filter((call) => call.path === `/api/v1/tenants/${TENANT}/profile`)
        .length
    const memberReads = () =>
      calls.filter((call) => call.path === `/api/v1/tenants/${TENANT}/members`)
        .length
    const pendingReads = () =>
      calls.filter(
        (call) =>
          call.path === `/api/v1/tenants/${TENANT}/owner/requests/pending`,
      ).length
    expect(profileReads()).toBe(1)
    expect(memberReads()).toBe(1)
    expect(pendingReads()).toBe(1)

    await user.click(screen.getByRole('button', { name: 'write' }))
    await waitFor(() =>
      expect(calls).toContainEqual({
        method: 'POST',
        path: `/api/v1/tenants/${TENANT}/owner/requests`,
        query: undefined,
        body: { account: 'acc-2' },
      }),
    )
    await waitFor(() => expect(pendingReads()).toBe(2))
    await waitFor(() => expect(memberReads()).toBe(2))
    expect(profileReads()).toBe(1)
    expect(screen.getByTestId('pending-request')).toHaveTextContent(
      'transfer-1',
    )
    expect(screen.getByTestId('member-rows')).toHaveTextContent(
      'acc-1:active:tenant-admin',
    )
    expect(calls.filter((call) => call.method === 'PUT')).toEqual([])
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })

  it('cancels a pending request at the tenant path and re-reads the pending state', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(ownershipResponder(true))
    renderWithProviders(<OwnershipProbe action="cancel" />)
    expect(await screen.findByText('settled')).toBeInTheDocument()
    expect(screen.getByTestId('pending-request')).toHaveTextContent(
      'transfer-1',
    )

    await user.click(screen.getByRole('button', { name: 'write' }))
    await waitFor(() =>
      expect(calls).toContainEqual({
        method: 'DELETE',
        path: `/api/v1/tenants/${TENANT}/owner/requests/transfer-1`,
      }),
    )
    await waitFor(() =>
      expect(screen.getByTestId('pending-request')).toHaveTextContent('none'),
    )
  })

  it('deletes at the tenant path and reflects the tombstone without a refused re-read', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(ownershipResponder())
    const { queryClient } = renderWithProviders(
      <OwnershipProbe action="delete" />,
    )
    expect(await screen.findByText('settled')).toBeInTheDocument()

    const profileReads = () =>
      calls.filter((call) => call.path === `/api/v1/tenants/${TENANT}/profile`)
        .length
    const memberReads = () =>
      calls.filter((call) => call.path === `/api/v1/tenants/${TENANT}/members`)
        .length

    await user.click(screen.getByRole('button', { name: 'write' }))
    await waitFor(() =>
      expect(calls).toContainEqual({
        method: 'DELETE',
        path: `/api/v1/tenants/${TENANT}`,
      }),
    )
    await waitFor(() =>
      expect(
        queryClient.getQueryData([
          TENANT,
          `/api/v1/tenants/${TENANT}/profile`,
        ]),
      ).toEqual({
        profile: { ...profileFixture(), status: 'deleted' },
      }),
    )
    expect(profileReads()).toBe(1)
    expect(memberReads()).toBe(1)
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

const PENDING_INVITATION: AdminInvitation = {
  tenant: TENANT,
  id: 'inv-1',
  email: 'ada@example.com',
  status: 'pending',
  state: 'pending',
  invited_by: 'staff-1',
  expires_at: '2099-01-01T00:00:00Z',
  accepted_at: null,
  created_at: '2026-01-01T00:00:00Z',
  revoked_at: null,
  last_sent_at: '2026-01-01T00:00:00Z',
  send_count: 1,
}

function pagerWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { readonly children: ReactNode }) {
    return (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )
  }
}

function freshQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
}

describe('the organization member and invitation reads keep a page within one tenant', () => {
  it('keeps the previous page while the next page of the same tenant reads', async () => {
    const nextMembers = deferred<AdminMemberPage>()
    const nextInvitations = deferred<AdminInvitationPage>()
    const calls = bindAdminRequest((call) => {
      if (call.path === `/api/v1/tenants/${TENANT}/members`) {
        return call.query?.after === undefined
          ? { members: [PREVIOUS_OWNER], next_cursor: 'members-2' }
          : nextMembers.promise
      }
      if (call.path === `/api/v1/tenants/${TENANT}/invitations`) {
        return call.query?.after === undefined
          ? { invitations: [PENDING_INVITATION], next_cursor: 'invitations-2' }
          : nextInvitations.promise
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    const { result, rerender } = renderHook(
      ({ after }: { readonly after: string | undefined }) => ({
        members: organizationAdminOperations.useMembers(
          TENANT,
          after === undefined ? undefined : { after },
        ),
        invitations: organizationAdminOperations.useInvitations(
          TENANT,
          after === undefined ? undefined : { after },
        ),
      }),
      {
        initialProps: { after: undefined as string | undefined },
        wrapper: pagerWrapper(freshQueryClient()),
      },
    )
    await waitFor(() => {
      expect(result.current.members.data).toBeDefined()
      expect(result.current.invitations.data).toBeDefined()
    })
    const previousMembers = result.current.members.data
    const previousInvitations = result.current.invitations.data

    rerender({ after: 'next-page' })
    await waitFor(() => {
      expect(calls.some((call) => call.query?.after === 'next-page')).toBe(true)
    })
    expect(result.current.members.data).toEqual(previousMembers)
    expect(result.current.members.isPlaceholderData).toBe(true)
    expect(result.current.invitations.data).toEqual(previousInvitations)
    expect(result.current.invitations.isPlaceholderData).toBe(true)

    await act(async () => {
      nextMembers.resolve({ members: [], next_cursor: '' })
      nextInvitations.resolve({ invitations: [], next_cursor: '' })
    })
  })

  it('drops the previous tenant page while the switched tenant reads', async () => {
    const globexMembers = deferred<AdminMemberPage>()
    const globexInvitations = deferred<AdminInvitationPage>()
    const calls = bindAdminRequest((call) => {
      if (call.path === `/api/v1/tenants/${TENANT}/members`) {
        return { members: [PREVIOUS_OWNER], next_cursor: '' }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/invitations`) {
        return { invitations: [PENDING_INVITATION], next_cursor: '' }
      }
      if (call.path === '/api/v1/tenants/globex/members') {
        return globexMembers.promise
      }
      if (call.path === '/api/v1/tenants/globex/invitations') {
        return globexInvitations.promise
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    const { result, rerender } = renderHook(
      ({ tenant }: { readonly tenant: string }) => ({
        members: organizationAdminOperations.useMembers(tenant),
        invitations: organizationAdminOperations.useInvitations(tenant),
      }),
      {
        initialProps: { tenant: TENANT },
        wrapper: pagerWrapper(freshQueryClient()),
      },
    )
    await waitFor(() => {
      expect(result.current.members.data).toBeDefined()
      expect(result.current.invitations.data).toBeDefined()
    })

    rerender({ tenant: 'globex' })
    await waitFor(() => {
      expect(
        calls.some((call) => call.path === '/api/v1/tenants/globex/members'),
      ).toBe(true)
      expect(
        calls.some((call) => call.path === '/api/v1/tenants/globex/invitations'),
      ).toBe(true)
    })
    expect(result.current.members.data).toBeUndefined()
    expect(result.current.members.isPlaceholderData).toBe(false)
    expect(result.current.invitations.data).toBeUndefined()
    expect(result.current.invitations.isPlaceholderData).toBe(false)

    await act(async () => {
      globexMembers.resolve({ members: [], next_cursor: '' })
      globexInvitations.resolve({ invitations: [], next_cursor: '' })
    })
  })
})
