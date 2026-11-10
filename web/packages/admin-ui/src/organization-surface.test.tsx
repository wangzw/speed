/**
 * The organization administration surface's exports: its declarations, the
 * frameless provider and breadcrumb context, and the page wrappers.
 *
 * The wrapper cases pin the tenant contract: the wrapper takes the current
 * tenant from the provider and hands it to the shared page as a controlled
 * tenant, so the page issues its reads at that tenant and does not mount
 * the roster-fed tenant selector. The provider cases pin that it renders
 * no frame of its own while publishing the organization contexts the pages
 * read, and that the breadcrumb context is preferred only where it is
 * installed.
 */

import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  OrganizationAdminBreadcrumbProvider,
  useOrganizationAdminBreadcrumbs,
} from './OrganizationAdminBreadcrumb.js'
import {
  OrganizationAdminProvider,
  useOrganizationAdminContext,
  useOrganizationAdminProtectedRoles,
  useOrganizationAdminTenant,
} from './OrganizationAdminProvider.js'
import { useAdminHasFeature } from './internal/features.js'
import { useAdminRouteTable } from './internal/route-table.js'
import { organizationAdminRoutes } from './organization-routes.js'
import {
  OrganizationInvitationsPage,
  OrganizationPermissionsPage,
  OrganizationSettingsPage,
  OrganizationSsoPage,
} from './organization-pages.js'
import { organizationAdminOperations } from './organization-operations.js'
import { adminOperations } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import {
  createAdminOperations,
  issuedInvitationFixture,
  makeAdminBackend,
} from '../test-utils/operations.js'
import type { AdminBackend } from '../test-utils/operations.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANT = 'acme'

/** A backend that records the tenant each tenant-scoped read was called
 * with, and records no platform read at all. */
function recordingBackend(): {
  readonly backend: AdminBackend
  readonly readTenants: readonly string[]
  readonly rosterReads: () => number
} {
  const readTenants: string[] = []
  let rosterReads = 0
  const base = makeAdminBackend()
  const backend = makeAdminBackend({
    listTenants: () => {
      rosterReads += 1
      return Promise.resolve([])
    },
    listInvitations: (tenant) => {
      readTenants.push(tenant)
      return base.listInvitations(tenant)
    },
    listMembers: (tenant) => {
      readTenants.push(tenant)
      return base.listMembers(tenant)
    },
    listSsoProviders: (tenant) => {
      readTenants.push(tenant)
      return base.listSsoProviders(tenant)
    },
  })
  return { backend, readTenants, rosterReads: () => rosterReads }
}

const noFeatures = (): boolean => false

/**
 * The platform bundle beside the organization bundle: the organization pages run
 * through the same generated operations, and the request record -- not the
 * bundle's shape -- proves they never reach a platform asset.
 */
const fullOrganizationBundle = {
  ...adminOperations,
  ...organizationAdminOperations,
}

describe('organizationAdminRoutes', () => {
  it('declares the tenant-scoped pages with tenant-domain codes and features', () => {
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
    expect(organizationAdminRoutes.map((route) => route.permission)).toEqual([
      'tenant.profile.read',
      'authn.membership.manage',
      'authn.idp.manage',
      'authz.tenant.role.read',
      'settings.tenant.read',
    ])
    expect(organizationAdminRoutes.map((route) => route.requires)).toEqual([
      ['tenant.profile'],
      ['authn.admin', 'authn.invitations'],
      ['authn.admin', 'authn.admin.sso'],
      ['authz.tenantadmin', 'authn.admin'],
      ['settings.tenantadmin'],
    ])
    // The organization declarations never name the platform roster feature: the
    // surface reads no roster and holds no tenant selector.
    for (const route of organizationAdminRoutes) {
      expect(route.requires).not.toContain('tenant.admin')
    }
  })
})

describe('the organization page wrappers', () => {
  it('scopes the invitations page to the provided tenant without a selector', async () => {
    const { backend, readTenants, rosterReads } = recordingBackend()
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={noFeatures}
        routes={organizationAdminRoutes}
      >
        <OrganizationInvitationsPage />
      </OrganizationAdminProvider>,
    )
    await waitFor(() => expect(readTenants).toEqual([TENANT, TENANT]))
    // The controlled tenant means the page never reads the platform roster
    // for a selector; every read names the current tenant.
    expect(rosterReads()).toBe(0)
  })

  it('prefers the organization trail over the platform trail on the page header', async () => {
    const { backend } = recordingBackend()
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={noFeatures}
        routes={organizationAdminRoutes}
        breadcrumbs={[
          { label: 'Organization home' },
          { label: 'Invitations' },
        ]}
      >
        <OrganizationInvitationsPage />
      </OrganizationAdminProvider>,
    )
    // The organization trail is the one the page header renders ...
    expect(await screen.findByText('Organization home')).toBeInTheDocument()
    // ... and the platform console's own trail is not.
    expect(screen.queryByText(zhCN.shell.title)).toBeNull()
  })

  it('prefers the organization trail over the platform trail on the SSO header', async () => {
    const { backend } = recordingBackend()
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
        breadcrumbs={[{ label: 'Organization home' }, { label: 'SSO' }]}
      >
        <OrganizationSsoPage />
      </OrganizationAdminProvider>,
    )
    expect(await screen.findByText('Organization home')).toBeInTheDocument()
    expect(screen.queryByText(zhCN.shell.title)).toBeNull()
  })

  it.each([
    ['roles', OrganizationPermissionsPage],
    ['settings', OrganizationSettingsPage],
  ])(
    'prefers the organization trail over the platform trail on the %s header',
    async (_page, Page) => {
      const { backend } = recordingBackend()
      renderWithProviders(
        <OrganizationAdminProvider
          tenant={TENANT}
          operations={createAdminOperations(backend)}
          hasFeature={() => true}
          routes={organizationAdminRoutes}
          breadcrumbs={[
            { label: 'Organization home' },
            { label: 'A page' },
          ]}
          protectedRoles={['tenant-admin']}
        >
          <Page />
        </OrganizationAdminProvider>,
      )
      // The organization trail reaches every page through the shared page
      // header, including the two pages that never read the context
      // themselves.
      expect(await screen.findByText('Organization home')).toBeInTheDocument()
      expect(screen.queryByText(zhCN.shell.title)).toBeNull()
    },
  )

  it('titles the organization roles page with the organization menu label', async () => {
    // The roles page belongs to the organization surface, so its level-one
    // title is `organization.nav.roles` -- the same key its menu entry
    // reads -- rather than the platform console's `permissions.title`.
    const { backend } = recordingBackend()
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
      >
        <OrganizationPermissionsPage />
      </OrganizationAdminProvider>,
    )
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: zhCN.organization.nav.roles,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: zhCN.permissions.title,
      }),
    ).toBeNull()
  })

  it('scopes the SSO page to the provided tenant without a selector', async () => {
    const { backend, readTenants, rosterReads } = recordingBackend()
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
      >
        <OrganizationSsoPage />
      </OrganizationAdminProvider>,
    )
    await waitFor(() => expect(readTenants).toEqual([TENANT]))
    expect(rosterReads()).toBe(0)
  })

  it('renders the acceptance link and copy control for an organization invitation', async () => {
    const user = userEvent.setup()
    const backend = makeAdminBackend({
      issueInvitation: () =>
        Promise.resolve(
          issuedInvitationFixture({
            invitation: {
              ...issuedInvitationFixture().invitation,
              email: 'new@example.com',
            },
          }),
        ),
    })
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={noFeatures}
        routes={organizationAdminRoutes}
        invitationLink={(token) => `/invitations/${token}`}
      >
        <OrganizationInvitationsPage />
      </OrganizationAdminProvider>,
    )
    await user.type(
      await screen.findByLabelText(zhCN.invitations.issue.emailLabel),
      'new@example.com',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.invitations.issue.submit }),
    )
    // The one-time token reaches the operator as the host's acceptance link,
    // with the copy control the platform console's page renders.
    expect(
      await screen.findByText(
        zhCN.invitations.issued.link.replace(
          '{{email}}',
          'new@example.com',
        ),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        `${window.location.origin}/invitations/spd_inv_test_token`,
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.invitations.issued.copyLink }),
    ).toBeInTheDocument()
  })
})

describe('OrganizationAdminProvider', () => {
  it('publishes the organization contexts and renders no frame of its own', () => {
    const { backend } = recordingBackend()
    function Probe() {
      const { tenant } = useOrganizationAdminContext()
      const tenantFromHook = useOrganizationAdminTenant()
      const protectedRoles = useOrganizationAdminProtectedRoles()
      const hasFeature = useAdminHasFeature()
      const routes = useAdminRouteTable()
      return (
        <div>
          <span data-testid="tenant">{tenant}</span>
          <span data-testid="tenant-hook">{tenantFromHook}</span>
          <span data-testid="roles">{protectedRoles.join(',')}</span>
          <span data-testid="feature">
            {hasFeature('authn.admin') ? 'present' : 'absent'}
          </span>
          <span data-testid="routes">
            {routes.map((route) => route.id).join(',')}
          </span>
        </div>
      )
    }
    const { container } = renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={(name) => name === 'authn.admin'}
        routes={organizationAdminRoutes}
        protectedRoles={['tenant-admin']}
      >
        <Probe />
      </OrganizationAdminProvider>,
    )
    expect(screen.getByTestId('tenant')).toHaveTextContent(TENANT)
    expect(screen.getByTestId('tenant-hook')).toHaveTextContent(TENANT)
    expect(screen.getByTestId('roles')).toHaveTextContent('tenant-admin')
    expect(screen.getByTestId('feature')).toHaveTextContent('present')
    expect(screen.getByTestId('routes')).toHaveTextContent(
      'organization-invitations,organization-sso,organization-roles,organization-settings',
    )
    // The provider adds no frame: no main landmark and no navigation of its
    // own appear around the probe.
    expect(container.querySelector('main')).toBeNull()
    expect(container.querySelector('nav')).toBeNull()
  })

  it('throws outside the provider rather than issuing a request with no tenant', () => {
    function Bare(): null {
      useOrganizationAdminContext()
      return null
    }
    // React logs the render error; the assertion is the throw itself.
    expect(() => render(<Bare />)).toThrow(/OrganizationAdminProvider/)
  })

  it('publishes the organization breadcrumb trail only where it is installed', () => {
    const { backend } = recordingBackend()
    function Trail() {
      const breadcrumbs = useOrganizationAdminBreadcrumbs()
      return (
        <span data-testid="trail">
          {breadcrumbs === undefined
            ? 'none'
            : breadcrumbs.map((crumb) => String(crumb.label)).join('>')}
        </span>
      )
    }
    const { unmount } = renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={createAdminOperations(backend)}
        hasFeature={noFeatures}
        breadcrumbs={[{ label: 'Organization' }, { label: 'Invitations' }]}
      >
        <Trail />
      </OrganizationAdminProvider>,
    )
    expect(screen.getByTestId('trail')).toHaveTextContent(
      'Organization>Invitations',
    )
    unmount()
    renderWithProviders(
      <OrganizationAdminBreadcrumbProvider breadcrumbs={[{ label: 'Only' }]}>
        <Trail />
      </OrganizationAdminBreadcrumbProvider>,
    )
    expect(screen.getByTestId('trail')).toHaveTextContent('Only')
  })

  it('ships a tenant-scoped operations bundle with no platform member', () => {
    expect(Object.keys(organizationAdminOperations).sort()).toEqual(
      [
        'useClaimSsoDomain',
        'useCreateSsoProvider',
        'useDeleteSsoProvider',
        'useInvitations',
        'useIssueInvitation',
        'useMembers',
        'useRefreshSsoMetadata',
        'useRemoveMember',
        'useRemoveSsoDomain',
        'useReverifySsoDomains',
        'useRevokeInvitation',
        'useSetMemberSsoExempt',
        'useSsoProvider',
        'useSsoProviders',
        'useTenantAccountBindings',
        'useTenantBindRole',
        'useTenantBindings',
        'useTenantGrantPermission',
        'useTenantGrants',
        'useTenantOwnerCancel',
        'useTenantOwnerDelete',
        'useTenantOwnerPending',
        'useTenantOwnerRequest',
        'useTenantPermissions',
        'useTenantProfile',
        'useTenantProfileWrite',
        'useTenantRemoveRole',
        'useTenantRevokePermission',
        'useTenantRoleBindings',
        'useTenantRoles',
        'useTenantSettingsList',
        'useTenantSettingsRemove',
        'useTenantSettingsWrite',
        'useTenantUnbindRole',
        'useTenantWriteRole',
        'useUpdateSsoProvider',
        'useVerifySsoDomain',
        'useWriteMember',
      ].sort(),
    )
    // No platform roster member rides along.
    expect(organizationAdminOperations).not.toHaveProperty('useTenants')
  })
})

/**
 * The organization pages through the shipped generated bundle, with a request
 * seam that records every URL the render issued. The assertion is the
 * surface's defining promise: an organization page never reaches a platform
 * asset, and every tenant-addressed read names the current tenant. The full
 * `adminOperations` bundle is mounted deliberately -- the page must not
 * issue the platform account reads that bundle carries, so the request
 * record, not the bundle's shape, is what proves the scoping.
 */
describe('the organization pages address only the current tenant', () => {
  interface Recorded {
    readonly paths: readonly string[]
  }

  const ACCOUNT = {
    id: 'acc-1',
    kind: 'user',
    status: 'active',
    display_name: 'Ada Lovelace',
    mfa_required: false,
    protected: false,
    password_set: true,
    attributes: {},
    identifiers: [],
    created_at: '2026-01-01T00:00:00Z',
  }
  const MEMBER = {
    account: 'acc-1',
    tenant: TENANT,
    active: true,
    display_name: 'Ada Lovelace',
    sso_exempt: false,
    source: 'admin',
    roles: [],
    joined_at: '2026-10-15T12:00:00Z',
  }
  const INVITATION = {
    id: 'invite-1',
    tenant: TENANT,
    email: 'invitee@example.com',
    invited_by: 'acc-1',
    status: 'pending',
    created_at: '2026-01-01T00:00:00Z',
    expires_at: '2027-01-01T00:00:00Z',
    accepted_at: '0001-01-01T00:00:00Z',
  }
  const PROVIDER = {
    id: 'provider-1',
    tenant: TENANT,
    name: 'Example IdP',
    protocol: 'oidc',
    enabled: true,
    provisioning: 'jit',
    allow_idp_initiated: false,
    allow_sha1: false,
    merge_existing: false,
    trust_idp_mfa: false,
    version: 1,
    domains: [],
  }

  /**
   * Binds a request seam that answers every read either surface may issue.
   * The platform account reads are answered on purpose: a page that regresses
   * to them renders rather than failing to mount, so the recorded URLs show
   * the platform path instead of hiding it behind a crash.
   */
  function recordRequests(): Recorded {
    const paths: string[] = []
    bindAdminRequest((call) => {
      paths.push(call.path)
      if (call.path === `/api/v1/tenants/${TENANT}/invitations`) {
        return { invitations: [INVITATION], next_cursor: '' }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/members`) {
        return { members: [MEMBER], next_cursor: '' }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/roles`) {
        return { roles: [] }
      }
      if (call.path === `/api/v1/tenants/${TENANT}/sso/providers`) {
        return { providers: [PROVIDER] }
      }
      if (call.path === '/api/v1/system/auth/accounts') {
        return { accounts: [ACCOUNT], next_cursor: '' }
      }
      if (call.path.startsWith('/api/v1/system/auth/accounts/')) {
        return { account: ACCOUNT }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    return { paths }
  }

  function renderPage(page: 'invitations' | 'sso'): Recorded {
    const recorded = recordRequests()
    renderWithProviders(
      <OrganizationAdminProvider
        tenant={TENANT}
        operations={fullOrganizationBundle}
        hasFeature={() => true}
        routes={organizationAdminRoutes}
      >
        {page === 'invitations' ? (
          <OrganizationInvitationsPage />
        ) : (
          <OrganizationSsoPage />
        )}
      </OrganizationAdminProvider>,
    )
    return recorded
  }

  it('reads the invitations and members of the current tenant, never a platform account', async () => {
    const { paths } = renderPage('invitations')
    // The member read's display name is the identity, so a resolved row is
    // proof the member read settled. It appears in the name column and in
    // each identity cell that resolves the same account.
    expect((await screen.findAllByText('Ada Lovelace')).length).toBeGreaterThan(0)
    // The recorded set is exactly the tenant's three reads: the member and
    // invitation lists, and the role read behind the member section's role
    // filter. The member write form's platform account list is not mounted,
    // and the identities cost no platform account read.
    expect([...paths].sort()).toEqual([
      `/api/v1/tenants/${TENANT}/invitations`,
      `/api/v1/tenants/${TENANT}/members`,
      `/api/v1/tenants/${TENANT}/roles`,
    ])
    // The surface's defining promise: no platform asset is read.
    expect(paths.filter((path) => path.startsWith('/api/v1/system'))).toEqual([])
    // Every tenant-addressed read names the tenant the host supplied.
    for (const path of paths) {
      expect(path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
    }
  })

  it('reads the SSO providers of the current tenant, never a platform asset', async () => {
    const { paths } = renderPage('sso')
    expect((await screen.findAllByText('Example IdP')).length).toBeGreaterThan(0)
    expect(paths).toEqual([`/api/v1/tenants/${TENANT}/sso/providers`])
    expect(paths.filter((path) => path.startsWith('/api/v1/system'))).toEqual([])
    for (const path of paths) {
      expect(path.startsWith(`/api/v1/tenants/${TENANT}/`)).toBe(true)
    }
  })
})
