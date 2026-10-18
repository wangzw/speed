/**
 * The host-side permission-gating composition over the product shell, as
 * package evidence.
 *
 * ProductShell itself gates nothing: it is the view machine between a
 * session and the AppShell frame, and route-level authorization is a
 * host concern composed inside its children (layout-kit's RouteGuard fed
 * by a status the host derives, auth-core's permission lists the host
 * attaches). This suite is the packaged proof that the composition works
 * end to end over the pieces the other packages ship -- the fixture host
 * below plays exactly that host role:
 *
 *   - the shell's authenticated frame with the tenant switcher
 *     (@speed/tenancy-ui) in the userMenu;
 *   - one destination per capability, each wrapped in layout-kit's
 *     RouteGuard with a status the host computes on every render from
 *     auth-core's usePermission over the lists it attached with
 *     session.setPermissionSet (never fetched or evaluated here);
 *   - the host's own role load: the fixture simulates the /me re-fetch a
 *     real host runs after it switches tenants (the tenant is the host's
 *     own state, mounted on auth-core's TenantProvider), resolving it at
 *     the moment each journey wants the gate to lift -- each tenant's
 *     list is attached only once its load lands, so the route falls back
 *     to the pending gate and stays closed until then;
 *   - a "Refresh roles" re-fetch that keeps the attached list while it
 *     is in flight, so the denied gate re-renders without re-entering
 *     (RouteGuard fires onDenied once per transition INTO denied, never
 *     per render).
 *
 * Journey 1 drives the gating across the tenancy-switch journey: an
 * allowed destination for one tenant's lists becomes pending the moment
 * the host switches tenants (the new tenant's list has not arrived yet),
 * resolves to allowed or denied when the new tenant's list lands, and
 * fires onDenied exactly once per denied spell, two renders within one
 * spell included. Journey 2 drives a server-side session death to the
 * same convergence the other suites prove: a refused /me whose refresh is
 * also refused signs the session out locally, ProductShell replaces the
 * frame with its session-ended screen, and signing in again returns to
 * the frame.
 *
 * Both journeys run over the real-client rig (the same bound client and
 * genuine Response objects the usage example uses), pinning the requests
 * in order. The fixture host renders English host content (nav labels,
 * view text, tenant names, its own buttons) -- test-file data standing in
 * for a host's own content, exempt from the no-literal-text rule -- while
 * every assertion on built-in strings imports the shipped sibling locale
 * bundles, never inline translations.
 */

import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Box, Button } from '@mui/material'
import { describe, expect, it, vi } from 'vitest'
import { registerNamespace } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type { AppShellNavItem, RouteGuardStatus } from '@speed/layout-kit'
import { EmptyState } from '@speed/ui-kit'
import {
  TENANCY_UI_NAMESPACE,
  tenancyUiResources,
  TenantSwitcher,
} from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'
import {
  attachSession,
  TenantProvider,
  useCurrentTenant,
  usePermission,
} from '@speed/auth-core'
import type { AuthSession } from '@speed/auth-core'
import { SignInScreen } from '@speed/auth-ui'
import { authnProfileGet } from '@speed/api-sdk'
import authUiZhCN from '../../auth-ui/src/locales/zh-CN.json' with { type: 'json' }
import layoutKitZhCN from '../../layout-kit/src/locales/zh-CN.json' with { type: 'json' }
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import { expectNoAxeViolations } from '../test-utils/axe.js'
import {
  createProductShellI18n,
  renderWithProviders,
} from '../test-utils/render.js'
import {
  authenticatedCompletion,
  errorResponse,
  jsonResponse,
  makeRealClientRig,
  methodList,
  openFlow,
  principal,
} from '../test-utils/real-client.js'
import { ProductShell } from './components/ProductShell.js'

const FLOWS_START = 'POST /api/v1/auth/flows'
const FLOWS_ANSWER = 'POST /api/v1/auth/flows/answer'
const METHODS = 'GET /api/v1/auth/methods'
const PROFILE = 'GET /api/v1/auth/me'
const FEATURES = 'GET /api/v1/features'
const TOKEN = 'POST /api/v1/auth/token'
const IDENTIFIER = 'alice@example.com'
const PASSWORD = 'password-1'

/** The deployment's method document: the password channel the journeys
 * sign in with. The sign-in surface reads it on mount. */
const METHODS_BODY = methodList([{ name: 'password', kind: 'form' }])

/** The host's tenant roster -- host data, the same fixture the
 * tenancy-ui README demo uses. */
const TENANTS: readonly TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
]

/** Host data: the permission list each tenant's membership implies, the
 * list the fixture host attaches once that tenant's role load lands.
 * tenant-1 can read notes and manage members; tenant-2 can only read
 * notes. */
const TENANT_ROLES = {
  'tenant-1': ['notes:read', 'members:manage'],
  'tenant-2': ['notes:read'],
} as const satisfies Readonly<Record<string, readonly string[]>>

type ViewId = 'home' | 'notes' | 'members'

const VIEWS: readonly { readonly id: ViewId; readonly label: string }[] = [
  { id: 'home', label: 'Overview' },
  { id: 'notes', label: 'Notes' },
  { id: 'members', label: 'Members' },
]

interface GatedContentProps {
  readonly session: AuthSession
  /**
   * The host's role-load queue: every load the fixture host starts (on
   * reaching a tenant whose list has not landed, or on an explicit
   * refresh) registers its resolver here under the tenant id, so each
   * journey resolves the loads at the moment it wants the gate to lift.
   * A real host would fetch /me instead; this map is the seam the journey
   * drives.
   */
  readonly roleLoads: Map<string, (permissions: readonly string[]) => void>
  /** The switcher's onSelect; the fixture host's record of the event. */
  readonly onSelect: (tenantId: string) => void
  /** Every RouteGuard's onDenied; the fixture host's record. */
  readonly onDenied: () => void
}

/**
 * The fixture host's content: the tenant switcher in the userMenu and one
 * gated destination per capability. The current tenant flows from
 * auth-core's TenantProvider (the host mounted it); the host derives each
 * destination's RouteGuardStatus on every render from usePermission over
 * the lists it attached, and re-attaches a tenant's list itself when its
 * role load completes -- the setPermissionSet host duty, played here for
 * real.
 */
function GatedContent({
  session,
  roleLoads,
  onSelect,
  onDenied,
}: GatedContentProps) {
  const current = useCurrentTenant()
  const tenantId = current?.tenantId ?? null
  const canReadNotes = usePermission('tenant', 'notes:read')
  const canManageMembers = usePermission('tenant', 'members:manage')
  const [view, setView] = useState<ViewId>('home')
  // The tenants whose role lists have been attached at least once; a
  // tenant missing from this set is still awaiting its first load, so
  // every gate under it reads pending -- never a stale allow.
  const [settled, setSettled] = useState<ReadonlySet<string>>(
    () => new Set(),
  )
  const [refreshing, setRefreshing] = useState(false)

  // The tenant a role-load resolution must attach for: a switch that
  // commits while the previous tenant's list is still in flight must not
  // attach it to the wrong tenant. Read at resolution time, synced from
  // the current tenant after every render.
  const tenantRef = useRef<string | null>(null)
  useEffect(() => {
    tenantRef.current = tenantId
  }, [tenantId])

  // Reaching a tenant whose list has not landed starts its role load.
  useEffect(() => {
    if (tenantId === null || settled.has(tenantId)) {
      return
    }
    // The tenant this load attaches for, frozen at start: the
    // resolution runs later, when the current tenant may have moved on.
    const tenant = tenantId
    const load = new Promise<readonly string[]>((resolve) => {
      roleLoads.set(tenant, resolve)
    })
    load.then((permissions) => {
      if (tenantRef.current !== tenant) {
        return
      }
      session.setPermissionSet('tenant', permissions)
      setSettled((previous) =>
        previous.has(tenant) ? previous : new Set(previous).add(tenant),
      )
    })
  }, [tenantId, settled, roleLoads, session])

  // The host's re-fetch: it keeps the attached list while the fresh one
  // is in flight, so an allowed or denied gate re-renders without ever
  // leaving its status -- RouteGuard must not re-fire onDenied for it.
  function handleRefreshRoles(): void {
    if (tenantId === null || !settled.has(tenantId)) {
      return
    }
    const tenant = tenantId
    setRefreshing(true)
    const load = new Promise<readonly string[]>((resolve) => {
      roleLoads.set(tenant, resolve)
    })
    load.then((permissions) => {
      if (tenantRef.current === tenant) {
        session.setPermissionSet('tenant', permissions)
      }
      setRefreshing(false)
    })
  }

  // A protected-call probe the host's home view offers: the composed
  // chain a real view runs (a generated operation over the bound
  // client). A refusal triggers the client's silent refresh leg; if the
  // refresh is refused too, the session signs itself out and the shell
  // replaces this whole view -- nothing to render here.
  async function checkSession(): Promise<void> {
    try {
      await authnProfileGet()
    } catch {
      // The session layer already converged; see the module comment.
    }
  }

  function gate(destination: 'notes' | 'members'): RouteGuardStatus {
    if (tenantId === null || !settled.has(tenantId)) {
      return 'pending'
    }
    const can = destination === 'notes' ? canReadNotes : canManageMembers
    return can ? 'allowed' : 'denied'
  }

  const navItems: readonly AppShellNavItem[] = VIEWS.map((entry) => ({
    id: entry.id,
    label: entry.label,
    selected: view === entry.id,
    onClick: () => setView(entry.id),
  }))

  // The denied fallback this host composes is the migrated shape the
  // layout-kit suite prescribes: the page carries a real h1, so the
  // fallback EmptyState declares the headingLevel that continues the
  // page order (h2, under the page h1).
  const deniedFallback = <EmptyState variant="noPermission" headingLevel="h2" />
  let content: ReactNode
  if (view === 'home') {
    content = (
      <>
        <h2>Overview</h2>
        <p>The default landing view, reachable by every member.</p>
        <Button onClick={() => void checkSession()}>Check session</Button>
      </>
    )
  } else if (view === 'notes') {
    content = (
      <RouteGuard status={gate('notes')} onDenied={onDenied} deniedFallback={deniedFallback}>
        <h2>Notes</h2>
        <p>The tenant&apos;s notes list.</p>
      </RouteGuard>
    )
  } else {
    content = (
      <RouteGuard status={gate('members')} onDenied={onDenied} deniedFallback={deniedFallback}>
        <h2>Members</h2>
        <p>The tenant&apos;s member roster.</p>
      </RouteGuard>
    )
  }

  return (
    <ProductShell
      navItems={navItems}
      header="My App"
      signIn={<SignInScreen session={session} />}
      userMenu={
        <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
          <TenantSwitcher
            tenants={TENANTS}
            currentTenantId={tenantId}
            onSelect={onSelect}
          />
        </Box>
      }
    >
      {view !== 'home' && (
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <Button disabled={refreshing} onClick={handleRefreshRoles}>
            Refresh roles
          </Button>
        </Box>
      )}
      {/* The host page's h1, above the view content and the gates: the
          chrome renders no page heading of its own, and every axe scan
          of the composed page needs the real page shape
          (page-has-heading-one is determinate in jsdom -- see the axe
          helper header). */}
      <h1>My app</h1>
      {content}
    </ProductShell>
  )
}

/**
 * The fixture host: it holds the current tenant (its route stand-in
 * here), mounts it on auth-core's TenantProvider, and treats a switch as
 * a change of that state -- exactly the host duty the tenancy-ui README
 * documents.
 */
function GatedTenantApp({
  session,
  roleLoads,
  onSelect,
  onDenied,
}: GatedContentProps) {
  const [tenantId, setTenantId] = useState<string>('tenant-1')
  return (
    <TenantProvider tenantId={tenantId}>
      <GatedContent
        session={session}
        roleLoads={roleLoads}
        onSelect={(id) => {
          onSelect(id)
          setTenantId(id)
        }}
        onDenied={onDenied}
      />
    </TenantProvider>
  )
}

/** Fresh instance with the tenancy-ui namespace added on top of the
 * shell quartet, the five namespaces a host composing the switcher
 * registers. */
function createGatedI18n() {
  const i18n = createProductShellI18n()
  registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
  return i18n
}

/** A password sign-in through the host's own SignInScreen. */
async function signIn(user: ReturnType<typeof userEvent.setup>) {
  const identifier = await screen.findByLabelText(
    authUiZhCN.passwordSignIn.identifierLabel,
  )
  await user.type(identifier, IDENTIFIER)
  await user.type(
    screen.getByLabelText(authUiZhCN.passwordSignIn.passwordLabel),
    PASSWORD,
  )
  await user.click(
    screen.getByRole('button', { name: authUiZhCN.passwordSignIn.submit }),
  )
  await screen.findByRole('main')
}

describe('host-side gating composition', () => {
  it('gates every destination from the session lists across a tenant switch, a denial spell and a role refresh', async () => {
    const user = userEvent.setup()
    const roleLoads = new Map<
      string,
      (permissions: readonly string[]) => void
    >()
    const onSelect = vi.fn<(tenantId: string) => void>()
    const onDenied = vi.fn()
    const rig = makeRealClientRig((call) => {
      const key = `${call.method} ${call.path}`
      if (key === METHODS) {
        return jsonResponse(200, METHODS_BODY)
      }
      if (key === FEATURES) {
        return jsonResponse(200, { features: [] })
      }
      if (key === FLOWS_START) {
        return jsonResponse(
          200,
          openFlow({ state: 'ready', next: { kind: 'complete' } }),
        )
      }
      if (key === FLOWS_ANSWER) {
        return jsonResponse(200, authenticatedCompletion())
      }
      if (key === PROFILE) {
        return jsonResponse(200, principal())
      }
      throw new Error(`unexpected request: ${key}`)
    })
    act(() => attachSession(rig.session))
    renderWithProviders(
      <GatedTenantApp
        session={rig.session}
        roleLoads={roleLoads}
        onSelect={onSelect}
        onDenied={onDenied}
      />,
      { i18n: createGatedI18n() },
    )

    // Sign in through the host's sign-in surface: the frame appears
    // with the tenant switcher showing the host's current tenant, and
    // the first tenant's role load is already in flight (home is exempt
    // from gating, so the frame is usable while it loads).
    await signIn(user)
    expect(
      screen.getByRole('button', { name: 'Sunshine Dental' }),
    ).toBeEnabled()
    expect(roleLoads.has('tenant-1')).toBe(true)
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument()
    expect(onDenied).not.toHaveBeenCalled()

    // Notes: the gate reads pending until the host's first role load
    // lands -- the spinner carries layout-kit's own pending label --
    // then allows the view.
    await user.click(screen.getByRole('button', { name: 'Notes' }))
    expect(
      await screen.findByRole('progressbar', {
        name: layoutKitZhCN.routeGuard.pending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: 'Notes' }),
    ).not.toBeInTheDocument()
    await act(async () => {
      const resolve = roleLoads.get('tenant-1')
      if (resolve === undefined) {
        throw new Error('expected a pending role load for tenant-1')
      }
      resolve(TENANT_ROLES['tenant-1'])
    })
    expect(
      await screen.findByRole('heading', { name: 'Notes' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('progressbar', {
        name: layoutKitZhCN.routeGuard.pending,
      }),
    ).not.toBeInTheDocument()
    expect(onDenied).not.toHaveBeenCalled()

    // Members: allowed for tenant-1's lists (members:manage attached
    // above); the frame is axe-clean in this allowed state.
    await user.click(screen.getByRole('button', { name: 'Members' }))
    expect(
      await screen.findByRole('heading', { name: 'Members' }),
    ).toBeInTheDocument()
    await expectNoAxeViolations()

    // The host switches tenants (its own route change): tenant-2's list
    // has not landed yet, so the Members gate falls back to pending and
    // the previously allowed content is gone -- the switcher relabels.
    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Bright Smile Clinic' }),
    )
    expect(
      await screen.findByRole('progressbar', {
        name: layoutKitZhCN.routeGuard.pending,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: 'Members' }),
    ).not.toBeInTheDocument()
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith('tenant-2')
    expect(
      screen.getByRole('button', { name: 'Bright Smile Clinic' }),
    ).toBeEnabled()
    expect(onDenied).not.toHaveBeenCalled()

    // The host's role load for tenant-2 lands with only notes:read --
    // Members is now denied, behind ui-kit's own noPermission
    // EmptyState, and onDenied fired exactly once for the transition.
    await act(async () => {
      const resolve = roleLoads.get('tenant-2')
      if (resolve === undefined) {
        throw new Error('expected a pending role load for tenant-2')
      }
      resolve(TENANT_ROLES['tenant-2'])
    })
    expect(
      await screen.findByText(uiKitZhCN.emptyState.noPermission.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(uiKitZhCN.emptyState.noPermission.description),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: 'Members' }),
    ).not.toBeInTheDocument()
    expect(onDenied).toHaveBeenCalledTimes(1)

    // The denied frame is axe-clean too.
    await expectNoAxeViolations()

    // A role refresh inside the denied spell: the host keeps the
    // attached list while the fresh copy is in flight, so the gate
    // re-renders -- twice -- without ever leaving 'denied', and
    // onDenied does not re-fire.
    await user.click(screen.getByRole('button', { name: 'Refresh roles' }))
    expect(
      screen.getByRole('button', { name: 'Refresh roles' }),
    ).toBeDisabled()
    expect(onDenied).toHaveBeenCalledTimes(1)
    await act(async () => {
      const resolve = roleLoads.get('tenant-2')
      if (resolve === undefined) {
        throw new Error('expected a pending role refresh for tenant-2')
      }
      resolve(TENANT_ROLES['tenant-2'])
    })
    expect(
      await screen.findByRole('button', { name: 'Refresh roles' }),
    ).toBeEnabled()
    expect(
      screen.getByText(uiKitZhCN.emptyState.noPermission.title),
    ).toBeInTheDocument()
    expect(onDenied).toHaveBeenCalledTimes(1)

    // Leaving the denied view and coming back is a fresh denied spell:
    // the guard remounts and onDenied fires again -- once per visit.
    await user.click(screen.getByRole('button', { name: 'Overview' }))
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument()
    expect(onDenied).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Members' }))
    expect(
      await screen.findByText(uiKitZhCN.emptyState.noPermission.title),
    ).toBeInTheDocument()
    expect(onDenied).toHaveBeenCalledTimes(2)

    // Notes, by contrast, is allowed under tenant-2's list.
    await user.click(screen.getByRole('button', { name: 'Notes' }))
    expect(
      await screen.findByRole('heading', { name: 'Notes' }),
    ).toBeInTheDocument()

    // Every request the journey made, pinned in order: the sign-in
    // surface's method read, the login's flow start, its completion
    // answer and the profile it read. The tenant switch is the host's own
    // state change and sends nothing.
    expect(rig.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
      METHODS,
      FLOWS_START,
      FLOWS_ANSWER,
      PROFILE,
      FEATURES,
    ])
  })

  it('converges a server-side session death to the session-ended screen and back into the frame', async () => {
    const user = userEvent.setup()
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    try {
      const roleLoads = new Map<
        string,
        (permissions: readonly string[]) => void
      >()
      const onSelect = vi.fn<(tenantId: string) => void>()
      const onDenied = vi.fn()
      // The profile read is the login completion's and the probe's: the
      // first (the login) answers, the second (the probe) is refused, and
      // the third (the fresh login) answers again.
      let profileCalls = 0
      const rig = makeRealClientRig((call) => {
        const key = `${call.method} ${call.path}`
        if (key === METHODS) {
          return jsonResponse(200, METHODS_BODY)
        }
        if (key === FEATURES) {
          return jsonResponse(200, { features: [] })
        }
        if (key === FLOWS_START) {
          return jsonResponse(
            200,
            openFlow({ state: 'ready', next: { kind: 'complete' } }),
          )
        }
        if (key === FLOWS_ANSWER) {
          return jsonResponse(200, authenticatedCompletion())
        }
        if (key === PROFILE) {
          profileCalls += 1
          return profileCalls === 2
            ? errorResponse(401, 'authn.session_revoked')
            : jsonResponse(200, principal())
        }
        if (key === TOKEN) {
          return errorResponse(401, 'authn.refresh_token_invalid')
        }
        throw new Error(`unexpected request: ${key}`)
      })
      act(() => attachSession(rig.session))
      renderWithProviders(
        <GatedTenantApp
          session={rig.session}
          roleLoads={roleLoads}
          onSelect={onSelect}
          onDenied={onDenied}
        />,
        { i18n: createGatedI18n() },
      )

      // Sign in and reach the frame, then hit the host's protected-call
      // probe: the server refuses the session (authn.session_revoked),
      // the client's silent refresh leg is refused too
      // (authn.refresh_token_invalid), and the session signs itself out
      // locally -- the store clears and ProductShell replaces the whole
      // frame with its default session-ended screen.
      await signIn(user)
      expect(
        screen.getByRole('heading', { name: 'Overview' }),
      ).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Check session' }))
      expect(
        await screen.findByText(authUiZhCN.sessionEnded.title),
      ).toBeInTheDocument()
      expect(
        screen.getByText(authUiZhCN.sessionEnded.description),
      ).toBeInTheDocument()
      expect(screen.queryByRole('banner')).not.toBeInTheDocument()
      // The frame is gone (banner above), but the ended screen is the
      // whole page now: its container IS the page's main landmark
      // (ProductShell renders the ended branch as `main` -- the
      // landmark-one-main rule a page must answer), with the frame's
      // own main unmounted.
      expect(screen.getByRole('main')).toBeInTheDocument()
      expect(rig.store.get()).toBeNull()
      // The client reports the failed refresh through its reporter with
      // the original refusal's code, never the refresh's own.
      await waitFor(() =>
        expect(warn).toHaveBeenCalledWith(
          'access token refresh failed',
          expect.objectContaining({
            status: 401,
            code: 'authn.session_revoked',
          }),
        ),
      )

      // The ended screen's action returns to the host's sign-in view.
      await user.click(
        screen.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
      )
      await screen.findByLabelText(authUiZhCN.passwordSignIn.identifierLabel)

      // A fresh sign-in reaches the frame again.
      await signIn(user)
      expect(
        screen.getByRole('heading', { name: 'Overview' }),
      ).toBeInTheDocument()
      expect(rig.store.get()).toBe('access-1')

      // Every request the journey made, pinned in order: the first
      // sign-in surface's method read, the login's flow start, answer and
      // profile; the probe's refused profile and the credential-less
      // refresh it triggered; then the ended screen's method read and the
      // fresh login's flow start, answer and profile.
      expect(rig.calls.map((call) => `${call.method} ${call.path}`)).toEqual([
        METHODS,
        FLOWS_START,
        FLOWS_ANSWER,
        PROFILE,
        FEATURES,
        PROFILE,
        TOKEN,
        METHODS,
        FLOWS_START,
        FLOWS_ANSWER,
        PROFILE,
        FEATURES,
      ])
    } finally {
      warn.mockRestore()
    }
  })
})
