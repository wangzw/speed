/**
 * The device color-mode source: the read, the server snapshot and the
 * media-query subscription the 'system' preference follows. Every case
 * asserts the degrade-to-light contract, because a color mode read must
 * never fail a render.
 */

import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  SYSTEM_COLOR_MODE_QUERY,
  getServerSystemColorMode,
  readSystemColorMode,
  subscribeSystemColorMode,
} from './system-color-mode.js'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('readSystemColorMode', () => {
  it('answers light when there is no matchMedia', () => {
    // jsdom ships no matchMedia; stating it keeps this case honest even
    // if a future shared setup installs a default stub.
    vi.stubGlobal('matchMedia', undefined)
    expect(readSystemColorMode()).toBe('light')
  })

  it('answers dark when the color-scheme query matches', () => {
    const matchMedia = vi.fn(() => ({ matches: true }))
    vi.stubGlobal('matchMedia', matchMedia)
    expect(readSystemColorMode()).toBe('dark')
    expect(matchMedia).toHaveBeenCalledWith(SYSTEM_COLOR_MODE_QUERY)
  })

  it('answers light when the color-scheme query does not match', () => {
    vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })))
    expect(readSystemColorMode()).toBe('light')
  })

  it('answers light when constructing the media query throws', () => {
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => {
        throw new Error('matchMedia is not supported')
      }),
    )
    expect(readSystemColorMode()).toBe('light')
  })
})

describe('getServerSystemColorMode', () => {
  it('answers light: a server render has no device preference', () => {
    expect(getServerSystemColorMode()).toBe('light')
  })
})

describe('subscribeSystemColorMode', () => {
  it('registers a change listener and releases it on unsubscribe', () => {
    const listeners = new Set<() => void>()
    const addEventListener = vi.fn(
      (_type: string, listener: () => void): void => {
        listeners.add(listener)
      },
    )
    const removeEventListener = vi.fn(
      (_type: string, listener: () => void): void => {
        listeners.delete(listener)
      },
    )
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({ matches: false, addEventListener, removeEventListener })),
    )
    const onChange = vi.fn()
    const unsubscribe = subscribeSystemColorMode(onChange)

    expect(addEventListener).toHaveBeenCalledWith('change', expect.any(Function))
    expect(listeners.size).toBe(1)
    const listener = addEventListener.mock.calls[0]?.[1]
    listener?.()
    expect(onChange).toHaveBeenCalledTimes(1)

    unsubscribe()
    expect(removeEventListener).toHaveBeenCalledWith('change', listener)
    expect(listeners.size).toBe(0)
  })

  it('falls back to the legacy addListener pair when addEventListener throws', () => {
    const listeners = new Set<() => void>()
    const addListener = vi.fn((listener: () => void): void => {
      listeners.add(listener)
    })
    const removeListener = vi.fn((listener: () => void): void => {
      listeners.delete(listener)
    })
    vi.stubGlobal(
      'matchMedia',
      vi.fn(() => ({
        matches: false,
        addEventListener: (): void => {
          throw new Error('addEventListener is not supported')
        },
        removeEventListener: (): void => {
          throw new Error('removeEventListener is not supported')
        },
        addListener,
        removeListener,
      })),
    )
    const onChange = vi.fn()
    let unsubscribe = (): void => undefined
    expect(() => {
      unsubscribe = subscribeSystemColorMode(onChange)
    }).not.toThrow()

    expect(addListener).toHaveBeenCalledTimes(1)
    const listener = addListener.mock.calls[0]?.[0]
    listener?.()
    expect(onChange).toHaveBeenCalledTimes(1)

    expect(() => unsubscribe()).not.toThrow()
    expect(removeListener).toHaveBeenCalledTimes(1)
  })

  it('returns a no-op unsubscribe when there is no matchMedia', () => {
    vi.stubGlobal('matchMedia', undefined)
    const unsubscribe = subscribeSystemColorMode(() => undefined)
    expect(() => unsubscribe()).not.toThrow()
  })
})
