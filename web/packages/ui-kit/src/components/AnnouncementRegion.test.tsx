
import { useState } from 'react'
import { flushSync } from 'react-dom'
import { act, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { renderWithProviders } from '../../test-utils/render.js'
import { AnnouncementRegion } from './AnnouncementRegion.js'

const REGION_ID = 'announcement-under-test'
const FIRST = 'The first message'
const SECOND = 'The second message'

function Host({ initial }: { readonly initial: string | null }) {
  const [message, setMessage] = useState(initial)
  return (
    <div>
      <button type="button" onClick={() => setMessage(FIRST)}>
        first
      </button>
      <button type="button" onClick={() => setMessage(SECOND)}>
        second
      </button>
      <button type="button" onClick={() => setMessage(null)}>
        clear
      </button>
      <AnnouncementRegion id={REGION_ID} message={message} />
    </div>
  )
}

function region(): HTMLElement {
  const node = document.getElementById(REGION_ID)
  expect(node).not.toBeNull()
  return node as HTMLElement
}

describe('AnnouncementRegion', () => {
  it('is born empty and fills only after a painted frame', async () => {
    renderWithProviders(<AnnouncementRegion id={REGION_ID} message={FIRST} />)

    expect(region().textContent).toBe('')
    expect(region()).not.toHaveAttribute('data-announcement-written')

    await waitFor(() => expect(region()).toHaveTextContent(FIRST))
    expect(region()).toHaveAttribute('data-announcement-written')
    expect(screen.getAllByRole('status')).toHaveLength(1)
  })

  it('renders an empty region for a null message', () => {
    renderWithProviders(<AnnouncementRegion id={REGION_ID} message={null} />)
    expect(region().textContent).toBe('')
    expect(region()).toBeInTheDocument()
  })

  it('renders an empty region for an empty string', () => {
    renderWithProviders(<AnnouncementRegion id={REGION_ID} message="" />)
    expect(region().textContent).toBe('')
  })

  it('carries the status role, a polite live setting and aria-atomic', () => {
    renderWithProviders(<AnnouncementRegion id={REGION_ID} message={null} />)
    expect(region()).toHaveAttribute('role', 'status')
    expect(region()).toHaveAttribute('aria-live', 'polite')
    expect(region()).toHaveAttribute('aria-atomic', 'true')
  })

  it('carries the alert role alone, with no second live setting, and aria-atomic', async () => {
    renderWithProviders(
      <AnnouncementRegion id={REGION_ID} message={FIRST} politeness="alert" />,
    )
    expect(region()).toHaveAttribute('role', 'alert')
    expect(region()).not.toHaveAttribute('aria-live')
    expect(region()).toHaveAttribute('aria-atomic', 'true')
    expect(region().textContent).toBe('')
    await waitFor(() => expect(region()).toHaveTextContent(FIRST))
  })

  it('forwards sx to the region root', () => {
    renderWithProviders(
      <AnnouncementRegion
        id={REGION_ID}
        message={null}
        sx={{ marginTop: '37px' }}
      />,
    )
    expect(region()).toHaveStyle({ marginTop: '37px' })
  })

  it('starts a change from empty to text from an empty frame', async () => {
    renderWithProviders(<Host initial={null} />)
    const regionAtStart = region()

    act(() => {
      flushSync(() => {
        screen.getByRole('button', { name: 'first' }).click()
      })
      expect(region().textContent).toBe('')
    })

    await waitFor(() => expect(regionAtStart).toHaveTextContent(FIRST))
  })

  it('starts a change from one text to another from an empty frame', async () => {
    renderWithProviders(<Host initial={FIRST} />)
    await waitFor(() => expect(region()).toHaveTextContent(FIRST))

    act(() => {
      flushSync(() => {
        screen.getByRole('button', { name: 'second' }).click()
      })
      expect(region().textContent).toBe('')
    })

    await waitFor(() => expect(region()).toHaveTextContent(SECOND))
  })

  it('clears in the commit that the message empties in', async () => {
    renderWithProviders(<Host initial={FIRST} />)
    await waitFor(() => expect(region()).toHaveTextContent(FIRST))

    act(() => {
      flushSync(() => {
        screen.getByRole('button', { name: 'clear' }).click()
      })
      expect(region().textContent).toBe('')
    })

    await waitFor(() => expect(region().textContent).toBe(''))
  })

  it('cancels a scheduled write when it unmounts', () => {
    const request = vi.spyOn(globalThis, 'requestAnimationFrame')
    const cancel = vi.spyOn(globalThis, 'cancelAnimationFrame')
    try {
      const view = renderWithProviders(
        <AnnouncementRegion id={REGION_ID} message={FIRST} />,
      )
      expect(region().textContent).toBe('')
      expect(request).toHaveBeenCalled()
      view.unmount()
      expect(cancel).toHaveBeenCalled()
    } finally {
      request.mockRestore()
      cancel.mockRestore()
    }
  })
})
