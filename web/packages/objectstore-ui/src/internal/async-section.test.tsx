import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../test-utils/render.js'
import { ObjectAsyncSection, isForbidden } from './async-section.js'
import type { AsyncSectionErrorState } from '@speed/ui-kit'

const errorState: AsyncSectionErrorState = {
  title: 'Load failed',
  description: 'try later',
  retryLabel: 'Retry',
  onRetry: vi.fn(),
}

describe('isForbidden', () => {
  it('treats an HTTP 403 or the client.http.403 code as a refusal', () => {
    expect(isForbidden({ status: 403 })).toBe(true)
    expect(isForbidden({ code: 'client.http.403' })).toBe(true)
  })
  it('treats anything else as an ordinary failure', () => {
    expect(isForbidden(undefined)).toBe(false)
    expect(isForbidden({ status: 503 })).toBe(false)
    expect(isForbidden({ code: 'client.network' })).toBe(false)
    expect(isForbidden('boom')).toBe(false)
  })
})

describe('ObjectAsyncSection', () => {
  it('renders the no-permission state on a permission refusal', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 403 }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText(/No access to the object store/i),
    ).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
  })

  it('renders the retryable error for a non-refusal failure', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Load failed')).toBeInTheDocument()
  })

  it('renders the loading branch while pending', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending
        payload={undefined}
        error={undefined}
        empty={false}
        loading={<p>loading now</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('loading now')).toBeInTheDocument()
  })

  it('renders the settled content', () => {
    renderWithProviders(
      <ObjectAsyncSection<string>
        pending={false}
        payload="ready"
        error={undefined}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('content: ready')).toBeInTheDocument()
  })
})
