import { act } from '@testing-library/react'
import { vi } from 'vitest'

export interface ResizeObserverInstance {
  readonly observed: readonly Element[]
  readonly disconnected: boolean
}

export interface ResizeObserverHandle {
  observedElement(): Element | null
  observedElements(): readonly Element[]
  instances(): readonly ResizeObserverInstance[]
  emitHeight(height: number): void
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
