/**
 * vitest setup for console-ui tests: the workspace's shared setup side
 * effect (see @speed/test-utils/setup) -- jest-dom matchers plus the
 * explicit RTL cleanup between tests -- plus a desktop-true
 * window.matchMedia default before each test.
 *
 * ConsoleShell renders the shared @speed/layout-kit AppShell frame,
 * whose responsive desktop/mobile split asks useMediaQuery for
 * theme.breakpoints.up('md'); jsdom implements no matchMedia, so the
 * stub must be installed before the frame renders or the query has
 * nothing to call. The default is desktop (the shell with the permanent
 * sidebar); a suite exercising the narrow-viewport drawer calls
 * mockMatchMedia(false) itself (see @speed/test-utils/matchMedia).
 */
import '@speed/test-utils/setup'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { beforeEach } from 'vitest'

beforeEach(() => {
  mockMatchMedia(true)
})
