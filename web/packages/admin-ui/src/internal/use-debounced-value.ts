/**
 * useDebouncedValue: the settled value of a fast-changing input.
 *
 * A search box drives a server read; forwarding every keystroke would spend
 * one request per character and race their answers. This hook holds the last
 * value for `delayMs` of quiet before publishing it, resetting the timer on
 * every change, and returns the latest value once the delay elapses. The
 * initial value settles immediately, so a page that mounts with a seeded
 * query does not pause before its first read.
 *
 * The timer is cleaned up on unmount and on every change, so a value never
 * lands after the component that watches it is gone.
 */

import { useEffect, useState } from 'react'

/** The console's search debounce: one value after roughly a typing pause. */
export const SEARCH_DEBOUNCE_MS = 300

export function useDebouncedValue<T>(value: T, delayMs = SEARCH_DEBOUNCE_MS): T {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setSettled(value), delayMs)
    return () => clearTimeout(timer)
  }, [value, delayMs])
  return settled
}
