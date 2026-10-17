/**
 * AsyncSection contract: the four states of an async read map to the
 * four fixed renderings -- loading announcement, retryable error
 * placeholder, empty placeholder, content -- with the absent payload
 * taking precedence over the empty decision, the empty placeholder
 * requiring its copy, the content callback receiving the narrowed
 * payload, and the placeholders' titles rendering at the passed heading
 * level (h2, the section-title level, by default).
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { AsyncSection } from './AsyncSection.js'
import type { AsyncSectionStaleState } from './AsyncSection.js'
import { renderWithProviders } from '../../test-utils/render.js'
import { expectNoAxeViolations } from '../../test-utils/axe.js'

const ERROR_STATE = {
  title: 'Could not load items',
  description: 'Something went wrong loading the items.',
  retryLabel: 'Retry',
  onRetry: vi.fn(),
} as const

const EMPTY_STATE = {
  title: 'No items',
  description: 'Items will show up here.',
} as const

const STALE_DESCRIPTION = 'The list may be out of date.'
const STALE_RETRY_LABEL = 'Refresh the list'

function STALE_STATE(onRetry: () => void): AsyncSectionStaleState {
  return {
    description: STALE_DESCRIPTION,
    retryLabel: STALE_RETRY_LABEL,
    onRetry,
  }
}

/**
 * A host holding the stale marker as state: its retry withdraws the
 * marker, and a later failed retry supplies it again -- the caller-side
 * lifecycle `staleState` documents.
 */
function StaleSectionHost() {
  const [stale, setStale] = useState<AsyncSectionStaleState | undefined>(() =>
    STALE_STATE(() => {
      setStale(undefined)
    }),
  )
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setStale(
            STALE_STATE(() => {
              setStale(undefined)
            }),
          )
        }}
      >
        retry failed
      </button>
      <AsyncSection
        pending={false}
        payload={['alpha']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={stale}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AsyncSection>
    </div>
  )
}

describe('AsyncSection', () => {
  it('renders the loading announcement while pending', () => {
    renderWithProviders(
      <AsyncSection
        pending
        payload={['one']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    expect(screen.getByText('loading node')).toBeInTheDocument()
    expect(screen.queryByText(/content/)).not.toBeInTheDocument()
  })

  it('renders the retryable error placeholder for a settled read without a payload', async () => {
    const onRetry = vi.fn()
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={undefined as readonly string[] | undefined}
        empty
        loading={<p>loading node</p>}
        errorState={{ ...ERROR_STATE, onRetry }}
        emptyState={EMPTY_STATE}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    expect(screen.getByText(ERROR_STATE.title)).toBeInTheDocument()
    expect(screen.getByText(ERROR_STATE.description)).toBeInTheDocument()
    expect(screen.queryByText(EMPTY_STATE.title)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: ERROR_STATE.retryLabel }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('renders the empty placeholder for a settled empty payload', () => {
    const children = vi.fn(() => <p>content</p>)
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
      >
        {children}
      </AsyncSection>,
    )
    expect(screen.getByText(EMPTY_STATE.title)).toBeInTheDocument()
    expect(screen.getByText(EMPTY_STATE.description)).toBeInTheDocument()
    // The content callback is never built for the placeholder branches.
    expect(children).not.toHaveBeenCalled()
  })

  it('renders the content from the narrowed payload for a settled non-empty read', () => {
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={['alpha', 'beta']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    expect(screen.getByText('content alpha,beta')).toBeInTheDocument()
    expect(screen.queryByText(ERROR_STATE.title)).not.toBeInTheDocument()
  })

  it('renders the content when the section omits an empty state', () => {
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={{ locale: 'zh-CN' }}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
      >
        {(settings) => <p>content {settings.locale}</p>}
      </AsyncSection>,
    )
    expect(screen.getByText('content zh-CN')).toBeInTheDocument()
  })

  it('renders the placeholder titles at h2 by default and at the passed level', () => {
    const first = renderWithProviders(
      <AsyncSection
        pending={false}
        payload={undefined}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    expect(
      screen.getByRole('heading', { level: 2, name: ERROR_STATE.title }),
    ).toBeInTheDocument()
    first.unmount()

    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        headingLevel="h3"
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    expect(
      screen.getByRole('heading', { level: 3, name: EMPTY_STATE.title }),
    ).toBeInTheDocument()
  })

  it('renders the empty placeholder headerless when emptyHeadingLevel is none', () => {
    const { container } = renderWithProviders(
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        headingLevel="h2"
        emptyHeadingLevel="none"
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    // The empty placeholder carries no heading; the whole placeholder is one
    // role="status" region instead. The error level is untouched.
    expect(
      screen.queryByRole('heading', { name: EMPTY_STATE.title }),
    ).not.toBeInTheDocument()
    expect(container.querySelector('[role="status"]')).toHaveTextContent(
      EMPTY_STATE.title,
    )
  })

  it('keeps the content and marks it stale when staleState is given', async () => {
    const onRetry = vi.fn()
    renderWithProviders(
      <div>
        <h1>Page title</h1>
        <AsyncSection
          pending={false}
          payload={['alpha', 'beta']}
          empty={false}
          loading={<p>loading node</p>}
          errorState={ERROR_STATE}
          emptyState={EMPTY_STATE}
          staleState={STALE_STATE(onRetry)}
        >
          {(items) => <p>content {items.join(',')}</p>}
        </AsyncSection>
      </div>,
    )
    // The earlier data stays on screen; the alert rides above it.
    expect(screen.getByText('content alpha,beta')).toBeInTheDocument()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(STALE_DESCRIPTION)
    expect(alert).toContainElement(
      screen.getByRole('button', { name: STALE_RETRY_LABEL }),
    )
    await userEvent.click(
      screen.getByRole('button', { name: STALE_RETRY_LABEL }),
    )
    expect(onRetry).toHaveBeenCalledTimes(1)
    await expectNoAxeViolations()
  })

  it('withdraws the stale alert on retry and remounts it when the retry fails', async () => {
    const view = renderWithProviders(<StaleSectionHost />)
    const firstAlert = screen.getByRole('alert')
    expect(firstAlert).toHaveTextContent(STALE_DESCRIPTION)

    // The retry is issued: the host withdraws staleState and the alert
    // unmounts while the read is in flight, content still on screen.
    await userEvent.click(
      screen.getByRole('button', { name: STALE_RETRY_LABEL }),
    )
    expect(screen.queryByRole('alert')).toBeNull()
    expect(view.getByText('content alpha')).toBeInTheDocument()

    // The retry failed again: the host re-supplies staleState; the alert
    // mounts anew (a new node, so role="alert" announces again).
    await userEvent.click(
      screen.getByRole('button', { name: 'retry failed' }),
    )
    const secondAlert = screen.getByRole('alert')
    expect(secondAlert).toHaveTextContent(STALE_DESCRIPTION)
    expect(secondAlert).not.toBe(firstAlert)
    expect(view.getByText('content alpha')).toBeInTheDocument()
  })

  it('ignores staleState outside the settled-content branch', () => {
    const pendingView = renderWithProviders(
      <AsyncSection
        pending
        payload={['alpha']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={STALE_STATE(vi.fn())}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    // A read in flight shows the loading announcement, not stale data.
    expect(screen.getByText('loading node')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    pendingView.unmount()

    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={undefined}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={STALE_STATE(vi.fn())}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    // A settled read with no payload takes the error placeholder; stale
    // marking belongs to the branch whose data is still on screen.
    expect(screen.getByText(ERROR_STATE.title)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
