/**
 * The demo host's vitest setup side effect: jest-dom matchers, explicit RTL
 * cleanup, and a desktop-default window.matchMedia stub.
 *
 * RTL's automatic cleanup only fires when the test framework exposes afterEach
 * globally (it checks globalThis), which vitest does not, so the import must be
 * explicit or a rendered tree leaks into the next test. jsdom implements no
 * matchMedia either, and AppShell's responsive split reads it through MUI's
 * useMediaQuery, so the stub reports the desktop branch by default.
 */

import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

window.matchMedia = vi.fn().mockImplementation((query: string) => ({
  matches: true,
  media: query,
  onchange: null,
  addListener: () => {},
  removeListener: () => {},
  addEventListener: () => {},
  removeEventListener: () => {},
  dispatchEvent: () => true,
})) as unknown as typeof window.matchMedia

afterEach(() => {
  cleanup()
})
