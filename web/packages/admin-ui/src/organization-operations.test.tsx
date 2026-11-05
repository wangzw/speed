/**
 * The organization surface's tenant-scoped operations: the settings and role
 * members the organization bundle adds beside the invitations and SSO members.
 *
 * The read cases bind the shipped bundle's generated operations into the
 * request seam and record every URL: a tenant operation may address only
 * `/api/v1/tenants/{tenant}/...`, never a platform asset, and it must name
 * the tenant it was given. The key cases pin the tenant-scoped query-key
 * convention: the tenant is the key's outermost segment, a switch to
 * another tenant builds another key, and auth-core's eviction helper
 * removes exactly the departing tenant's subtree.
 *
 * The route and bundle-shape cases pin the declarations (`/roles` and
 * `/settings`, their tenant-domain permission codes and feature
 * dependencies) and the bundle's member set: the tenant members are the
 * bundle's, no platform member rides along.
 */

import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
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

const TENANT = 'acme'

/** The generated operation bodies the read probe's requests answer with. */
function readResponder(call: ObservedCall): unknown {
  if (call.path === `/api/v1/tenants/${TENANT}/settings`) {
    return []
  }
  if (call.path === `/api/v1/tenants/${TENANT}/settings/app`) {
    return []
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
      const namespace = operations.useTenantSettingsRead(TENANT, 'app')
      const roles = operations.useTenantRoles(TENANT)
      const permissions = operations.useTenantPermissions(TENANT)
      const grants = operations.useTenantGrants(TENANT, 'tenant-admin')
      const bindings = operations.useTenantBindings(TENANT)
      const settled = [
        settings,
        namespace,
        roles,
        permissions,
        grants,
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
        `/api/v1/tenants/${TENANT}/roles/tenant-admin/grants`,
        `/api/v1/tenants/${TENANT}/settings`,
        `/api/v1/tenants/${TENANT}/settings/app`,
      ].sort(),
    )
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
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
    // The two tenants never share a key: the switch cannot reuse the
    // previous tenant's rows.
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
  // The generated keys of these reads name the tenant inside the path
  // string, not as the key's outermost segment, so an organization read that
  // forwards them verbatim shares no prefix with the tenant subtree the
  // host evicts on a switch. The bundle wraps each in tenantQueryKey.
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
    // The bare generated key is not where the cache entry lives: a bare
    // entry would be invisible to the tenant-prefixed eviction below.
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

describe('the organization declarations for roles and settings', () => {
  it('carries the tenant-domain permission and feature codes', () => {
    expect(organizationAdminRoutes.map((route) => route.id)).toEqual([
      'organization-invitations',
      'organization-sso',
      'organization-roles',
      'organization-settings',
    ])
    expect(organizationAdminRoutes.map((route) => route.path)).toEqual([
      '/invitations',
      '/sso',
      '/roles',
      '/settings',
    ])
    const roles = organizationAdminRoutes.find(
      (route) => route.id === 'organization-roles',
    )
    expect(roles?.permission).toBe('authz.tenant.role.read')
    // The role page's account pickers read the tenant's members through the
    // membership endpoints, so the page needs that feature as well.
    expect(roles?.requires).toEqual(['authz.tenantadmin', 'authn.admin'])
    const settings = organizationAdminRoutes.find(
      (route) => route.id === 'organization-settings',
    )
    expect(settings?.permission).toBe('settings.tenant.read')
    expect(settings?.requires).toEqual(['settings.tenantadmin'])
    // The organization declarations never name the platform roster feature.
    for (const route of organizationAdminRoutes) {
      expect(route.requires).not.toContain('tenant.admin')
    }
  })
})

describe('the organization settings wrapper', () => {
  const SETTING = {
    key: 'app.mode',
    namespace: 'app',
    type: 'string',
    description: 'How the app runs',
    domain: '',
    sensitive: false,
    default: 'off',
    written: 'on',
    has_row: true,
    value: 'on',
    source: 'tenant',
  }

  it('pins the page to the current tenant and reads no platform settings', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === `/api/v1/tenants/${TENANT}/settings`) {
        return [SETTING]
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
    expect(await screen.findByText('app.mode')).toBeInTheDocument()
    expect(calls.length).toBeGreaterThan(0)
    for (const call of calls) {
      expect(call.path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
      expect(call.path.startsWith('/api/v1/system')).toBe(false)
    }
  })
})

describe('the organization roles wrapper', () => {
  const ROLE = {
    tenant: TENANT,
    role: 'tenant-operator',
    builtin: false,
    description_key: 'role.tenantOperator',
    created_at: '2026-01-01T00:00:00Z',
  }
  const MEMBER = {
    account: 'acc-1',
    tenant: TENANT,
    active: true,
    display_name: 'Ada Lovelace',
    source: 'admin',
    sso_exempt: false,
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
        return { members: [MEMBER] }
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
      'useTenantSettingsRead',
      'useTenantSettingsWrite',
      'useTenantSettingsRemove',
      'useTenantRoles',
      'useTenantPermissions',
      'useTenantGrants',
      'useTenantBindings',
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
