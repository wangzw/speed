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

const METHODS_BODY = methodList([{ name: 'password', kind: 'form' }])

const TENANTS: readonly TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
]

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
  readonly roleLoads: Map<string, (permissions: readonly string[]) => void>
  readonly onSelect: (tenantId: string) => void
  readonly onDenied: () => void
}

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
  const [settled, setSettled] = useState<ReadonlySet<string>>(
    () => new Set(),
  )
  const [refreshing, setRefreshing] = useState(false)

  const tenantRef = useRef<string | null>(null)
  useEffect(() => {
    tenantRef.current = tenantId
  }, [tenantId])

  useEffect(() => {
    if (tenantId === null || settled.has(tenantId)) {
      return
    }
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

  async function checkSession(): Promise<void> {
    try {
      await authnProfileGet()
    } catch {
      // The session layer already converged.
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
      <h1>My app</h1>
      {content}
    </ProductShell>
  )
}

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

function createGatedI18n() {
  const i18n = createProductShellI18n()
  registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
  return i18n
}

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

    await signIn(user)
    expect(
      screen.getByRole('button', { name: 'Sunshine Dental' }),
    ).toBeEnabled()
    expect(roleLoads.has('tenant-1')).toBe(true)
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument()
    expect(onDenied).not.toHaveBeenCalled()

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

    await user.click(screen.getByRole('button', { name: 'Members' }))
    expect(
      await screen.findByRole('heading', { name: 'Members' }),
    ).toBeInTheDocument()
    await expectNoAxeViolations()

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

    await expectNoAxeViolations()

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

    await user.click(screen.getByRole('button', { name: 'Overview' }))
    expect(screen.getByRole('heading', { name: 'Overview' })).toBeInTheDocument()
    expect(onDenied).toHaveBeenCalledTimes(1)
    await user.click(screen.getByRole('button', { name: 'Members' }))
    expect(
      await screen.findByText(uiKitZhCN.emptyState.noPermission.title),
    ).toBeInTheDocument()
    expect(onDenied).toHaveBeenCalledTimes(2)

    await user.click(screen.getByRole('button', { name: 'Notes' }))
    expect(
      await screen.findByRole('heading', { name: 'Notes' }),
    ).toBeInTheDocument()

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
      expect(screen.getByRole('main')).toBeInTheDocument()
      expect(rig.store.get()).toBeNull()
      await waitFor(() =>
        expect(warn).toHaveBeenCalledWith(
          'access token refresh failed',
          expect.objectContaining({
            status: 401,
            code: 'authn.session_revoked',
          }),
        ),
      )

      await user.click(
        screen.getByRole('button', { name: authUiZhCN.sessionEnded.signInAction }),
      )
      await screen.findByLabelText(authUiZhCN.passwordSignIn.identifierLabel)

      await signIn(user)
      expect(
        screen.getByRole('heading', { name: 'Overview' }),
      ).toBeInTheDocument()
      expect(rig.store.get()).toBe('access-1')

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
