import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  ObjectAsyncSection,
  isForbidden,
  isObjectNotFound,
} from './async-section.js'
import type { AsyncSectionErrorState } from '@speed/ui-kit'
import enUS from '../locales/en-US.json' with { type: 'json' }

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

describe('isObjectNotFound', () => {
  it('treats the typed objectstore.not_found code as a gone object', () => {
    expect(
      isObjectNotFound({ status: 404, code: 'objectstore.not_found' }),
    ).toBe(true)
    expect(isObjectNotFound({ code: 'objectstore.not_found' })).toBe(true)
  })
  it('does not treat the admission refusal or another failure as a gone object', () => {
    expect(isObjectNotFound({ status: 404, code: 'client.http.404' })).toBe(false)
    expect(isObjectNotFound({ status: 503, code: 'client.http.503' })).toBe(false)
    expect(isObjectNotFound(undefined)).toBe(false)
    expect(isObjectNotFound('boom')).toBe(false)
  })
})

describe('ObjectAsyncSection', () => {
  it('renders the tenant state on a bare 404, without a retry', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404, code: 'client.http.404' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText("Cannot access the current organization's content"),
    ).toBeInTheDocument()
    expect(screen.getByText(/may not exist, or you may not be a member/i)).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('treats a bare 404 before the code is synthesized as the same refusal', () => {
    // The admission refusal is recognized structurally: the transport's
    // client.http.404 code when it has been synthesized, and the bare 404
    // shape when the answer has not crossed that layer yet.
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404 }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText("Cannot access the current organization's content"),
    ).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
  })

  it('keeps the retryable error for a typed object not-found 404 when no not-found state is given', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404, code: 'objectstore.not_found' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Load failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(
      screen.queryByText("Cannot access the current organization's content"),
    ).not.toBeInTheDocument()
  })

  it('renders the not-found placeholder for a typed 404, without a retry', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404, code: 'objectstore.not_found' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
        notFoundState={{ title: 'Gone', description: 'It was deleted' }}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Gone')).toBeInTheDocument()
    expect(screen.getByText('It was deleted')).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('replaces a cached payload with the not-found placeholder for a typed 404', () => {
    // A refetch answered not-found while the earlier detail is still
    // cached: the server has confirmed the object is gone, so the cached
    // payload must not stay on screen.
    renderWithProviders(
      <ObjectAsyncSection<string>
        pending={false}
        payload="stale detail"
        error={{ status: 404, code: 'objectstore.not_found' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
        notFoundState={{ title: 'Gone', description: 'It was deleted' }}
      >
        {(value) => <p>content: {value}</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Gone')).toBeInTheDocument()
    expect(screen.getByText('It was deleted')).toBeInTheDocument()
    expect(screen.queryByText('content: stale detail')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('keeps the retryable error for a 5xx even when a not-found state is given', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
        notFoundState={{ title: 'Gone', description: 'It was deleted' }}
      >
        {() => <p>content</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Load failed')).toBeInTheDocument()
    expect(screen.queryByText('Gone')).not.toBeInTheDocument()
  })

  it('renders the no-permission state on a permission refusal', () => {
    renderWithProviders(
      <ObjectAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 403 }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
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
        refreshing={false}
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
        refreshing={false}
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
        refreshing={false}
      >
        {(value) => <p>content: {value}</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('content: ready')).toBeInTheDocument()
  })

  it('replaces a cached payload with the no-permission state on a 403', () => {
    // A refusal makes the older data meaningless: it must not survive as a
    // stale list beside a permission notice.
    renderWithProviders(
      <ObjectAsyncSection<string>
        pending={false}
        payload="cached rows"
        error={{ status: 403 }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
      >
        {(value) => <p>content: {value}</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText(/No access to the object store/i)).toBeInTheDocument()
    expect(screen.queryByText('content: cached rows')).not.toBeInTheDocument()
    expect(screen.queryByText(enUS.stale.description)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('keeps a cached payload and marks it stale after a failed refresh', () => {
    renderWithProviders(
      <ObjectAsyncSection<string>
        pending={false}
        payload="cached rows"
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing={false}
      >
        {(value) => <p>content: {value}</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('content: cached rows')).toBeInTheDocument()
    expect(screen.getByText(enUS.stale.description)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('withdraws the stale marker while the retry is in flight', () => {
    renderWithProviders(
      <ObjectAsyncSection<string>
        pending={false}
        payload="cached rows"
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        loading={<p>loading</p>}
        errorState={errorState}
        refreshing
      >
        {(value) => <p>content: {value}</p>}
      </ObjectAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('content: cached rows')).toBeInTheDocument()
    expect(screen.queryByText(enUS.stale.description)).not.toBeInTheDocument()
  })
})
