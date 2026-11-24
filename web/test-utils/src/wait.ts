import { getConfig } from '@testing-library/react'
import { vi } from 'vitest'

export interface WaitForConditionOptions {
  readonly interval?: number
  readonly timeout?: number
}

// Read Testing Library's configured budget, not the environment: the
// browser tier's page has no process global. A smaller timeout is ignored.
export function waitForCondition<T>(
  callback: () => T | Promise<T>,
  options: WaitForConditionOptions = {},
): Promise<T> {
  return vi.waitFor(callback, {
    ...options,
    timeout: Math.max(options.timeout ?? 0, getConfig().asyncUtilTimeout),
  })
}
