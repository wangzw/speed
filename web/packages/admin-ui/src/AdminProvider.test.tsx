/**
 * AdminProvider: the console's package-level contexts without the frame.
 *
 * The provider installs the same seams AdminShell installs, so a host
 * that renders a platform-staff frame of its own can put console pages
 * inside it. Each case reads one context back through the hook a page
 * uses and checks the supplied value -- or the documented fallback when
 * the prop is absent.
 */

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AdminProvider } from './AdminProvider.js'
import type { AdminRole } from './operations.js'
import { useAdminAccountHref } from './internal/account-href.js'
import { useAdminDescribeRole } from './internal/describe-role.js'
import { useAdminHasFeature } from './internal/features.js'
import { useAdminImpersonationChange } from './internal/impersonation-change.js'
import { useAdminImpersonationStarting } from './internal/impersonation-starting.js'
import { useAdminInvitationLink } from './internal/invitation-link.js'
import { AdminLink } from './internal/navigation.js'
import {
  adminRouteById,
  useAdminRouteTable,
} from './internal/route-table.js'
import { useAdminSession } from './internal/session.js'
import type { AdminRoute } from './route.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createStepUpSession } from '../test-utils/session.js'

const ROUTES: readonly AdminRoute[] = [
  { id: 'tenants', path: '/tenants', view: () => null },
  { id: 'accounts', path: '/accounts', view: () => null },
]

const ROLE: AdminRole = {
  tenant: 'acme',
  role: 'editor',
  builtin: false,
  can_manage: true,
  description: '',
  description_key: 'role.editor',
  permission_count: 0,
  created_at: '2026-10-15T12:00:00Z',
}

/**
 * Dispatches a click and cancels its default action at the document, after
 * React's delegated handler has run: jsdom implements no navigation, and
 * the cancellation keeps an uncancelled link click from logging
 * "Not implemented" without changing what the component saw.
 */
function clickWithoutNavigating(
  element: HTMLElement,
  init: MouseEventInit,
): void {
  const cancel = (event: MouseEvent): void => {
    event.preventDefault()
  }
  document.addEventListener('click', cancel)
  try {
    fireEvent.click(element, init)
  } finally {
    document.removeEventListener('click', cancel)
  }
}

describe('AdminProvider', () => {
  it('publishes the feature answer', () => {
    function Probe() {
      const hasFeature = useAdminHasFeature()
      return <span>{hasFeature('tenant.admin') ? 'present' : 'absent'}</span>
    }
    renderWithProviders(
      <AdminProvider hasFeature={(name) => name === 'tenant.admin'}>
        <Probe />
      </AdminProvider>,
    )
    expect(screen.getByText('present')).toBeInTheDocument()
  })

  it('fails every feature closed without a supplied answer', () => {
    // The provider requires hasFeature; this case pins the fail-closed
    // direction of the context itself (a page rendered without any
    // provider never sees a feature present).
    function Probe() {
      const hasFeature = useAdminHasFeature()
      return <span>{hasFeature('tenant.admin') ? 'present' : 'absent'}</span>
    }
    // `null` mounts no feature provider: the harness's all-present default
    // is a test convenience, and this case is about the context itself.
    renderWithProviders(<Probe />, { hasFeature: null })
    expect(screen.getByText('absent')).toBeInTheDocument()
  })

  it('publishes the session, or null when none is supplied', () => {
    const session = createStepUpSession()
    function Probe() {
      const supplied = useAdminSession()
      return <span>{supplied === null ? 'none' : supplied.getSnapshot().state}</span>
    }
    const { unmount } = renderWithProviders(
      <AdminProvider hasFeature={() => true} session={session}>
        <Probe />
      </AdminProvider>,
    )
    expect(screen.getByText('authenticated')).toBeInTheDocument()
    unmount()
    renderWithProviders(
      <AdminProvider hasFeature={() => true}>
        <Probe />
      </AdminProvider>,
    )
    expect(screen.getByText('none')).toBeInTheDocument()
  })

  it('publishes the account destination and the invitation-link builder', () => {
    function Probe() {
      const accountHref = useAdminAccountHref()
      const invitationLink = useAdminInvitationLink()
      return (
        <span>
          {accountHref ?? 'no-account'} {invitationLink?.('token-1') ?? 'no-link'}
        </span>
      )
    }
    renderWithProviders(
      <AdminProvider
        hasFeature={() => true}
        accountHref="/host/security"
        invitationLink={(token) => `/host/invitations/${token}`}
      >
        <Probe />
      </AdminProvider>,
    )
    expect(
      screen.getByText('/host/security /host/invitations/token-1'),
    ).toBeInTheDocument()
  })

  it('publishes the impersonation change action', async () => {
    const onChange = vi.fn()
    function Probe() {
      const change = useAdminImpersonationChange()
      return (
        <button type="button" onClick={() => void change?.('ended')}>
          report
        </button>
      )
    }
    renderWithProviders(
      <AdminProvider
        hasFeature={() => true}
        onImpersonationChange={onChange}
      >
        <Probe />
      </AdminProvider>,
    )
    await userEvent.setup().click(screen.getByRole('button', { name: 'report' }))
    expect(onChange).toHaveBeenCalledWith('ended')
  })

  it('publishes the impersonation-starting hook and its release', async () => {
    const release = vi.fn()
    const starting = vi.fn(() => release)
    function Probe() {
      const onStarting = useAdminImpersonationStarting()
      return (
        <button
          type="button"
          onClick={() => {
            const release = onStarting?.()
            if (typeof release === 'function') {
              release()
            }
          }}
        >
          start
        </button>
      )
    }
    renderWithProviders(
      <AdminProvider
        hasFeature={() => true}
        onImpersonationStarting={starting}
      >
        <Probe />
      </AdminProvider>,
    )
    await userEvent.setup().click(screen.getByRole('button', { name: 'start' }))
    expect(starting).toHaveBeenCalledTimes(1)
    // The provider hands the hook's return value straight back, so the
    // page's release reaches the host's own function.
    expect(release).toHaveBeenCalledTimes(1)
  })

  it('publishes the role-description resolver', () => {
    function Probe() {
      const describeRole = useAdminDescribeRole()
      return (
        <span>
          {describeRole?.(ROLE.description_key, ROLE) ?? 'no-resolver'}
        </span>
      )
    }
    renderWithProviders(
      <AdminProvider
        hasFeature={() => true}
        describeRole={(_key, role) => `described:${role.role}`}
      >
        <Probe />
      </AdminProvider>,
    )
    expect(screen.getByText('described:editor')).toBeInTheDocument()
  })

  it('publishes the route table unchanged, resolving a declaration by id', () => {
    function Probe() {
      const routes = useAdminRouteTable()
      return (
        <span>
          {routes.map((route) => route.id).join(',')}{' '}
          {adminRouteById(routes, 'accounts')?.path ?? 'missing'}
        </span>
      )
    }
    renderWithProviders(
      <AdminProvider hasFeature={() => true} routes={ROUTES}>
        <Probe />
      </AdminProvider>,
    )
    expect(screen.getByText('tenants,accounts /accounts')).toBeInTheDocument()
  })

  it('publishes an empty route table when none is supplied', () => {
    function Probe() {
      return <span>{useAdminRouteTable().length} routes</span>
    }
    renderWithProviders(
      <AdminProvider hasFeature={() => true}>
        <Probe />
      </AdminProvider>,
    )
    expect(screen.getByText('0 routes')).toBeInTheDocument()
  })

  it('routes a console link in place on a plain click only', async () => {
    const onNavigate = vi.fn()
    renderWithProviders(
      <AdminProvider hasFeature={() => true} onNavigate={onNavigate}>
        <AdminLink href="/tenants">go</AdminLink>
      </AdminProvider>,
    )
    const link = screen.getByRole('link', { name: 'go' })
    // A modifier click belongs to the browser: the host's router is not
    // told, and the link keeps its href.
    clickWithoutNavigating(link, { ctrlKey: true })
    expect(onNavigate).not.toHaveBeenCalled()
    expect(link).toHaveAttribute('href', '/tenants')

    await userEvent.setup().click(link)
    expect(onNavigate).toHaveBeenCalledWith('/tenants')
  })
})
