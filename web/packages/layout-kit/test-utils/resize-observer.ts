/**
 * ResizeObserver stub for tests that exercise AppShell's measured-header
 * mechanism.
 *
 * jsdom implements no ResizeObserver (and the browsers AppShell runs in
 * do, so the guard in AppShell's own effect skips the mechanism only
 * where the API is genuinely absent). The stub installs a constructor
 * that records, per observer instance, which elements that instance was
 * told to observe and whether it has been disconnected -- so a test can
 * report a box height exactly the way a real observer entry would, and
 * can also prove the effect's lifecycle: a re-run disconnects the old
 * observer, mounts a new one, and unmount leaves none connected. The
 * per-instance record is what makes the lifecycle observable; a shared
 * set cannot tell a disconnected observer from a live one.
 *
 * Several elements may be observed through one observer (AppShell
 * measures its header and its banner slot together); `emitHeightFor`
 * addresses one of them, `emitHeight` the most recently observed.
 * Install with `vi.unstubAllGlobals()` (or the test file's afterEach)
 * so the stub never leaks into tests that want the no-ResizeObserver
 * path.
 */

import { act } from '@testing-library/react'
import { vi } from 'vitest'

/** One observer instance the stub constructor created. */
export interface ResizeObserverInstance {
  /** Every element this instance was told to observe, in order. */
  readonly observed: readonly Element[]
  /** Whether `disconnect()` has been called on this instance. */
  readonly disconnected: boolean
}

export interface ResizeObserverHandle {
  /** The most recently observed element of a connected instance. */
  observedElement(): Element | null
  /**
   * The union of the observed elements of the connected instances, in
   * observation order. A disconnected instance contributes nothing.
   */
  observedElements(): readonly Element[]
  /** Every instance created, connected or not, in creation order. */
  instances(): readonly ResizeObserverInstance[]
  /** Report a new content-box height for the last observed element. */
  emitHeight(height: number): void
  /** Report a new content-box height for one observed element. */
  emitHeightFor(element: Element, height: number): void
}

interface ObserverRecord {
  readonly callback: (entries: ResizeObserverEntry[]) => void
  readonly observed: Element[]
  connected: boolean
}

export function stubResizeObserver(): ResizeObserverHandle {
  const records: ObserverRecord[] = []

  class StubResizeObserver {
    private readonly record: ObserverRecord

    constructor(callback: (entries: ResizeObserverEntry[]) => void) {
      this.record = { callback, observed: [], connected: true }
      records.push(this.record)
    }

    observe(element: Element): void {
      if (!this.record.observed.includes(element)) {
        this.record.observed.push(element)
      }
    }

    unobserve(element: Element): void {
      const index = this.record.observed.indexOf(element)
      if (index >= 0) {
        this.record.observed.splice(index, 1)
      }
    }

    disconnect(): void {
      this.record.connected = false
      this.record.observed.length = 0
    }
  }

  vi.stubGlobal('ResizeObserver', StubResizeObserver)

  const liveRecords = (): readonly ObserverRecord[] =>
    records.filter((record) => record.connected)

  const observedElements = (): readonly Element[] => {
    const elements: Element[] = []
    for (const record of liveRecords()) {
      for (const element of record.observed) {
        if (!elements.includes(element)) {
          elements.push(element)
        }
      }
    }
    return elements
  }

  const observedElement = (): Element | null => observedElements().at(-1) ?? null

  const emit = (target: Element, height: number): void => {
    const record = liveRecords().find((candidate) =>
      candidate.observed.includes(target),
    )
    if (record === undefined) {
      throw new Error(
        'stubResizeObserver: no connected observer watches that element',
      )
    }
    act(() => {
      record.callback([
        {
          target,
          contentRect: { height },
        } as unknown as ResizeObserverEntry,
      ])
    })
  }

  return {
    observedElement,
    observedElements,
    instances: () =>
      records.map((record) => ({
        observed: [...record.observed],
        disconnected: !record.connected,
      })),
    emitHeight(height: number): void {
      const element = observedElement()
      if (element === null) {
        throw new Error('stubResizeObserver.emitHeight: nothing observed yet')
      }
      emit(element, height)
    },
    emitHeightFor(element: Element, height: number): void {
      emit(element, height)
    },
  }
}
