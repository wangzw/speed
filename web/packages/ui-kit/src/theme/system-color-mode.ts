/**
 * The device color mode behind the 'system' preference.
 *
 * `createAppTheme` accepts only a resolved `AppThemeMode`; 'system' is
 * resolved here, at the provider layer, by reading the
 * `prefers-color-scheme` media query and subscribing to its changes. The
 * module is internal: the entry exports `AppThemeModePreference` and the
 * provider, not this read/subscribe pair.
 *
 * Every path degrades to 'light'. A server render has no window; a
 * browser without `matchMedia`, a list whose construction throws, and a
 * `matches` read that throws all answer 'light' rather than failing the
 * render. Subscriptions follow the same rule: any failure leaves a no-op
 * unsubscribe, and `subscribeSystemColorMode` never throws.
 */

import { useSyncExternalStore } from 'react'
import type { AppThemeMode } from './createAppTheme.js'

/** The media feature the 'system' preference follows. */
export const SYSTEM_COLOR_MODE_QUERY = '(prefers-color-scheme: dark)'

/** A stable no-op, shared so disabled subscriptions keep one identity. */
const NOOP = (): void => undefined

/** A subscribe function that never registers anything. */
function noopSubscribe(): () => void {
  return NOOP
}

/**
 * The media query list, or null when there is no window, no matchMedia,
 * or the construction throws.
 */
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

/**
 * The device's color mode right now: 'dark' when the media query matches,
 * 'light' in every other case (no window, no matchMedia, or a construction
 * or read that throws).
 */
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

/**
 * The server snapshot: a server render has no device preference to read,
 * so it answers 'light'.
 */
export function getServerSystemColorMode(): AppThemeMode {
  return 'light'
}

/**
 * Subscribe to the media query's 'change' event. Engines whose
 * `MediaQueryList` predates `addEventListener` are reached through the
 * legacy `addListener` pair when the modern call throws. Any failure --
 * no window, no matchMedia, or both subscription paths throwing -- leaves
 * a no-op unsubscribe; the returned function never throws either.
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
 * disabled read still answers the current mode (the caller ignores it
 * while following an explicit preference), but registers no media-query
 * listener.
 */
export function useSystemColorMode(enabled: boolean): AppThemeMode {
  return useSyncExternalStore(
    enabled ? subscribeSystemColorMode : noopSubscribe,
    readSystemColorMode,
    getServerSystemColorMode,
  )
}
