/**
 * AccountAsyncSection behaviour: the binding derives ui-kit's stale notice
 * from a read's failure over an already settled payload, so a failed
 * refresh keeps the content on screen and states that it may be out of
 * date; the notice is withdrawn while a read is in flight and the retry is
 * the section's own retry. Without a settled payload the same failure
 * stays the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AsyncSectionErrorState } from '@speed/ui-kit'
import enUS from '../locales/en-US.json' with { type: 'json' }
import zhCN from '../locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../../test-utils/render.js'
import { AccountAsyncSection } from './async-section.js'

const errorState: AsyncSectionErrorState = {
  title: 'Load failed',
  description: 'try later',
  retryLabel: 'Retry',
  onRetry: vi.fn(),
}

describe('AccountAsyncSection', () => {
  it('keeps settled content and marks it stale when a refresh fails', async () => {
    const onRetry = vi.fn()
    const user = userEvent.setup()
    renderWithProviders(
      <AccountAsyncSection<string>
        pending={false}
        payload="ready"
        error={{ status: 503 }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={{ ...errorState, onRetry }}
      >
        {(value) => <p>content: {value}</p>}
      </AccountAsyncSection>,
    )

    const content = screen.getByText('content: ready')
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(zhCN.stale.description)
    // The notice stands above the content it marks.
    expect(
      alert.compareDocumentPosition(content) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // The failure never replaces the content with the error placeholder.
    expect(screen.queryByText(errorState.title)).toBeNull()

    await user.click(screen.getByRole('button', { name: errorState.retryLabel }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('withdraws the notice while a read is in flight', () => {
    renderWithProviders(
      <AccountAsyncSection<string>
        pending={false}
        payload="ready"
        error={{ status: 503 }}
        empty={false}
        refreshing
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </AccountAsyncSection>,
    )

    expect(screen.getByText('content: ready')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('renders the retryable error placeholder when no payload settled', () => {
    renderWithProviders(
      <AccountAsyncSection<string>
        pending={false}
        payload={undefined}
        error={{ status: 503 }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {() => <p>content</p>}
      </AccountAsyncSection>,
    )

    expect(screen.getByText(errorState.title)).toBeInTheDocument()
    expect(screen.queryByText(zhCN.stale.description)).toBeNull()
  })

  it('reads the stale copy from the package bundle per language', () => {
    renderWithProviders(
      <AccountAsyncSection<string>
        pending={false}
        payload="ready"
        error={{ status: 503 }}
        empty={false}
        refreshing={false}
        loading={<p>loading</p>}
        errorState={errorState}
      >
        {(value) => <p>content: {value}</p>}
      </AccountAsyncSection>,
      { language: 'en-US' },
    )

    expect(screen.getByRole('alert')).toHaveTextContent(
      enUS.stale.description,
    )
  })
})
