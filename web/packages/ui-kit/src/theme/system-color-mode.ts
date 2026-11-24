/** The device color mode behind the 'system' preference; every unavailable path answers 'light'. */

import { useSyncExternalStore } from 'react'
import type { AppThemeMode } from './createAppTheme.js'

/** The media feature the 'system' preference follows. */
export const SYSTEM_COLOR_MODE_QUERY = '(prefers-color-scheme: dark)'

/** A stable no-op, shared so disabled subscriptions keep one identity. */
const NOOP = (): void => undefined

function noopSubscribe(): () => void {
  return NOOP
}

function systemColorModeQuery(): MediaQueryList | null {
  if (typeof window === 'undefined') {
    return null
  }
  if (typeof window.matchMedia !== 'function') {
    return null
  }
  try {
    return window.matchMedia(SYSTEM_COLOR_MODE_QUERY)
  } catch {
    return null
  }
}

/** 'dark' when the media query matches, 'light' in every other case. */
export function readSystemColorMode(): AppThemeMode {
  const query = systemColorModeQuery()
  if (query === null) {
    return 'light'
  }
  try {
    return query.matches ? 'dark' : 'light'
  } catch {
    return 'light'
  }
}

/** The server snapshot. */
export function getServerSystemColorMode(): AppThemeMode {
  return 'light'
}

/**
 * Subscribes to the media query's 'change' event, through the legacy
 * `addListener` pair when `addEventListener` throws. Any failure leaves a
 * no-op unsubscribe; the returned function never throws.
 */
export function subscribeSystemColorMode(onChange: () => void): () => void {
  const query = systemColorModeQuery()
  if (query === null) {
    return NOOP
  }
  const listener = (): void => {
    onChange()
  }
  try {
    query.addEventListener('change', listener)
    return () => {
      try {
        query.removeEventListener('change', listener)
      } catch {
        // The list is gone; there is nothing left to release.
      }
    }
  } catch {
    // Legacy MediaQueryList: fall through to addListener/removeListener.
  }
  try {
    query.addListener(listener)
    return () => {
      try {
        query.removeListener(listener)
      } catch {
        // The list is gone; there is nothing left to release.
      }
    }
  } catch {
    return NOOP
  }
}

/**
 * The device color mode, subscribed only while `enabled` is true. A
 * disabled read still answers the current mode but registers no
 * media-query listener.
 */
export function useSystemColorMode(enabled: boolean): AppThemeMode {
  return useSyncExternalStore(
    enabled ? subscribeSystemColorMode : noopSubscribe,
    readSystemColorMode,
    getServerSystemColorMode,
  )
}
