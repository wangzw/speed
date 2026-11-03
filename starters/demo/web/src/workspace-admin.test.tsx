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

type PermissionsFor = (
  alias: string | null,
) => PermissionAnswer | Promise<PermissionAnswer>

/** Scripts the reads the entry and the two pages depend on, recording every
 * path asked for. */
function scriptReads(options: {
  readonly tenants: readonly TenantRow[]
  readonly permissionsFor: PermissionsFor
  readonly impersonationGrant?: boolean
}): { readonly requested: string[] } {
  const requested: string[] = []
  const request: RequestFn = <T,>(path: string): Promise<T> => {
    requested.push(path)
    if (path === SELF_TENANTS_PATH) {
      return Promise.resolve({ tenants: options.tenants } as T)
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
  readonly permissionsFor: PermissionsFor
  readonly impersonating?: boolean
  readonly impersonationGrant?: boolean
}): { readonly requested: string[]; readonly session: TestSession } {
  const target = options.path ?? '/'
  window.history.replaceState({}, '', target)
  const { requested } = scriptReads(options)
  const snapshot = authenticatedSnapshot({ features: WORKSPACE_FEATURES })
  const session = createTestSession(
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
})
