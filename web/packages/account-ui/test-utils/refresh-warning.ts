/**
 * The refresh-failure warning capture the account journeys share.
 */

import { expect, onTestFinished, vi } from 'vitest'
import type { MockInstance } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'

const REFRESH_WARNING = 'access token refresh failed'

export interface RefreshWarningOptions {
  readonly expected?: boolean
}

export function captureRefreshWarning(
  options: RefreshWarningOptions = {},
): MockInstance {
  const original = console.warn
  const warn = vi.spyOn(console, 'warn').mockImplementation((message, attrs) => {
    if (message !== REFRESH_WARNING) {
      original(message, attrs)
    }
  })
  // Registered first so it runs last: onTestFinished runs callbacks in reverse.
  onTestFinished(() => {
    warn.mockRestore()
  })
  if (options.expected === true) {
  // The diagnostic can land a tick after the body, so wait for it.
    onTestFinished(async () => {
      await waitForCondition(() =>
        expect(warn).toHaveBeenCalledWith(
          REFRESH_WARNING,
          expect.objectContaining({ status: 401 }),
        ),
      )
    })
  }
  return warn
}
