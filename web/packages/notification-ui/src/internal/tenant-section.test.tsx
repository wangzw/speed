import { describe, expect, it, vi } from 'vitest'
import { useState } from 'react'
import { act, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AsyncSectionErrorState } from '@speed/ui-kit'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { TenantAsyncSection } from './tenant-section.js'

const errorState: AsyncSectionErrorState = {
  title: 'Load failed',
  description: 'try later',
  retryLabel: 'Retry',
  onRetry: vi.fn(),
}

function InFlightHarness() {
  const [refreshing, setRefreshing] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setRefreshing(true)}>
        start read
      </button>
      <TenantAsyncSection<string>
        pending={false}
        payload="ready"
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        refreshing={refreshing}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>
    </>
  )
}

function RefusalHarness() {
  const [refused, setRefused] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setRefused(true)}>
        refuse
      </button>
      <TenantAsyncSection<string>
        pending={false}
        payload="ready"
        error={
          refused
            ? { status: 404, code: 'client.http.404' }
            : { status: 503, code: 'client.http.503' }
        }
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>
    </>
  )
}

function FocusHarness() {
  const [refreshing, setRefreshing] = useState(false)
  return (
    <TenantAsyncSection<string>
      pending={false}
      payload="ready"
      error={{ status: 503, code: 'client.http.503' }}
      empty={false}
      refreshing={refreshing}
      loading={<p>loading</p>}
      errorState={{ ...errorState, onRetry: () => setRefreshing(true) }}
    >
      {(value) => <p>content: {value}</p>}
    </TenantAsyncSection>
  )
}

let startAutomaticRead: (() => void) | undefined

function AutomaticReadHarness() {
  const [refreshing, setRefreshing] = useState(false)
  startAutomaticRead = () => setRefreshing(true)
  return (
    <TenantAsyncSection<string>
      pending={false}
      payload="ready"
      error={{ status: 503, code: 'client.http.503' }}
      empty={false}
      refreshing={refreshing}
      loading={<p>loading</p>}
      errorState={errorState}
    >
      {(value) => <p>content: {value}</p>}
    </TenantAsyncSection>
  )
}

describe('TenantAsyncSection', () => {
  it('renders the tenant state on a bare 404, without a retry', () => {
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404, code: 'client.http.404' }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText(enUS.tenantUnavailable.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(enUS.tenantUnavailable.description),
    ).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
  })

  it('treats a bare 404 that carries no code at all as the admission refusal', () => {
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 404 }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText(enUS.tenantUnavailable.title),
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
        refreshing={false}
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
      screen.queryByText(enUS.tenantUnavailable.title),
    ).not.toBeInTheDocument()
  })

  it('keeps the retryable error for a 503', () => {
    renderWithProviders(
      <TenantAsyncSection<unknown>
        pending={false}
        payload={undefined}
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        refreshing={false}
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
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('content: ready')).toBeInTheDocument()
  })

  it('keeps the content under a stale notice when a refresh fails', async () => {
    renderWithProviders(
      <TenantAsyncSection<string>
        pending={false}
        payload="ready"
        error={{ status: 503, code: 'client.http.503' }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>,
      { language: 'zh-CN' },
    )
    expect(screen.getByText('content: ready')).toBeInTheDocument()
    expect(screen.queryByText('Load failed')).not.toBeInTheDocument()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(errorState.onRetry).toHaveBeenCalledTimes(1)
  })

  it('withdraws the stale notice while the retry is in flight', async () => {
    renderWithProviders(<InFlightHarness />, { language: 'zh-CN' })
    expect(screen.getByRole('alert')).toHaveTextContent(zhCN.stale.description)
    await userEvent.click(screen.getByRole('button', { name: 'start read' }))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText('content: ready')).toBeInTheDocument()
  })

  it('keeps focus on the retained content when the retry withdraws the notice', async () => {
    renderWithProviders(<FocusHarness />, { language: 'en-US' })
    const retry = screen.getByRole('button', { name: 'Retry' })
    retry.focus()
    expect(retry).toHaveFocus()

    await userEvent.keyboard('{Enter}')

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(document.activeElement).not.toBe(document.body)
    expect(document.activeElement).toHaveTextContent('content: ready')
  })

  it('keeps focus on the retained content when an automatic re-read withdraws the notice', () => {
    renderWithProviders(<AutomaticReadHarness />, { language: 'en-US' })
    const retry = screen.getByRole('button', { name: 'Retry' })
    retry.focus()
    expect(retry).toHaveFocus()

    act(() => {
      startAutomaticRead?.()
    })

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(document.activeElement).not.toBe(document.body)
    expect(document.activeElement).toHaveTextContent('content: ready')
  })

  it('replaces content with the tenant state on a refusal, not a stale notice', () => {
    renderWithProviders(
      <TenantAsyncSection<string>
        pending={false}
        payload="ready"
        error={{ status: 404, code: 'client.http.404' }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(
      screen.getByText(enUS.tenantUnavailable.title),
    ).toBeInTheDocument()
    expect(screen.queryByText('content: ready')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('replaces a stale notice and the content once admission refuses the tenant', async () => {
    renderWithProviders(<RefusalHarness />, { language: 'en-US' })
    expect(screen.getByRole('alert')).toHaveTextContent(enUS.stale.description)
    expect(screen.getByText('content: ready')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'refuse' }))

    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByText(enUS.tenantUnavailable.title)).toBeInTheDocument()
    expect(screen.queryByText('content: ready')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Retry' }),
    ).not.toBeInTheDocument()
  })

  it('keeps the retryable error placeholder when the first read delivered nothing', () => {
    renderWithProviders(
      <TenantAsyncSection<string>
        pending={false}
        payload={undefined}
        error={{ status: 503, code: 'client.http.503' }}
        empty
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </TenantAsyncSection>,
      { language: 'en-US' },
    )
    expect(screen.getByText('Load failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
