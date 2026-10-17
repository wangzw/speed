/**
 * ResizeObserver stub for tests that exercise AppShell's measured-header
 * mechanism.
 *
 * jsdom implements no ResizeObserver (and the browsers AppShell runs in
 * do, so the guard in AppShell's own effect skips the mechanism only
 * where the API is genuinely absent). The stub installs a constructor
 * that records the observed elements and the callback, so a test can
 * report a box height exactly the way a real observer entry would and
 * assert how the spacers and offsets derived from it react. Several
 * elements may be observed through one observer (AppShell measures its
 * header and its banner slot together); `emitHeightFor` addresses one of
 * them, `emitHeight` the most recently observed. Install with
 * `vi.unstubAllGlobals()` (or the test file's afterEach) so the stub
 * never leaks into tests that want the no-ResizeObserver path.
 */

import { act } from '@testing-library/react'
import { vi } from 'vitest'

export interface ResizeObserverHandle {
  /** The most recently observed element, if any. */
  observedElement(): Element | null
  /** Every element the stub has been told to observe, in order. */
  observedElements(): readonly Element[]
  /** Report a new content-box height for the last observed element. */
  emitHeight(height: number): void
  /** Report a new content-box height for one observed element. */
  emitHeightFor(element: Element, height: number): void
}

export function stubResizeObserver(): ResizeObserverHandle {
  let callback: ((entries: ResizeObserverEntry[]) => void) | null = null
  let observed: Element | null = null
  const observedSet = new Set<Element>()

  class StubResizeObserver {
    constructor(cb: (entries: ResizeObserverEntry[]) => void) {
      callback = cb
    }

    observe(element: Element): void {
      observed = element
      observedSet.add(element)
    }

    unobserve(): void {}

    disconnect(): void {
      observed = null
      observedSet.clear()
    }
  }

  vi.stubGlobal('ResizeObserver', StubResizeObserver)

  const emit = (target: Element, height: number): void => {
    act(() => {
      if (callback === null) {
        throw new Error('stubResizeObserver: nothing observed yet')
      }
      callback([
        {
          target,
          contentRect: { height },
        } as unknown as ResizeObserverEntry,
      ])
    })
  }

  return {
    observedElement: () => observed,
    observedElements: () => [...observedSet],
    emitHeight(height: number): void {
      if (observed === null) {
        throw new Error('stubResizeObserver.emitHeight: nothing observed yet')
      }
      emit(observed, height)
    },
    emitHeightFor(element: Element, height: number): void {
      if (!observedSet.has(element)) {
        throw new Error(
          'stubResizeObserver.emitHeightFor: the element is not observed',
        )
      }
      emit(element, height)
    },
  }
}
