/**
 * useDebouncedValue: publishes the latest input after `delayMs` of quiet,
 * resetting the timer on every change. The initial value settles immediately,
 * and the timer is cleared on change and unmount.
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
