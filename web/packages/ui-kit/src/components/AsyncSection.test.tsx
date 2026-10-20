/**
 * AsyncSection contract: the four states of an async read map to the
 * four fixed renderings -- loading announcement, retryable error
 * placeholder, empty placeholder, content -- with the absent payload
 * taking precedence over the empty decision, the empty placeholder
 * requiring its copy, the content callback receiving the narrowed
 * payload, and the placeholders' titles rendering at the passed heading
 * level (h2, the section-title level, by default). A settled branch may
 * also carry the stale marker: the settled rendering -- the content, or
 * the empty placeholder when the payload is empty -- keeps rendering
 * while a warning alert with the retry action renders above it, so the
 * marker appearing or withdrawing never remounts the settled node.
 */

import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef, useState } from 'react'
import { alertClasses } from '@mui/material/Alert'
import { AsyncSection } from './AsyncSection.js'
import type { AsyncSectionStaleState } from './AsyncSection.js'
import { EmptyBoxIcon } from '../internal/icons.js'
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

const REFUSED_STATE = {
  title: 'No access',
  description: 'You cannot view these items.',
} as const

function staleStateOf(onRetry: () => void): AsyncSectionStaleState {
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
    staleStateOf(() => {
      setStale(undefined)
    }),
  )
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setStale(
            staleStateOf(() => {
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

/** The settled content, holding state a remount would lose. */
function StatefulContent() {
  const [count, setCount] = useState(0)
  return (
    <div data-testid="settled-content">
      <button
        type="button"
        onClick={() => {
          setCount((current) => current + 1)
        }}
      >
        increment
      </button>
      <output>{count}</output>
      <input aria-label="draft" />
    </div>
  )
}

/** A settled section whose host flips the stale marker on and off. */
function StatefulStaleHost() {
  const [stale, setStale] = useState<AsyncSectionStaleState | undefined>(() =>
    staleStateOf(vi.fn()),
  )
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setStale((current) =>
            current === undefined ? staleStateOf(vi.fn()) : undefined,
          )
        }}
      >
        toggle stale
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
        {() => <StatefulContent />}
      </AsyncSection>
    </div>
  )
}

/** A settled EMPTY section whose host flips the stale marker on and off. */
function EmptyPayloadStaleHost({
  onRetry,
}: {
  readonly onRetry: () => void
}) {
  const [stale, setStale] = useState<AsyncSectionStaleState | undefined>(() =>
    staleStateOf(onRetry),
  )
  return (
    <div>
      <button
        type="button"
        onClick={() => {
          setStale((current) =>
            current === undefined ? staleStateOf(onRetry) : undefined,
          )
        }}
      >
        toggle stale
      </button>
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={stale}
      >
        {(items) => <p>content {items.length}</p>}
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
          staleState={staleStateOf(onRetry)}
        >
          {(items) => <p>content {items.join(',')}</p>}
        </AsyncSection>
      </div>,
    )
    // The earlier data stays on screen; the alert rides above it.
    const content = screen.getByText('content alpha,beta')
    expect(content).toBeInTheDocument()
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(STALE_DESCRIPTION)
    // The notice precedes the content and does not wrap it: it is a
    // sibling above the settled node, not a container around it.
    expect(
      alert.compareDocumentPosition(content) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(alert).not.toContainElement(content)
    expect(alert).toContainElement(
      screen.getByRole('button', { name: STALE_RETRY_LABEL }),
    )
    await userEvent.click(
      screen.getByRole('button', { name: STALE_RETRY_LABEL }),
    )
    expect(onRetry).toHaveBeenCalledTimes(1)
    await expectNoAxeViolations()
  })

  it('renders the stale notice as a warning alert with two spacing units below it', () => {
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
          staleState={staleStateOf(vi.fn())}
        >
          {(items) => <p>content {items.join(',')}</p>}
        </AsyncSection>
      </div>,
    )
    const alert = screen.getByRole('alert')
    // The notice is a warning: a refresh failed but the shown data keeps
    // its meaning. Error and info severities are not this state.
    expect(alert).toHaveClass(alertClasses.colorWarning)
    expect(alert.className).not.toMatch(new RegExp(alertClasses.colorError))
    expect(alert.className).not.toMatch(new RegExp(alertClasses.colorInfo))
    // Two spacing units (8px each) separate the notice from the content
    // rendered beneath it.
    expect(alert).toHaveStyle({ marginBottom: '16px' })
  })

  it('keeps the stale notice and its retry when the description is empty', async () => {
    const onRetry = vi.fn()
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={['alpha']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={{
          description: '',
          retryLabel: STALE_RETRY_LABEL,
          onRetry,
        }}
      >
        {(items) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    // An empty description does not dissolve the state marker: the alert
    // stands (announcing the state change) and the retry stays available.
    const alert = screen.getByRole('alert')
    expect(alert).toBeInTheDocument()
    const retry = screen.getByRole('button', { name: STALE_RETRY_LABEL })
    expect(alert).toContainElement(retry)
    await userEvent.click(retry)
    expect(onRetry).toHaveBeenCalledTimes(1)
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

  it('keeps the settled content mounted while the stale notice appears and withdraws', async () => {
    const view = renderWithProviders(<StatefulStaleHost />)
    const content = view.getByTestId('settled-content')
    await userEvent.click(view.getByRole('button', { name: 'increment' }))
    await userEvent.type(view.getByRole('textbox', { name: 'draft' }), 'draft text')
    expect(content).toHaveTextContent('1')
    expect(view.getByRole('alert')).toBeInTheDocument()

    // The notice withdraws: the content node is the same node and keeps its
    // local state -- it is not unmounted and remounted by the tree-shape
    // change.
    await userEvent.click(view.getByRole('button', { name: 'toggle stale' }))
    expect(view.queryByRole('alert')).toBeNull()
    expect(view.getByTestId('settled-content')).toBe(content)
    expect(content).toHaveTextContent('1')
    expect(view.getByRole('textbox', { name: 'draft' })).toHaveValue(
      'draft text',
    )

    // The notice returns: still the same content node, still its state.
    await userEvent.click(view.getByRole('button', { name: 'toggle stale' }))
    expect(view.getByRole('alert')).toBeInTheDocument()
    expect(view.getByTestId('settled-content')).toBe(content)
    expect(content).toHaveTextContent('1')
    expect(view.getByRole('textbox', { name: 'draft' })).toHaveValue(
      'draft text',
    )
  })

  it('keeps the stale notice when the content is a multi-node fragment', () => {
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={['alpha']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={staleStateOf(vi.fn())}
      >
        {() => (
          <>
            <p>first half</p>
            <p>second half</p>
          </>
        )}
      </AsyncSection>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(STALE_DESCRIPTION)
    expect(screen.getByText('first half')).toBeInTheDocument()
    expect(screen.getByText('second half')).toBeInTheDocument()
  })

  it.each([
    ['null', null],
    ['undefined', undefined],
    ['false', false],
  ])(
    'keeps the stale notice when the content renders %s',
    (_label, content) => {
      renderWithProviders(
        <AsyncSection
          pending={false}
          payload={['alpha']}
          empty={false}
          loading={<p>loading node</p>}
          errorState={ERROR_STATE}
          emptyState={EMPTY_STATE}
          staleState={staleStateOf(vi.fn())}
        >
          {() => content}
        </AsyncSection>,
      )
      expect(screen.getByRole('alert')).toHaveTextContent(STALE_DESCRIPTION)
    },
  )

  it('keeps the content and notice when empty is true but emptyState is omitted', () => {
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        staleState={staleStateOf(vi.fn())}
      >
        {(items) => <p>content {items.length}</p>}
      </AsyncSection>,
    )
    // No empty placeholder to take over: the content callback still renders
    // the settled payload, with the stale notice above it.
    expect(screen.getByText('content 0')).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent(STALE_DESCRIPTION)
  })

  it('renders the stale notice above the empty placeholder for an empty payload', () => {
    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={staleStateOf(vi.fn())}
      >
        {(items) => <p>content {items.length}</p>}
      </AsyncSection>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(STALE_DESCRIPTION)
    const title = screen.getByText(EMPTY_STATE.title)
    expect(title).toBeInTheDocument()
    expect(
      alert.compareDocumentPosition(title) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(alert).not.toContainElement(title)
    expect(screen.queryByText(/content/)).not.toBeInTheDocument()
  })

  it('keeps the empty placeholder and its icon while the stale notice appears and withdraws', async () => {
    const onRetry = vi.fn()
    const view = renderWithProviders(
      <EmptyPayloadStaleHost onRetry={onRetry} />,
    )

    const title = view.getByText(EMPTY_STATE.title)
    const placeholder = title.parentElement
    expect(placeholder).not.toBeNull()
    expect(view.queryByText(/content/)).not.toBeInTheDocument()

    // The placeholder carries the EMPTY variant's icon, not the error
    // placeholder's: mutating the variant must fail here.
    const icon = placeholder!.querySelector('svg')
    expect(icon).not.toBeNull()
    expect(icon!.outerHTML).toBe(
      render(<EmptyBoxIcon />).container.innerHTML,
    )

    // The notice stands above the placeholder, as a sibling rather than a
    // container around it.
    const alert = view.getByRole('alert')
    expect(alert).toHaveTextContent(STALE_DESCRIPTION)
    expect(
      alert.compareDocumentPosition(placeholder!) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(alert).not.toContainElement(placeholder!)

    // Withdrawing the marker unmounts the notice and leaves the same
    // placeholder node -- and the same icon -- in place.
    await userEvent.click(view.getByRole('button', { name: 'toggle stale' }))
    expect(view.queryByRole('alert')).toBeNull()
    const withdrawnTitle = view.getByText(EMPTY_STATE.title)
    expect(withdrawnTitle.parentElement).toBe(placeholder)
    expect(withdrawnTitle.parentElement!.querySelector('svg')!.outerHTML).toBe(
      icon!.outerHTML,
    )

    // Re-supplying it mounts the notice anew; the placeholder is still the
    // same node.
    await userEvent.click(view.getByRole('button', { name: 'toggle stale' }))
    expect(view.getByRole('alert')).toHaveTextContent(STALE_DESCRIPTION)
    expect(view.getByText(EMPTY_STATE.title).parentElement).toBe(placeholder)
  })

  it('reports the stale retry from under the empty placeholder', async () => {
    const onRetry = vi.fn()
    const view = renderWithProviders(
      <EmptyPayloadStaleHost onRetry={onRetry} />,
    )
    expect(view.getByText(EMPTY_STATE.title)).toBeInTheDocument()
    const retry = view.getByRole('button', { name: STALE_RETRY_LABEL })
    expect(view.getByRole('alert')).toContainElement(retry)
    await userEvent.click(retry)
    expect(onRetry).toHaveBeenCalledTimes(1)
    // The marker is the host's state: clicking the retry does not
    // dissolve it on its own.
    expect(view.getByRole('alert')).toBeInTheDocument()
  })

  it('replaces the content with the refused placeholder and no retry', () => {
    const children = vi.fn(() => <p>content</p>)
    renderWithProviders(
      <div>
        <h1>Page title</h1>
        <AsyncSection
          pending={false}
          payload={['alpha']}
          empty={false}
          loading={<p>loading node</p>}
          errorState={ERROR_STATE}
          emptyState={EMPTY_STATE}
          refusedState={REFUSED_STATE}
        >
          {children}
        </AsyncSection>
      </div>,
    )
    // The caller decided the read was refused: the no-permission
    // placeholder takes the content's place, and nothing offers a retry
    // that could only be refused again.
    expect(screen.getByText(REFUSED_STATE.title)).toBeInTheDocument()
    expect(screen.getByText(REFUSED_STATE.description)).toBeInTheDocument()
    expect(screen.queryByText('content')).not.toBeInTheDocument()
    expect(children).not.toHaveBeenCalled()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })

  it('renders the refused placeholder over the empty, error and stale branches', () => {
    const first = renderWithProviders(
      <AsyncSection
        pending={false}
        payload={[]}
        empty
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={staleStateOf(vi.fn())}
        refusedState={REFUSED_STATE}
      >
        {(items) => <p>content {items.length}</p>}
      </AsyncSection>,
    )
    expect(screen.getByText(REFUSED_STATE.title)).toBeInTheDocument()
    expect(screen.queryByText(EMPTY_STATE.title)).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    first.unmount()

    renderWithProviders(
      <AsyncSection
        pending={false}
        payload={undefined}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        refusedState={REFUSED_STATE}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    // A refusal is not a load failure: the retryable error placeholder and
    // its retry action are replaced too.
    expect(screen.getByText(REFUSED_STATE.title)).toBeInTheDocument()
    expect(screen.queryByText(ERROR_STATE.title)).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: ERROR_STATE.retryLabel }),
    ).toBeNull()
  })

  it('keeps rendering the loading branch while pending even with a refusal marker', () => {
    renderWithProviders(
      <AsyncSection
        pending
        payload={undefined}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        refusedState={REFUSED_STATE}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    expect(screen.getByText('loading node')).toBeInTheDocument()
    expect(screen.queryByText(REFUSED_STATE.title)).not.toBeInTheDocument()
  })

  it('renders no stale notice while pending or in the error branch', () => {
    const pendingView = renderWithProviders(
      <AsyncSection
        pending
        payload={['alpha']}
        empty={false}
        loading={<p>loading node</p>}
        errorState={ERROR_STATE}
        emptyState={EMPTY_STATE}
        staleState={staleStateOf(vi.fn())}
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
        staleState={staleStateOf(vi.fn())}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    // A settled read with no payload takes the error placeholder; stale
    // marking belongs to the branch whose data is still on screen.
    expect(screen.getByText(ERROR_STATE.title)).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('forwards sx, className and ref to a root box around the branch', () => {
    const ref = createRef<HTMLDivElement>()
    const { container } = renderWithProviders(
      <AsyncSection
        ref={ref}
        className="host-section"
        sx={{ mt: 4 }}
        pending={false}
        payload={['a']}
        empty={false}
        loading={<p>Loading</p>}
        errorState={ERROR_STATE}
      >
        {(items: readonly string[]) => <p>content {items.join(',')}</p>}
      </AsyncSection>,
    )
    const root = container.firstChild as HTMLElement
    expect(root).toHaveClass('host-section')
    expect(root).toBe(ref.current)
    expect(root).toHaveStyle({ marginTop: '32px' })
    expect(root).toHaveTextContent('content a')
  })
})
