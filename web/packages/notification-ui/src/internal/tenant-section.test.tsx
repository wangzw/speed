import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import type { AsyncSectionErrorState } from '@speed/ui-kit'
import { renderWithProviders } from '../../test-utils/render.js'
import { TenantAsyncSection } from './tenant-section.js'

const errorState: AsyncSectionErrorState = {
  title: 'Load failed',
  description: 'try later',
  retryLabel: 'Retry',
  onRetry: vi.fn(),
}

describe('TenantAsyncSection', () => {
  it('renders the tenant state on a bare 404, without a retry', () => {
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404, code: 'client.http.404' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText("Cannot access the current organization's content"),
    ).toBeInTheDocument()
    expect(
      screen.getByText(/To join, ask an administrator to send you an invitation/i),
    ).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('treats a bare 404 that carries no code at all as the admission refusal', () => {
    // A host-created error or a test double may carry the status alone;
    // auth-core's predicate classifies that shape as the refusal too.
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404 }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText("Cannot access the current organization's content"),
    ).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
  })

  it('keeps the retryable error for a typed 404', () => {
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404, code: 'notification.message_not_found' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Load failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(
      screen.queryByText("Cannot access the current organization's content"),
    ).not.toBeInTheDocument()
  })

  it('keeps the retryable error for a 503', () => {
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Load failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('renders the settled content for a successful read', () => {
    renderWithProviders(
      <TenantAsyncSection<string>
        pending={false}
        payload="ready"
        error={undefined}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('content: ready')).toBeInTheDocument()
  })
})
