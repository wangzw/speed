/**
 * The refresh-failure warning capture the account journeys share.
 *
 * The journeys run over the real api-client with its default console
 * reporter. A scripted 401 -- the wrong-current-password refusal or the
 * step-up shortfall -- makes the client try the refresh leg, and a refresh
 * that cannot succeed is reported through the reporter's warn channel as
 * `access token refresh failed`. That diagnostic is the expected log of
 * the path under test, not a defect, so the tests capture it and assert
 * the envelope it carries instead of letting it print on every journey.
 *
 * Only that one message is silenced: every other console.warn still
 * reaches the console, so an unexpected warning surfaces rather than being
 * swallowed with the known one.
 */

import { onTestFinished, vi } from 'vitest'
import type { MockInstance } from 'vitest'

const REFRESH_WARNING = 'access token refresh failed'

/**
 * Captures `access token refresh failed` for the current test, forwarding
 * every other warning to the real console. The spy is restored when the
 * test finishes, so a failing assertion cannot leak it into the next test.
 */
export function captureRefreshWarning(): MockInstance {
  const original = console.warn
  const warn = vi.spyOn(console, 'warn').mockImplementation((message, attrs) => {
    if (message !== REFRESH_WARNING) {
      original(message, attrs)
    }
  })
  onTestFinished(() => {
    warn.mockRestore()
  })
  return warn
}
