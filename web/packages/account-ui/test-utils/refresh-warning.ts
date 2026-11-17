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

import { expect, onTestFinished, vi } from 'vitest'
import type { MockInstance } from 'vitest'

const REFRESH_WARNING = 'access token refresh failed'

/** How strictly the capture treats the diagnostic as required. */
export interface RefreshWarningOptions {
  /**
   * Assert, when the test finishes, that the diagnostic was reported. A case
   * whose path must produce it sets this, so a silent removal of the client's
   * report fails the case; a case that merely silences a diagnostic its path
   * may or may not reach leaves it off.
   */
  readonly expected?: boolean
}

/**
 * Captures `access token refresh failed` for the current test, forwarding
 * every other warning to the real console. The spy is restored when the
 * test finishes, so a failing assertion cannot leak it into the next test.
 * With `expected`, the finish also asserts the diagnostic was reported.
 */
export function captureRefreshWarning(
  options: RefreshWarningOptions = {},
): MockInstance {
  const original = console.warn
  const warn = vi.spyOn(console, 'warn').mockImplementation((message, attrs) => {
    if (message !== REFRESH_WARNING) {
      original(message, attrs)
    }
  })
  // Registered first so it runs LAST: onTestFinished callbacks run in reverse
  // order, and a restored spy has cleared its calls.
  onTestFinished(() => {
    warn.mockRestore()
  })
  if (options.expected === true) {
    // Some paths report the failure from a read the test does not await, so
    // the diagnostic can land a tick after the body; wait for it rather than
    // reading an empty spy. A path that never reports it still fails here.
    onTestFinished(async () => {
      await vi.waitFor(() =>
        expect(warn).toHaveBeenCalledWith(
          REFRESH_WARNING,
          expect.objectContaining({ status: 401 }),
        ),
      )
    })
  }
  return warn
}
