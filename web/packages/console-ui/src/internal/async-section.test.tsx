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

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
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
