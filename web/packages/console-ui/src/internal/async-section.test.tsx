/**
 * ConsoleAsyncSection: the console's failure-branch split.
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
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConsoleAsyncSection, isForbidden } from './async-section.js'
import { renderWithProviders } from '../../test-utils/render.js'

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

/** A probe mounting ConsoleAsyncSection with a settled failed read. */
function Probe({ error }: { readonly error: unknown }) {
  return (
    <ConsoleAsyncSection
      pending={false}
      payload={undefined}
      error={error}
      empty={false}
      loading={<p>loading</p>}
      errorState={{
        title: 'error-title',
        description: 'error-description',
        retryLabel: '重试',
        onRetry: () => {},
      }}
    >
      {() => <p>content</p>}
    </ConsoleAsyncSection>
  )
}

describe('ConsoleAsyncSection', () => {
  it('renders the permission placeholder for a module refusal carried by status', () => {
    renderWithProviders(
      <Probe error={{ status: 403, code: 'authz.forbidden' }} />,
    )
    expect(screen.getByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('renders the retryable placeholder for a 5xx failure', () => {
    renderWithProviders(
      <Probe error={{ status: 500, code: 'client.http.500' }} />,
    )
    expect(screen.getByText('error-title')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('renders the retryable placeholder for a transport failure', () => {
    renderWithProviders(
      <Probe error={{ status: 0, code: 'client.network' }} />,
    )
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
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
    <ConsoleAsyncSection
      pending={false}
      payload={payload}
      error={error}
      refreshing={refreshing}
      empty={false}
      loading={<p>loading</p>}
      errorState={{
        title: 'error-title',
        description: 'error-description',
        retryLabel: '重试',
        onRetry,
      }}
    >
      {(items) => <p>content {items.join(',')}</p>}
    </ConsoleAsyncSection>
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
      <ConsoleAsyncSection
        pending={false}
        payload={['alpha']}
        error={{ status: 500, code: 'client.http.500' }}
        refreshing={refreshing}
        empty={false}
        loading={<p>loading</p>}
        errorState={{
          title: 'error-title',
          description: 'error-description',
          retryLabel: '重试',
          onRetry: () => setRefreshing(true),
        }}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </ConsoleAsyncSection>
    </div>
  )
}

describe('ConsoleAsyncSection refresh state', () => {
  it('replaces content with the permission placeholder on a 403 that has a payload', () => {
    renderWithProviders(
      <SettledProbe
        payload={['alpha']}
        error={{ status: 403, code: 'authz.forbidden' }}
      />,
    )
    // A refusal makes the older data meaningless: the placeholder takes
    // the content's place, with no retry and no stale marker.
    expect(screen.getByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByText('content alpha')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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
    expect(alert).toHaveTextContent('所示内容可能已过期，请重试刷新。')
    const content = screen.getByText('content alpha')
    expect(
      alert.compareDocumentPosition(content) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
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
    expect(firstAlert).toHaveTextContent('所示内容可能已过期，请重试刷新。')

    // The retry is issued: the read is in flight and the marker unmounts
    // while the content stays on screen.
    await userEvent.click(screen.getByRole('button', { name: '重试' }))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(view.getByText('content alpha')).toBeInTheDocument()

    // The retry failed again: the marker mounts anew, so its alert
    // announcement fires again.
    await userEvent.click(screen.getByRole('button', { name: 'retry failed' }))
    const secondAlert = screen.getByRole('alert')
    expect(secondAlert).toHaveTextContent('所示内容可能已过期，请重试刷新。')
    expect(secondAlert).not.toBe(firstAlert)
    expect(view.getByText('content alpha')).toBeInTheDocument()
  })
})
