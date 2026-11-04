/**
 * The workspace admin area in the demo host: its entry derivation and its
 * route admission.
 *
 * The entry (navigation parent and home card) is one derivation over the
 * feature-narrowed workspace declarations and the tenant-domain permission
 * set scoped to the current tenant. These cases drive the real composition:
 * the app under the production provider stack, the caller tenant read and
 * the permission read scripted at the api-sdk request seam.
 *
 * The scoping rule is the point of several cases: the permission set is a
 * pair with the tenant it was read for. A pending read, a failed one, and a
 * set still tagged with the tenant the viewer just left all fail closed --
 * no entry and no page -- rather than pairing the new tenant with the old
 * tenant's permissions.
 */

import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import type { RequestFn } from '@speed/api-client'
import { ADMIN_UI_NAMESPACE, adminUiResources } from '@speed/admin-ui'
import { createSpeedI18n } from '@speed/product-shell/bootstrap'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { PlatformPermissions } from './platform-permissions.js'
import {
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { clearSelfTenants } from './tenant-self.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'
import type { TestSession } from './test-utils/session.js'

const SELF_TENANTS_PATH = '/api/v1/auth/me/tenants'
const PERMISSIONS_PATH = '/api/v1/authz/me/permissions'

const tAdmin = createSpeedI18n({
  supportedLanguages: ['en-US', 'zh-CN'],
  defaultLanguage: 'en-US',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
  namespaces: [{ namespace: ADMIN_UI_NAMESPACE, resources: adminUiResources }],
}).getFixedT('en-US', ADMIN_UI_NAMESPACE)

/** The features this surface's declarations need, and nothing else: the
 * notification surfaces stay out, so their reads never join the script. */
const WORKSPACE_FEATURES = readyFeatures(
  'authn.admin',
  'authn.invitations',
  'authn.admin.sso',
  'authz.tenantadmin',
  'settings.tenantadmin',
)

interface TenantRow {
  readonly id: string
  readonly alias: string
  readonly kind: 'business' | 'personal'
}

interface PermissionAnswer {
  readonly platform: string[]
  readonly tenant: string[]
}

/** One row of the tenant roles read, in the shape the generated view carries. */
interface TenantRoleRow {
  readonly role: string
  readonly builtin: boolean
  readonly tenant: string
  readonly description_key: string
  readonly created_at: string
}

/** A non-builtin tenant role: the host's protected mark, not the server's,
 * is what must lock the row this suite renders. */
function tenantRole(role: string): TenantRoleRow {
  return {
    role,
    builtin: false,
    tenant: 'acme',
    description_key: '',
    created_at: '2026-10-01T00:00:00Z',
  }
}

type PermissionsFor = (
  alias: string | null,
) => PermissionAnswer | Promise<PermissionAnswer>

/** Scripts the reads the entry and the two pages depend on, recording every
 * path asked for. */
function scriptReads(options: {
  readonly tenants: readonly TenantRow[]
  /** Whether the caller tenant read refuses instead of answering. */
  readonly tenantsFailed?: boolean
  /** Answers the caller tenant read instead of the static list or failure. */
  readonly tenantsRead?: () => Promise<{ readonly tenants: readonly TenantRow[] }>
  readonly permissionsFor: PermissionsFor
  /** The rows the tenant roles read answers; absent leaves it empty. */
  readonly roles?: readonly TenantRoleRow[]
  readonly impersonationGrant?: boolean
}): { readonly requested: string[] } {
  const requested: string[] = []
  const request: RequestFn = <T,>(path: string): Promise<T> => {
    requested.push(path)
    if (path === SELF_TENANTS_PATH) {
      if (options.tenantsRead !== undefined) {
        return options.tenantsRead() as Promise<T>
      }
      return options.tenantsFailed === true
        ? Promise.reject(new Error('tenant read refused'))
        : Promise.resolve({ tenants: options.tenants } as T)
    }
    if (path === PERMISSIONS_PATH) {
      return Promise.resolve(
        options.permissionsFor(getCurrentTenantAlias()),
      ).then((answer) => answer as T)
    }
    if (path === '/api/v1/auth/impersonation') {
      // An in-force grant lets the tenant frame settle a current tenant even
      // while impersonating, so the impersonation case exercises the entry's
      // own condition rather than falling out at an unsettled tenant.
      return options.impersonationGrant === true
        ? Promise.resolve({
            grant: {
              id: 'grant-1',
              target: 'account-2',
              tenant: 'acme',
              started_at: '2026-10-15T12:00:00Z',
              // Near enough to stay within the timer range and far enough
              // not to expire during the case.
              ends_at: new Date(Date.now() + 60_000).toISOString(),
            },
          } as T)
        : Promise.reject(new Error('no impersonation'))
    }
    // The remaining tenant-scoped reads of a rendered page answer empty
    // pages; a page that is not mounted asks nothing.
    if (path.endsWith('/invitations')) {
      return Promise.resolve({ invitations: [], next_cursor: '' } as T)
    }
    if (path.endsWith('/members')) {
      return Promise.resolve({ members: [], next_cursor: '' } as T)
    }
    if (path.endsWith('/sso/providers')) {
      return Promise.resolve({ providers: [], next_cursor: '' } as T)
    }
    // The workspace roles page's tenant-scoped reads. The grants read stays
    // disabled until a role is selected, but it answers here too so a case
    // that drives the selection needs no second script.
    if (path.endsWith('/grants')) {
      return Promise.resolve({ grants: [] } as T)
    }
    if (path.endsWith('/roles')) {
      return Promise.resolve({ roles: options.roles ?? [] } as T)
    }
    if (path.endsWith('/permissions')) {
      return Promise.resolve({ permissions: [] } as T)
    }
    if (path.endsWith('/bindings')) {
      return Promise.resolve({ bindings: [] } as T)
    }
    // The workspace settings page's tenant-layer read.
    if (path.endsWith('/settings')) {
      return Promise.resolve([] as T)
    }
    return Promise.reject(new Error(`unexpected request: ${path}`))
  }
  bindRequestFn(request)
  return { requested }
}

/** Lets the scripted reads resolve and React commit the settlement they
 * cause, so an absence assertion runs against a settled tree rather than a
 * pending one. */
async function flushReads(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

/** Mounts the app at `path` under the production permission attach. */
function mountApp(options: {
  readonly path?: string
  readonly tenants: readonly TenantRow[]
  readonly tenantsFailed?: boolean
  /** Answers the caller tenant read instead of the static list or failure. */
  readonly tenantsRead?: () => Promise<{ readonly tenants: readonly TenantRow[] }>
  readonly permissionsFor: PermissionsFor
  /** The rows the tenant roles read answers; absent leaves it empty. */
  readonly roles?: readonly TenantRoleRow[]
  readonly impersonating?: boolean
  readonly impersonationGrant?: boolean
  /** Mount the app with no session, as a fresh visitor would arrive. */
  readonly anonymous?: boolean
  /** The feature document to mount; the workspace surface's features by default. */
  readonly features?: ReturnType<typeof authenticatedSnapshot>['features']
}): { readonly requested: string[]; readonly session: TestSession } {
  const target = options.path ?? '/'
  window.history.replaceState({}, '', target)
  const { requested } = scriptReads(options)
  const snapshot = authenticatedSnapshot({
    features: options.features ?? WORKSPACE_FEATURES,
  })
  const session =
    options.anonymous === true
      ? createTestSession()
      : createTestSession(
          options.impersonating === true
            ? { ...snapshot, impersonating: true }
            : snapshot,
        )
  renderDemoApp({
    session,
    wrap: (app) => (
      <PlatformPermissions session={session}>{app}</PlatformPermissions>
    ),
  })
  return { requested, session }
}

/** The navigation parent and the home card, both named by the demo
 * namespace. */
function workspaceEntry() {
  const nav = screen.queryByRole('button', {
    name: demoEnUS.nav.workspaceAdmin,
  })
  const card = within(screen.getByRole('main')).queryByText(
    demoEnUS.home.cards.workspaceAdmin.title,
  )
  return { nav, card }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId('acme')
  setTenantAliases(new Map([['acme', 'acme']]))
  clearSelfTenants()
})

describe('the workspace admin entry', () => {
  it('appears for a business tenant that grants a workspace permission', async () => {
    mountApp({
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    expect(
      await screen.findByRole('button', {
        name: demoEnUS.nav.workspaceAdmin,
      }),
    ).toBeInTheDocument()
    expect(
      within(screen.getByRole('main')).getByText(
        demoEnUS.home.cards.workspaceAdmin.title,
      ),
    ).toBeInTheDocument()
  })

  it('does not appear for a personal tenant', async () => {
    setCurrentTenantId('personal-account-1')
    setTenantAliases(new Map([['personal-account-1', 'personal-account-1']]))
    mountApp({
      tenants: [
        {
          id: 'personal-account-1',
          alias: 'personal-account-1',
          kind: 'personal',
        },
      ],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    await screen.findByText(demoEnUS.home.heading)
    await flushReads()
    expect(workspaceEntry().nav).toBeNull()
    expect(workspaceEntry().card).toBeNull()
  })

  it('does not appear for the system tenant', async () => {
    setCurrentTenantId('system')
    setTenantAliases(new Map([['system', 'system']]))
    mountApp({
      tenants: [],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    await screen.findByText(demoEnUS.home.heading)
    await flushReads()
    expect(workspaceEntry().nav).toBeNull()
    expect(workspaceEntry().card).toBeNull()
  })

  it('does not appear while the session impersonates another account', async () => {
    mountApp({
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
      impersonating: true,
      impersonationGrant: true,
    })
    // The grant settles the current tenant, so the surface has everything it
    // would need but for the impersonation itself.
    await screen.findByText(demoEnUS.home.heading)
    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
    await flushReads()
    expect(workspaceEntry().nav).toBeNull()
    expect(workspaceEntry().card).toBeNull()
  })

  it('does not appear while the tenant permission read is pending', async () => {
    const { requested } = mountApp({
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      // A read that never answers: the entry must wait, not guess.
      permissionsFor: () => new Promise<PermissionAnswer>(() => undefined),
    })
    await screen.findByText(demoEnUS.home.heading)
    await waitFor(() => expect(requested).toContain(PERMISSIONS_PATH))
    await flushReads()
    expect(workspaceEntry().nav).toBeNull()
    expect(workspaceEntry().card).toBeNull()
  })

  it('hides the entry while the set still belongs to the tenant just left', async () => {
    const user = userEvent.setup()
    let settleGlobex: (value: PermissionAnswer) => void = () => undefined
    const { requested } = mountApp({
      tenants: [
        { id: 'acme', alias: 'acme', kind: 'business' },
        { id: 'globex', alias: 'globex', kind: 'business' },
      ],
      permissionsFor: (alias) =>
        alias === 'acme'
          ? { platform: [], tenant: ['authn.membership.manage'] }
          : new Promise<PermissionAnswer>((resolve) => {
              settleGlobex = resolve
            }),
    })
    expect(
      await screen.findByRole('button', {
        name: demoEnUS.nav.workspaceAdmin,
      }),
    ).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))

    // The switch clears the tenant domain and the read for globex has not
    // answered: the old tenant's set must not stand in for the new tenant.
    await waitFor(() => expect(getCurrentTenantId()).toBe('globex'))
    await waitFor(() =>
      expect(
        screen.queryByRole('button', { name: demoEnUS.nav.workspaceAdmin }),
      ).toBeNull(),
    )

    settleGlobex({ platform: [], tenant: ['authn.membership.manage'] })
    expect(
      await screen.findByRole('button', {
        name: demoEnUS.nav.workspaceAdmin,
      }),
    ).toBeInTheDocument()
    expect(
      requested.filter((path) => path === PERMISSIONS_PATH),
    ).toHaveLength(2)
  })
})

describe('the workspace admin route admission', () => {
  it('renders a page only with the permission scoped to the current tenant', async () => {
    const { requested } = mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.membership.manage'] }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/invitations')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('sends the viewer to the first page the tenant admits when the page code is unheld', async () => {
    const { requested } = mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      // SSO is openable; invitations is not. The guard redirects to the
      // first openable declaration.
      permissionsFor: () => ({ platform: [], tenant: ['authn.idp.manage'] }),
    })
    await waitFor(() =>
      expect(window.location.pathname).toBe('/workspace/admin/sso'),
    )
    expect(
      await screen.findByRole('heading', { name: tAdmin('sso.title') }),
    ).toBeInTheDocument()
    // The unheld page's reads were never issued.
    expect(
      requested.some((path) => path.endsWith('/invitations')),
    ).toBe(false)
  })

  it('remounts the surface under the new tenant after a switch', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [
        { id: 'acme', alias: 'acme', kind: 'business' },
        { id: 'globex', alias: 'globex', kind: 'business' },
      ],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    const email = await screen.findByLabelText(
      tAdmin('invitations.issue.emailLabel'),
    )
    await user.type(email, 'draft@example.com')

    // A switch inside the workspace area takes the viewer home; the page
    // unmounts with its draft.
    await user.click(screen.getByRole('button', { name: 'acme' }))
    await user.click(await screen.findByRole('menuitem', { name: 'globex' }))
    await waitFor(() => expect(window.location.pathname).toBe('/'))

    // Re-entering under the new tenant starts a fresh page: the draft did
    // not cross the switch. The parent entry starts expanded, so its child
    // links are already in view.
    expect(
      await screen.findByRole('button', {
        name: demoEnUS.nav.workspaceAdmin,
      }),
    ).toBeInTheDocument()
    await user.click(
      await screen.findByRole('link', {
        name: tAdmin('workspace.nav.invitations'),
      }),
    )
    expect(
      await screen.findByLabelText(tAdmin('invitations.issue.emailLabel')),
    ).toHaveValue('')
  })

  it('renders a retryable failure page with a heading when the caller tenant read fails', async () => {
    const user = userEvent.setup()
    const { requested } = mountApp({
      path: '/workspace/admin/invitations',
      tenants: [],
      tenantsFailed: true,
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    // A failed read is not a pending one: the surface must not spin forever.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.workspace.accessFailed.title,
      }),
    ).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(
      screen.getByRole('button', {
        name: demoEnUS.workspace.accessFailed.retry,
      }),
    ).toBeInTheDocument()

    // The retry re-issues the read that failed rather than leaving the page
    // stuck on a failure the viewer cannot clear.
    const before = requested.filter((path) => path === SELF_TENANTS_PATH).length
    await user.click(
      screen.getByRole('button', {
        name: demoEnUS.workspace.accessFailed.retry,
      }),
    )
    await waitFor(() =>
      expect(
        requested.filter((path) => path === SELF_TENANTS_PATH).length,
      ).toBeGreaterThan(before),
    )
  })

  it('renders the failure page when the permission read fails instead of sending the viewer home', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => Promise.reject(new Error('permission read refused')),
    })
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.workspace.accessFailed.title,
      }),
    ).toBeInTheDocument()
    // The denied redirect is reserved for a known empty set: an unknown one
    // stays on the workspace URL with a retry.
    expect(window.location.pathname).toBe('/workspace/admin/invitations')
    expect(
      screen.getByRole('button', {
        name: demoEnUS.workspace.accessFailed.retry,
      }),
    ).toBeInTheDocument()
  })

  it('renders the failure page when the feature document read failed', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      features: { state: 'failed', error: new Error('feature read refused') },
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    // A failed feature document is unknown, not a denial: the workspace
    // admission renders its retryable failure rather than redirecting a
    // possible manager home.
    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: demoEnUS.workspace.accessFailed.title,
      }),
    ).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent(
      demoEnUS.workspace.accessFailed.title,
    )
    await flushReads()
    expect(workspaceEntry().nav).toBeNull()
    expect(workspaceEntry().card).toBeNull()
    expect(window.location.pathname).toBe('/workspace/admin/invitations')
  })

  it('disables the retry while its reads are in flight', async () => {
    const user = userEvent.setup()
    let settleRetry: (value: {
      readonly tenants: readonly TenantRow[]
    }) => void = () => undefined
    let calls = 0
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [],
      tenantsRead: () => {
        calls += 1
        if (calls === 1) {
          return Promise.reject(new Error('tenant read refused'))
        }
        return new Promise((resolve) => {
          settleRetry = resolve
        })
      },
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    const retry = await screen.findByRole('button', {
      name: demoEnUS.workspace.accessFailed.retry,
    })

    await user.click(retry)

    // The reads the retry started are still settling: a second click would
    // start a competing caller-tenant read, so the control is disabled.
    expect(retry).toBeDisabled()

    await act(async () => {
      settleRetry({
        tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      })
    })
    // The retry settles the admission into the page it retried for.
    expect(
      await screen.findByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeInTheDocument()
  })

  it('still sends a personal tenant direct visit to the tenant home', async () => {
    setCurrentTenantId('personal-account-1')
    setTenantAliases(new Map([['personal-account-1', 'personal-account-1']]))
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [
        {
          id: 'personal-account-1',
          alias: 'personal-account-1',
          kind: 'personal',
        },
      ],
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        name: demoEnUS.workspace.accessFailed.title,
      }),
    ).toBeNull()
  })

  it('still sends the system tenant direct visit to the tenant home', async () => {
    setCurrentTenantId('system')
    setTenantAliases(new Map([['system', 'system']]))
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [],
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        name: demoEnUS.workspace.accessFailed.title,
      }),
    ).toBeNull()
  })

  it('still sends an impersonating viewer direct visit to the tenant home', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
      impersonating: true,
      impersonationGrant: true,
    })
    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
    expect(
      screen.queryByRole('heading', {
        name: demoEnUS.workspace.accessFailed.title,
      }),
    ).toBeNull()
  })

  it('still denies an anonymous direct visit', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [],
      permissionsFor: () => ({ platform: [], tenant: [] }),
      anonymous: true,
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(
      await screen.findByRole('heading', { name: demoEnUS.signIn.heading }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('denies an ordinary member with no workspace permission', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      // A known, empty tenant permission set: a member, not a fault.
      permissionsFor: () => ({ platform: [], tenant: [] }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    await flushReads()
    expect(workspaceEntry().nav).toBeNull()
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('denies the surface when no workspace declaration is served', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      // The feature document is ready but serves none of the workspace
      // declarations: narrowing leaves no page to admit.
      features: readyFeatures(),
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(workspaceEntry().nav).toBeNull()
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('does not admit the page while the feature document is not ready', async () => {
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      features: { state: 'pending' },
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    await flushReads()
    // An unsettled feature document admits nothing: no entry, no page.
    expect(workspaceEntry().nav).toBeNull()
    expect(
      screen.queryByRole('heading', { name: tAdmin('invitations.title') }),
    ).toBeNull()
  })

  it('serves the workspace SSO page without reading any platform asset', async () => {
    const { requested } = mountApp({
      path: '/workspace/admin/sso',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({ platform: [], tenant: ['authn.idp.manage'] }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('sso.title') }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/sso/providers')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('serves the workspace roles page from the tenant namespace only', async () => {
    const { requested } = mountApp({
      path: '/workspace/admin/roles',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authz.tenant.role.read'],
      }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('permissions.title') }),
    ).toBeInTheDocument()
    // Every read names the current tenant.
    expect(requested).toContain('/api/v1/tenants/acme/roles')
    expect(requested).toContain('/api/v1/tenants/acme/permissions')
    expect(requested).toContain('/api/v1/tenants/acme/bindings')
    // The page never reaches a platform asset.
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('renders a host-configured protected role read-only', async () => {
    // tenant-admin is the demo host's protected role (the seed writes and
    // converges it). The read marks it non-builtin on purpose: the lock must
    // come from the host's protectedRoles prop, not the server's builtin
    // mark, so a page that dropped the prop would show its write controls.
    mountApp({
      path: '/workspace/admin/roles',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      roles: [tenantRole('tenant-admin'), tenantRole('editor')],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authz.tenant.role.read'],
      }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('permissions.title') }),
    ).toBeInTheDocument()
    expect(await screen.findByText('tenant-admin')).toBeInTheDocument()

    // The protected row keeps only the read-only grants entry.
    expect(
      screen.getByRole('button', {
        name: tAdmin('permissions.roles.viewGrantsAria', {
          role: 'tenant-admin',
        }),
      }),
    ).toBeEnabled()
    expect(
      screen.queryByRole('button', {
        name: tAdmin('permissions.roles.manageGrantsAria', {
          role: 'tenant-admin',
        }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('button', {
        name: tAdmin('permissions.roles.removeAria', { role: 'tenant-admin' }),
      }),
    ).toBeNull()
    expect(
      screen.getByText(tAdmin('permissions.roles.protectedReadOnly')),
    ).toBeInTheDocument()

    // An ordinary tenant role keeps its write controls, so the absence above
    // distinguishes the host's protected mark from an empty action column.
    expect(
      screen.getByRole('button', {
        name: tAdmin('permissions.roles.manageGrantsAria', { role: 'editor' }),
      }),
    ).toBeEnabled()
    expect(
      screen.getByRole('button', {
        name: tAdmin('permissions.roles.removeAria', { role: 'editor' }),
      }),
    ).toBeEnabled()
  })

  it('serves the workspace settings page from the tenant namespace only', async () => {
    const { requested } = mountApp({
      path: '/workspace/admin/settings',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['settings.tenant.read'],
      }),
    })
    expect(
      await screen.findByRole('heading', { name: tAdmin('settings.title') }),
    ).toBeInTheDocument()
    expect(requested).toContain('/api/v1/tenants/acme/settings')
    // Neither the platform settings read nor any other platform asset is
    // issued by the tenant settings page.
    expect(requested).not.toContain('/api/v1/system/settings')
    expect(requested.some((path) => path.startsWith('/api/v1/system'))).toBe(
      false,
    )
  })

  it('sends a viewer without the roles code to the first page they may open', async () => {
    const { requested } = mountApp({
      path: '/workspace/admin/roles',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      // Settings is openable and roles is not: the page guard redirects to
      // the admission's first enterable declaration.
      permissionsFor: () => ({
        platform: [],
        tenant: ['settings.tenant.read'],
      }),
    })
    await waitFor(() =>
      expect(window.location.pathname).toBe('/workspace/admin/settings'),
    )
    expect(
      await screen.findByRole('heading', { name: tAdmin('settings.title') }),
    ).toBeInTheDocument()
    // The denied page's reads were never issued.
    expect(requested).not.toContain('/api/v1/tenants/acme/roles')
  })

  it('routes the workspace breadcrumb Home in place', async () => {
    const user = userEvent.setup()
    mountApp({
      path: '/workspace/admin/invitations',
      tenants: [{ id: 'acme', alias: 'acme', kind: 'business' }],
      permissionsFor: () => ({
        platform: [],
        tenant: ['authn.membership.manage'],
      }),
    })
    await screen.findByRole('heading', { name: tAdmin('invitations.title') })
    // The crumb is the one in the page header's breadcrumb landmark, not the
    // navigation entry that shares its label.
    const trail = within(
      screen.getByRole('navigation', { name: 'Breadcrumb' }),
    )
    await user.click(trail.getByRole('link', { name: demoEnUS.nav.home }))
    await waitFor(() => expect(window.location.pathname).toBe('/'))
    expect(await screen.findByText(demoEnUS.home.heading)).toBeInTheDocument()
  })
})
