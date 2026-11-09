/**
 * AdminAsyncSection: the console's failure-branch split.
 *
 * A refused read is detected from the answer -- its HTTP status is 403,
 * or a bare 403 surfaced as the client.http.403 code -- and every other
 * failure (a 5xx, a transport error) falls through to the retryable error
 * placeholder. Both branches are exercised here: the status check that
 * catches a module refusal whose code is the module's own (not
 * client.http.403), and the fall-through that keeps a 5xx or a transport
 * failure retryable. The page suites script every 403 with the bare
 * client.http.403 code, so neither the status branch nor the fall-through
 * would be missed by them alone.
 */

import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { getAuthnMfaFactorsQueryKey } from '@speed/api-sdk'
import { AdminAsyncSection, isForbidden } from './async-section.js'
import { AdminAccountHrefProvider } from './account-href.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { bindAdminRequest } from '../../test-utils/request-rig.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

/** A caller-supplied retry label fixture; the section renders what it is given. */
const RETRY = 'Retry'

/** One active factor as the caller's own factor read answers it. */
const ACTIVE_FACTOR = {
  id: 'factor-1',
  kind: 'totp',
  label: 'Authenticator app',
  status: 'active',
  created_at: '2026-01-01T00:00:00Z',
}

/** Binds the refusal exit's factor read; any other request leg is a defect. */
function bindFactors(factors: readonly unknown[]): void {
  bindAdminRequest((call) => {
    if (call.method === 'GET' && call.path === '/api/v1/auth/factors') {
      return { factors }
    }
    throw new Error(`unexpected ${call.method} ${call.path}`)
  })
}

/** Waits until the refusal exit's factor read has settled. */
async function awaitFactorRead(
  queryClient: ReturnType<typeof renderWithProviders>['queryClient'],
): Promise<void> {
  await waitFor(() => {
    expect(
      queryClient.getQueryState(getAuthnMfaFactorsQueryKey())?.status,
    ).toBe('success')
  })
}

describe('isForbidden', () => {
  it('reads a module refusal carried by the HTTP status alone', () => {
    // A module refuses with RFC 9457 problem details; its code is the
    // module's own (authz.forbidden), not the transport's client.http.403,
    // so the 403 status is the only signal the answer carries.
    expect(isForbidden({ status: 403, code: 'authz.forbidden' })).toBe(true)
  })

  it('reads the bare 403 the transport synthesizes with no envelope', () => {
    expect(isForbidden({ status: 403, code: 'client.http.403' })).toBe(true)
  })

  it('does not read a 5xx as a refusal', () => {
    expect(isForbidden({ status: 500, code: 'client.http.500' })).toBe(false)
  })

  it('does not read a transport failure as a refusal', () => {
    expect(isForbidden({ status: 0, code: 'client.network' })).toBe(false)
  })

  it('does not read a plain failure as a refusal', () => {
    expect(isForbidden(new Error('boom'))).toBe(false)
    expect(isForbidden(undefined)).toBe(false)
    expect(isForbidden(null)).toBe(false)
  })
})

/** A probe mounting AdminAsyncSection with a settled failed read. */
function Probe({
  error,
  notFoundStatuses,
  mfaRelogin,
}: {
  readonly error: unknown
  readonly notFoundStatuses?: readonly number[]
  readonly mfaRelogin?: () => void
}) {
  return (
    <AdminAsyncSection
      pending={false}
      payload={undefined}
      error={error}
      refreshing={false}
      empty={false}
      loading={<p>loading</p>}
      errorState={{
        title: 'error-title',
        description: 'error-description',
        retryLabel: RETRY,
        onRetry: () => {},
      }}
      notFoundState={{
        title: 'not-found-title',
        description: 'not-found-description',
        retryLabel: RETRY,
        onRetry: () => {},
      }}
      notFoundStatuses={notFoundStatuses}
      mfaRelogin={mfaRelogin}
    >
      {() => <p>content</p>}
    </AdminAsyncSection>
  )
}

describe('AdminAsyncSection', () => {
  it('renders the permission placeholder for a module refusal carried by status', () => {
    renderWithProviders(
      <Probe error={{ status: 403, code: 'authz.forbidden' }} />,
    )
    expect(screen.getByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: RETRY })).toBeNull()
  })

  it('renders the retryable placeholder for a 5xx failure', () => {
    renderWithProviders(
      <Probe error={{ status: 500, code: 'client.http.500' }} />,
    )
    expect(screen.getByText('error-title')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: RETRY })).toBeInTheDocument()
  })

  it('renders the retryable placeholder for a transport failure', () => {
    renderWithProviders(
      <Probe error={{ status: 0, code: 'client.network' }} />,
    )
    expect(screen.getByRole('button', { name: RETRY })).toBeInTheDocument()
  })

  it('renders the sign-in exit for a multi-factor refusal when the account has a factor', async () => {
    bindFactors([ACTIVE_FACTOR])
    const mfaRelogin = vi.fn()
    const { queryClient } = renderWithProviders(
      <Probe
        error={{
          status: 401,
          code: 'authn.insufficient_user_authentication',
        }}
        mfaRelogin={mfaRelogin}
      />,
    )
    // The organization's requirement is named through the console resolver,
    // and with a factor registered a fresh sign-in clears it: retrying the
    // same request cannot.
    expect(
      await screen.findByText(
        zhCN.errors.authn.insufficient_user_authentication,
      ),
    ).toBeInTheDocument()
    // The factor read settles with an active factor: the sign-in exit
    // stays, not the enrollment guidance.
    await awaitFactorRead(queryClient)
    expect(screen.queryByText(zhCN.stepUp.mfaEnrollRequired)).toBeNull()
    expect(screen.queryByRole('button', { name: RETRY })).toBeNull()
    await userEvent.click(
      screen.getByRole('button', { name: zhCN.stepUp.relogin }),
    )
    expect(mfaRelogin).toHaveBeenCalledTimes(1)
  })

  it('names the missing second factor and points at enrollment when the account has none', async () => {
    bindFactors([])
    renderWithProviders(
      <AdminAccountHrefProvider accountHref="/account/security">
        <Probe
          error={{
            status: 401,
            code: 'authn.insufficient_user_authentication',
          }}
          mfaRelogin={() => {}}
        />
      </AdminAccountHrefProvider>,
    )
    // Signing in again cannot add a factor, so the section names the
    // missing two-step verification and points at the host's account
    // settings; it offers no sign-in button and no retry.
    expect(
      await screen.findByText(zhCN.stepUp.mfaEnrollRequired),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.stepUp.relogin })).toBeNull()
    expect(screen.queryByRole('button', { name: RETRY })).toBeNull()
    expect(
      screen.getByRole('link', { name: zhCN.stepUp.goToAccount }),
    ).toHaveAttribute('href', '/account/security')
  })

  it('renders the no-factor guidance as text when the host supplies no account destination', async () => {
    bindFactors([])
    renderWithProviders(
      <Probe
        error={{
          status: 401,
          code: 'authn.insufficient_user_authentication',
        }}
        mfaRelogin={() => {}}
      />,
    )
    expect(
      await screen.findByText(zhCN.stepUp.mfaEnrollRequired),
    ).toBeInTheDocument()
    // The package holds no host path of its own.
    expect(screen.queryAllByRole('link')).toHaveLength(0)
  })

  it('keeps a generic 401 on the retryable placeholder', () => {
    renderWithProviders(
      <Probe
        error={{ status: 401, code: 'client.http.401' }}
        mfaRelogin={() => {}}
      />,
    )
    expect(screen.getByText('error-title')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: RETRY })).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.stepUp.relogin }),
    ).toBeNull()
  })

  it('keeps a multi-factor refusal retryable when the caller offers no sign-in exit', () => {
    renderWithProviders(
      <Probe
        error={{
          status: 401,
          code: 'authn.insufficient_user_authentication',
        }}
      />,
    )
    expect(screen.getByText('error-title')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: RETRY })).toBeInTheDocument()
  })

  it('renders the not-found placeholder for a status the page names as the same answer', () => {
    // A route that answers 400 for an id it cannot parse names that status;
    // the 404 answer renders it without being listed.
    renderWithProviders(
      <Probe
        error={{ status: 400, code: 'audit.bad-request' }}
        notFoundStatuses={[400]}
      />,
    )
    expect(screen.getByText('not-found-title')).toBeInTheDocument()
    expect(screen.queryByText('error-title')).toBeNull()
  })

  it('keeps a status outside the not-found set on the retryable placeholder', () => {
    renderWithProviders(
      <Probe
        error={{ status: 400, code: 'audit.bad-request' }}
        notFoundStatuses={[422]}
      />,
    )
    expect(screen.getByText('error-title')).toBeInTheDocument()
  })
})

/** A settled section: the caller decides payload, failure and refresh state. */
function SettledProbe({
  payload,
  error,
  refreshing = false,
  onRetry = () => {},
}: {
  readonly payload: readonly string[] | undefined
  readonly error: unknown
  readonly refreshing?: boolean
  readonly onRetry?: () => void
}) {
  return (
    <AdminAsyncSection
      pending={false}
      payload={payload}
      error={error}
      refreshing={refreshing}
      empty={false}
      loading={<p>loading</p>}
      errorState={{
        title: 'error-title',
        description: 'error-description',
        retryLabel: RETRY,
        onRetry,
      }}
    >
      {(items) => <p>content {items.join(',')}</p>}
    </AdminAsyncSection>
  )
}

/**
 * A settled section whose refresh retry is scripted: the retry withdraws
 * the marker (a read is in flight) and a later control re-supplies it,
 * standing in for a retry that failed again.
 */
function FlakyRefreshHost() {
  const [refreshing, setRefreshing] = useState(false)
  return (
    <div>
      <button type="button" onClick={() => setRefreshing(false)}>
        retry failed
      </button>
      <AdminAsyncSection
        pending={false}
        payload={['alpha']}
        error={{ status: 500, code: 'client.http.500' }}
        refreshing={refreshing}
        empty={false}
        loading={<p>loading</p>}
        errorState={{
          title: 'error-title',
          description: 'error-description',
          retryLabel: RETRY,
          onRetry: () => setRefreshing(true),
        }}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AdminAsyncSection>
    </div>
  )
}

describe('AdminAsyncSection refresh state', () => {
  it('replaces content with the permission placeholder on a 403 that has a payload', () => {
    renderWithProviders(
      <SettledProbe
        payload={['alpha']}
        error={{ status: 403, code: 'authz.forbidden' }}
      />,
    )
    // A refusal makes the older data meaningless: the placeholder takes
    // the content's place, with no retry and no stale marker.
    expect(screen.getByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByText('content alpha')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('button', { name: RETRY })).toBeNull()
  })

  it('marks a failed refresh with data on screen as stale above the content', async () => {
    const onRetry = vi.fn()
    renderWithProviders(
      <SettledProbe
        payload={['alpha']}
        error={{ status: 500, code: 'client.http.500' }}
        onRetry={onRetry}
      />,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    const content = screen.getByText('content alpha')
    expect(
      alert.compareDocumentPosition(content) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: RETRY }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('leaves the marker out while the retry is in flight', () => {
    renderWithProviders(
      <SettledProbe
        payload={['alpha']}
        error={{ status: 500, code: 'client.http.500' }}
        refreshing
      />,
    )
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByText('content alpha')).toBeInTheDocument()
  })

  it('withdraws the stale marker on retry and remounts it when the retry fails', async () => {
    const view = renderWithProviders(<FlakyRefreshHost />)
    const firstAlert = screen.getByRole('alert')
    expect(firstAlert).toHaveTextContent(zhCN.stale.description)

    // The retry is issued: the read is in flight and the marker unmounts
    // while the content stays on screen.
    await userEvent.click(screen.getByRole('button', { name: RETRY }))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(view.getByText('content alpha')).toBeInTheDocument()

    // The retry failed again: the marker mounts anew, so its alert
    // announcement fires again.
    await userEvent.click(screen.getByRole('button', { name: 'retry failed' }))
    const secondAlert = screen.getByRole('alert')
    expect(secondAlert).toHaveTextContent(zhCN.stale.description)
    expect(secondAlert).not.toBe(firstAlert)
    expect(view.getByText('content alpha')).toBeInTheDocument()
  })
})
