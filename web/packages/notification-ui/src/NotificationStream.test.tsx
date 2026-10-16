/**
 * NotificationStreamProvider behaviour: it opens the stream at the
 * generated stream path for the acting tenant, re-reads (invalidates) on
 * a `message` frame and ignores other frames, reconnects with backoff
 * after the connection ends, and closes the connection on unmount. The
 * opener is a stand-in; the transport's own framing is api-client's
 * event-stream suite.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import type {
  EventStreamHandle,
  EventStreamOpener,
  EventStreamOptions,
} from '@speed/api-client'
import { renderWithProviders, TEST_TENANT_ID } from '../test-utils/render.js'
import { NotificationStreamProvider } from './NotificationStream.js'

const STREAM_PATH = '/api/v1/notifications/stream'

interface OpenedStream {
  readonly path: string
  readonly options: EventStreamOptions
  closed: number
}

/** A stand-in opener recording every open and its handle's closes. */
function fakeOpener(): {
  readonly opened: OpenedStream[]
  readonly opener: EventStreamOpener
} {
  const opened: OpenedStream[] = []
  const opener: EventStreamOpener = (path, options) => {
    const record: OpenedStream = { path, options, closed: 0 }
    opened.push(record)
    const handle: EventStreamHandle = {
      close: () => {
        record.closed += 1
      },
    }
    return handle
  }
  return { opened, opener }
}

describe('NotificationStreamProvider', () => {
  it('opens the stream path and re-reads on a message frame', async () => {
    const { opened, opener } = fakeOpener()
    const { queryClient, unmount } = renderWithProviders(
      <NotificationStreamProvider openStream={opener}>
        <div>content</div>
      </NotificationStreamProvider>,
    )
    const invalidate = vi.spyOn(queryClient, 'invalidateQueries')
    expect(opened).toHaveLength(1)
    expect(opened[0]?.path).toBe(STREAM_PATH)

    act(() => {
      opened[0]?.options.onEvent({ event: 'message', data: '{"message_id":"m1"}' })
    })
    expect(invalidate).toHaveBeenCalledWith(
      expect.objectContaining({ queryKey: [TEST_TENANT_ID, '/api/v1/notifications/messages'] }),
    )
    expect(invalidate).toHaveBeenCalledWith(
      expect.objectContaining({
        queryKey: [TEST_TENANT_ID, '/api/v1/notifications/messages/unread-count'],
      }),
    )

    invalidate.mockClear()
    act(() => {
      opened[0]?.options.onEvent({ event: 'comment', data: 'ignored' })
    })
    expect(invalidate).not.toHaveBeenCalled()

    unmount()
    expect(opened[0]?.closed).toBe(1)
  })

  it('reconnects after the connection ends', async () => {
    vi.useFakeTimers()
    try {
      const { opened, opener } = fakeOpener()
      renderWithProviders(
        <NotificationStreamProvider openStream={opener}>
          <div>content</div>
        </NotificationStreamProvider>,
      )
      expect(opened).toHaveLength(1)
      act(() => {
        opened[0]?.options.onClose?.()
      })
      act(() => {
        vi.advanceTimersByTime(2000)
      })
      expect(opened.length).toBeGreaterThanOrEqual(2)
      expect(opened[1]?.path).toBe(STREAM_PATH)
    } finally {
      vi.useRealTimers()
    }
  })

  it('is inert when the host mounts no opener', () => {
    const { opened } = fakeOpener()
    const rendered = render(
      <NotificationStreamProvider>
        <div>content</div>
      </NotificationStreamProvider>,
    )
    expect(opened).toHaveLength(0)
    rendered.unmount()
  })

  it('opens nothing while disabled, then opens once enabled', async () => {
    const { opened, opener } = fakeOpener()
    function Gated() {
      const [enabled, setEnabled] = useState(false)
      return (
        <>
          <button type="button" onClick={() => setEnabled(true)}>
            enable
          </button>
          <NotificationStreamProvider openStream={opener} enabled={enabled}>
            <div>content</div>
          </NotificationStreamProvider>
        </>
      )
    }
    renderWithProviders(<Gated />)
    expect(opened).toHaveLength(0)
    await userEvent.click(screen.getByRole('button', { name: 'enable' }))
    expect(opened).toHaveLength(1)
  })
})
