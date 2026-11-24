import { describe, expect, it, vi } from 'vitest'
import { restoreRealTimers } from '@speed/test-utils/timers'
import { act, fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import type { ReactElement } from 'react'
import { ApiError } from '@speed/api-client'
import type {
  EventStreamHandle,
  EventStreamOpener,
  EventStreamOptions,
} from '@speed/api-client'
import { TenantProvider } from '@speed/auth-core'
import { renderWithProviders, TEST_TENANT_ID } from '../test-utils/render.js'
import { NotificationStreamProvider } from './NotificationStream.js'

const STREAM_PATH = '/api/v1/notifications/stream'

interface OpenedStream {
  readonly path: string
  readonly options: EventStreamOptions
  closed: number
}

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

  it('re-registers the stream when the identity generation changes', async () => {
    const user = userEvent.setup()
    const { opened, opener } = fakeOpener()
    function Harness(): ReactElement {
      const [generation, setGeneration] = useState(0)
      return (
        <>
          <button type="button" onClick={() => setGeneration((n) => n + 1)}>
            bump
          </button>
          <NotificationStreamProvider
            openStream={opener}
            identityGeneration={generation}
          >
            <div>content</div>
          </NotificationStreamProvider>
        </>
      )
    }
    renderWithProviders(<Harness />)
    expect(opened).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'bump' }))
    expect(opened).toHaveLength(2)
    expect(opened[0]?.closed).toBe(1)
    expect(opened[1]?.path).toBe(STREAM_PATH)
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
      await restoreRealTimers()
    }
  })

  it('reconnects at the base delay when a failure fires onError then onClose', async () => {
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
        opened[0]?.options.onError?.(
          new ApiError({ status: 0, code: 'client.network', attempts: 1 }),
        )
        opened[0]?.options.onClose?.()
      })
      act(() => {
        vi.advanceTimersByTime(999)
      })
      expect(opened).toHaveLength(1)
      act(() => {
        vi.advanceTimersByTime(1)
      })
      expect(opened).toHaveLength(2)
      expect(opened[1]?.path).toBe(STREAM_PATH)
    } finally {
      await restoreRealTimers()
    }
  })

  it('is inert when the host mounts no opener', () => {    const { opened } = fakeOpener()
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

  it('stops reconnecting after the admission layer refuses with a bare 404', async () => {
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
        opened[0]?.options.onError?.(
          new ApiError({ status: 404, code: 'client.http.404', attempts: 1 }),
        )
        opened[0]?.options.onClose?.()
      })
      act(() => {
        vi.advanceTimersByTime(120_000)
      })
      expect(opened).toHaveLength(1)
    } finally {
      await restoreRealTimers()
    }
  })

  it('reads the refusal off the answer status, not only an ApiError code', async () => {
    vi.useFakeTimers()
    try {
      const { opened, opener } = fakeOpener()
      renderWithProviders(
        <NotificationStreamProvider openStream={opener}>
          <div>content</div>
        </NotificationStreamProvider>,
      )
      act(() => {
        opened[0]?.options.onError?.({
          status: 404,
        } as unknown as ApiError)
        opened[0]?.options.onClose?.()
      })
      act(() => {
        vi.advanceTimersByTime(120_000)
      })
      expect(opened).toHaveLength(1)
    } finally {
      await restoreRealTimers()
    }
  })

  it("keeps reconnecting after the module's own typed 404", async () => {
    vi.useFakeTimers()
    try {
      const { opened, opener } = fakeOpener()
      renderWithProviders(
        <NotificationStreamProvider openStream={opener}>
          <div>content</div>
        </NotificationStreamProvider>,
      )
      act(() => {
        opened[0]?.options.onError?.(
          new ApiError({
            status: 404,
            code: 'notification.message_not_found',
            attempts: 1,
          }),
        )
        opened[0]?.options.onClose?.()
      })
      act(() => {
        vi.advanceTimersByTime(1000)
      })
      expect(opened).toHaveLength(2)
    } finally {
      await restoreRealTimers()
    }
  })

  it('reopens after a refusal when the tenant changes', () => {
    const { opened, opener } = fakeOpener()
    function TenantHarness(): ReactElement {
      const [tenantId, setTenantId] = useState(TEST_TENANT_ID)
      return (
        <>
          <button type="button" onClick={() => setTenantId('other-tenant')}>
            switch
          </button>
          <TenantProvider tenantId={tenantId}>
            <NotificationStreamProvider openStream={opener}>
              <div>content</div>
            </NotificationStreamProvider>
          </TenantProvider>
        </>
      )
    }
    renderWithProviders(<TenantHarness />)
    expect(opened).toHaveLength(1)
    act(() => {
      opened[0]?.options.onError?.(
        new ApiError({ status: 404, code: 'client.http.404', attempts: 1 }),
      )
      opened[0]?.options.onClose?.()
    })
    expect(opened).toHaveLength(1)
    fireEvent.click(screen.getByRole('button', { name: 'switch' }))
    expect(opened).toHaveLength(2)
    expect(opened[1]?.path).toBe(STREAM_PATH)
  })

  it('reopens after a refusal when a hidden tab becomes visible', () => {
    const { opened, opener } = fakeOpener()
    renderWithProviders(
      <NotificationStreamProvider openStream={opener}>
        <div>content</div>
      </NotificationStreamProvider>,
    )
    expect(opened).toHaveLength(1)
    act(() => {
      opened[0]?.options.onError?.(
        new ApiError({ status: 404, code: 'client.http.404', attempts: 1 }),
      )
      opened[0]?.options.onClose?.()
    })
    expect(opened).toHaveLength(1)
    try {
      act(() => {
        setDocumentVisibility('hidden')
        document.dispatchEvent(new Event('visibilitychange'))
      })
      act(() => {
        setDocumentVisibility('visible')
        document.dispatchEvent(new Event('visibilitychange'))
      })
      expect(opened).toHaveLength(2)
      expect(opened[1]?.path).toBe(STREAM_PATH)
    } finally {
      Reflect.deleteProperty(document, 'visibilityState')
    }
  })
})

function setDocumentVisibility(state: 'visible' | 'hidden'): void {
  Object.defineProperty(document, 'visibilityState', {
    configurable: true,
    get: () => state,
  })
}
